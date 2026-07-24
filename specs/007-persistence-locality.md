# Spec 007: Persistence Grounding and LLM Locality Enforcement

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/001-constitution.md`–`specs/003-controller.md` executed; ideally after 005 (`metrics/`) and 008 (`agent/`) so the audit table is complete, but the gate and README fix stand alone |
| Produces | Canonical persistence map, a deterministic LLM-locality gate in `scripts/check.sh`, persistence + locality assertions in smoke/e2e, README correction removing non-local modes |
| Followed by | Binds all future specs (any new stateful component must appear in the §1 table) |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. The new gate is **deterministic and network-free** (constitution rule 5): it is pure static analysis of tracked files.
- This spec adds enforcement; it does not change runtime behavior. It **supersedes** the README "modes" list (constitution rule 4): the Low-Cost/Balanced/Best OpenRouter/native-provider modes are aspirational and contradict the current local-only implementation, so they are removed (§3). No existing spec file is edited.
- Stop-on-red per section; finish with the Acceptance Checklist (§6).

## 1. Canonical persistence map

Every stateful component and its **required** location. Persistent state lives only under the single rw data mount `$VM_DATA_DIR` (`/home/virtualme/.virtualme`, spec 002 §1). Anything not in the "persistent" group is intentionally ephemeral or image-baked; new stateful components MUST be added to this table by their introducing spec.

### 1a. Persistent — under `$VM_DATA_DIR` (survives `stop`/`start` and image replacement)

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| Chat history | `$VM_DATA_DIR/valkey/` (AOF) | `svc-valkey` `--appendonly yes --dir` | 002/003 |
| Conversation stats | `$VM_DATA_DIR/valkey/` (AOF, key `virtualme:chat-stats`) | chat service | 005 |
| Chromium profile (cookies, history, `Preferences`, `Local State`) | `$VM_DATA_DIR/chromium/` | `--user-data-dir` + clean-shutdown flush | 002/006 |
| XDG config/cache/data | `$VM_DATA_DIR/xdg/{config,cache,data}/` | `XDG_*` env redirect | 002 |
| Metrics tiers | `$VM_DATA_DIR/metrics/tier{0,1,2,3}.json` | `metrics.Store` atomic writes | 005 |
| Agent artifacts (screenshots, step logs) | `$VM_DATA_DIR/agent/` | agent loop, bounded retention | 008 |

All of the above directories are created by `docker/rootfs/etc/cont-init.d/10-data-dirs.sh`. This spec requires that script's `mkdir -p` list to be a **superset** of the persistent paths in this table (005 adds `metrics/`, 008 adds `agent/`); the gate in §2c enforces it.

### 1b. Intentionally ephemeral (lost on container stop — by design)

| State | Path | Rationale |
|---|---|---|
| X11 sockets | `/tmp/.X11-unix` (tmpfs `/tmp`) | recreated by Xvfb each boot |
| s6 runtime / service supervision | `/run` (tmpfs) | `S6_READ_ONLY_ROOT=1` staging |
| VNC / noVNC transient state | memory + localhost ports | `-nopw`, no persistent store by design (trust model) |
| Controller metrics ring (live tier-0 in RAM) | process memory | mirrored to `metrics/` every 60 s |
| llama KV cache / context | process memory | recomputed per request |
| Container logs | Docker log driver (`docker logs`) | **decision:** logs stay ephemeral via `docker logs`, not on the data mount |

### 1c. Image-baked read-only (lost only if the image is removed; never written at runtime)

| State | Path |
|---|---|
| LLM weights (Gemma 4 E2B GGUF) | `/opt/models/` (`0444`) |
| llama.cpp runtime | `/opt/llama/` |
| Playwright/agent deps | `/opt/agent/` |
| Vision projector (mmproj), if present | `/opt/models/` (spec 008) |
| Controller binary | `/usr/local/bin/controller` |

## 2. Deterministic gates (`scripts/check.sh`)

Add a new gate block (network-free, tracked-files-only) after the existing shell-syntax step and before eslint. All three checks are pure `grep`/file inspection over the working tree.

### 2a. LLM locality

Every LLM endpoint referenced by **runtime** code must be loopback-only. Implement as a helper `scripts/check-llm-local.sh` invoked by `check.sh`:

- Scope: `controller/**/*.go` (excluding `*_test.go`) and `docker/rootfs/**` (the `svc-llama` launch + any agent wiring).
- Rule: collect every URL/host that appears on a line also mentioning an LLM surface (`/v1/chat/completions`, `/v1/completions`, `/completion`, `/slots`, `/health`, `/props`, `llama`, `VM_LLAMA`). Each such host MUST be `127.0.0.1`, `localhost`, or an env var that defaults to one of those (`VM_LLAMA_PORT`, `VM_LLAMA_*`). Any other scheme/host (`http(s)://` to a non-loopback literal, or known provider domains `openai.com`, `openrouter.ai`, `anthropic.com`, `googleapis.com`, `api.*`) → **FAIL** with the offending file:line.
- Additionally FAIL on any occurrence of provider API-key env names in runtime code: `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, `GEMINI_API_KEY`, `HF_TOKEN` (build-time model fetch in `docker/layers/003-model.sh` is out of scope — layers are not runtime).

```bash
#!/usr/bin/env bash
# Deterministic: fail if any runtime LLM endpoint is not loopback, or if any
# external-provider API-key env name appears in runtime code. No network.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

rc=0
runtime_files() {
  { git ls-files 'controller/**/*.go' 'docker/rootfs/**' 2>/dev/null || true; } \
    | grep -v '_test\.go$' || true
}

# 1. External provider hosts / key names anywhere in runtime code.
BAD_HOSTS='openai\.com|openrouter\.ai|anthropic\.com|generativelanguage|googleapis\.com|api\.mistral|api\.groq|api\.together'
BAD_KEYS='OPENAI_API_KEY|ANTHROPIC_API_KEY|OPENROUTER_API_KEY|GEMINI_API_KEY|GROQ_API_KEY|\bHF_TOKEN\b'
while IFS= read -r f; do
  [ -n "$f" ] || continue
  if grep -nEI "$BAD_HOSTS" "$f"; then echo "locality: external LLM host in $f" >&2; rc=1; fi
  if grep -nEI "$BAD_KEYS" "$f"; then echo "locality: provider API key env in $f" >&2; rc=1; fi
done < <(runtime_files)

# 2. LLM-surface lines must reference only loopback hosts.
while IFS= read -r f; do
  [ -n "$f" ] || continue
  # lines mentioning an LLM surface AND an http(s) URL
  grep -nEI '(/v1/chat/completions|/v1/completions|/completion|/slots|/props|llama)' "$f" 2>/dev/null \
    | grep -EI 'https?://' \
    | grep -vEI 'https?://(127\.0\.0\.1|localhost)(:| |/|")' \
    && { echo "locality: non-loopback LLM URL in $f" >&2; rc=1; } || true
done < <(runtime_files)

[ "$rc" -eq 0 ] && echo "locality: OK"
exit "$rc"
```

### 2b. SPA has no external origins (tightens spec 003 §11 item 13 into the gate)

`grep -REI 'https?://' controller/web/static/js controller/web/static/css` must match nothing outside comments — the SPA is same-origin only (all fonts/icons are self-hosted, spec 005 §10). Non-comment match → FAIL.

### 2c. Persistence-map consistency

Assert that every persistent path named in §1a has a matching `mkdir -p` entry in `docker/rootfs/etc/cont-init.d/10-data-dirs.sh`: for each of `valkey chromium xdg/config xdg/cache xdg/data metrics agent`, `grep -q` the token in that script (the `metrics`/`agent` tokens exist once specs 005/008 land; before then, scope the required set to what those specs have introduced — the gate reads the set from a literal list at the top of the check so it advances spec-by-spec). Missing entry → FAIL with the path.

`check.sh` wiring:

```bash
step "llm locality + spa origins + persistence map"
bash scripts/check-llm-local.sh || fail "llm locality"
# 2b and 2c inline or as scripts/check-persistence.sh
```

## 3. README correction (constitution rule 9 / rule 4)

The current README lists four "modes" (Free / Low-Cost / Balanced / Best), three of which route to OpenRouter or native frontier providers. No such code exists and the locality gate forbids adding it without a future spec. Replace the modes list (`README.md` lines describing Free/Low-Cost/Balanced/Best) with an accurate statement:

> Virtual Me runs a **fully local** model (llama.cpp + Gemma 4 E2B) by default: no data leaves your machine and there are no AI bills. Optional commercial-provider backends are a possible future direction, not part of v1.

Keep the Overview paragraph's "runs completely locally" claim (now unqualified-accurate).

## 4. Smoke/e2e assertions

- **Smoke (`test/smoke.sh`)** — after all-green: assert `$DATA_DIR/valkey` contains an AOF (`appendonly*` file or `appendonlydir/`); assert no unexpected top-level entries under `$DATA_DIR` beyond the known set (`valkey chromium xdg metrics agent`) — a stray directory means something writes outside its lane.
- **e2e (`test/e2e.sh`)** — the existing restart-cycle already proves chat history survives; add an explicit assertion that `$DATA_DIR/valkey` is non-empty after the first run (AOF written) and that chat history is present after restart (already covered by `chat-probe --history-only`). Reference spec 005 §11 for the `metrics/` persistence assertion (kept in 005 to avoid duplication).

## 5. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes:

- README: modes list replaced (§3); data-directory section already lists the persistent layout — extend with `metrics/` and `agent/` and a one-line "everything else is ephemeral or baked into the image".
- `develop` skill: mention the locality/persistence gate as part of `npm run check`; new stateful components must be added to spec 007 §1.
- `operate` skill: note that all inference is local (no external calls); troubleshooting for "where is my data" points at the §1a table.

## 6. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | `npm run check` | `check: OK`; prints `locality: OK` |
| 2 | `bash scripts/check-llm-local.sh` | `locality: OK`, exit 0 on the current tree |
| 3 | Inject a temporary `http://api.openai.com/v1/chat/completions` into a controller `.go` file, re-run | gate FAILS naming the file:line; revert |
| 4 | Inject `OPENROUTER_API_KEY` into a rootfs script, re-run | gate FAILS; revert |
| 5 | `grep -REI 'https?://' controller/web/static/js controller/web/static/css` | only comments (or nothing) |
| 6 | `bash test/smoke.sh` | `smoke: OK` including AOF + no-stray-dir assertions |
| 7 | `bash test/e2e.sh` | `e2e: OK` |
| 8 | README no longer advertises OpenRouter/native-provider modes | manual read; §3 text present |
| 9 | Every persistent path in §1a is `mkdir`'d in `10-data-dirs.sh` | §2c check passes |
| 10 | `/master-update` run | §5 changes present |

Commit as `spec 007: persistence grounding + deterministic LLM-locality gate`.

## Amendments

### 2026-07-23 — Comment-aware SPA scan and reserved agent lane

The single `scripts/check-llm-local.sh` helper implements all three §2 checks.
Its SPA-origin scan ignores JavaScript/CSS comments while still rejecting URLs
in strings and executable/style content. Runtime LLM analysis and SPA analysis
use `git ls-files`, so the deterministic gate examines the tracked tree (with
staged files included) and never generated output.

Spec 008 has not executed yet, but `agent/` is created and enforced now because
it is already part of this spec's canonical map and known top-level set. This
reserves the persistence lane without adding agent runtime behavior.

### 2026-07-23 — Persistent job queue state

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| `virtualme:jobs:*` job queue state | `$VM_DATA_DIR/valkey/` (AOF) | `internal/jobs` via `svc-valkey` | 013 |

### 2026-07-23 — Persistent projects and scratch space

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| `virtualme:projects*` project records and run summaries | `$VM_DATA_DIR/valkey/` (AOF) | `internal/projects` via `svc-valkey` | 014 |
| Project scratch space | `$VM_DATA_DIR/projects/<id>/` | `internal/projects`, created on first run and retained after project deletion | 014 |

### 2026-07-23 — Persistent activity ledger

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| `virtualme:activity` bounded machine-activity ledger | `$VM_DATA_DIR/valkey/` (AOF) | `internal/jobs.Activity` via `svc-valkey` | 015 |

### 2026-07-23 — Persistent jiggler setting

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| `virtualme:jiggler:enabled` jiggler switch | `$VM_DATA_DIR/valkey/` (AOF) | `internal/jiggler` via `svc-valkey` | 017 |

### 2026-07-23 — Speech history and synthesized-audio cache

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| `virtualme:speech:log` bounded speech history | `$VM_DATA_DIR/valkey/` (AOF) | `internal/tts.Log` via `svc-valkey` | 020 |
| Synthesized-audio cache | `$VM_DATA_DIR/tts-cache/` | `ttsd`, exact sentence/voice/speed WAV files; safe to delete anytime | 020 |

### 2026-07-24 — Mail flush diagnostics

| State | Path | Owner / mechanism | Introduced |
|---|---|---|---|
| Bounded dma delivery diagnostics | `$VM_DATA_DIR/mail/flush.log` | `svc-mailq`, newest 500 lines | 023 |
| Last queue-flush marker | `$VM_DATA_DIR/mail/last-flush` | `svc-mailq`, epoch seconds rewritten after each cycle | 023 |
