# Spec 006: Virtual Desktop and Chromium Reliability

| | |
|---|---|
| Status | Executed (2026-07-23) |
| Depends on | `specs/002-container.md` executed (desktop stack + Chromium service live) |
| Produces | Reliable Chromium supervision (deterministic restart, single tab, no restore/crash prompts), best-effort real sandbox with graceful fallback, profile-persistence fix; a Chromium watchdog s6 service; CLI + smoke-test updates |
| Followed by | Independent of 004/005/007; `specs/008-browser-agent.md` builds on the stable Chromium |

## 0. Executor instructions

- The constitution (`specs/001-constitution.md` §1) binds this spec. Chromium and its supervision live in `docker/rootfs/` (fast-moving top of the image) and one **new** append-only layer for the watchdog helper if needed — no edits to existing numbered layers (constitution rule 6). apt-installed packages already present (`xdotool`, `x11-utils`) suffice; no new package.
- This spec **supersedes** (constitution rule 4 — superseding text here; prior specs untouched): the `svc-chromium/run` launch line in spec 002 §5, and the trust-model sandbox posture note in spec 002 §0/§5 (`--no-sandbox` "required" → "best-effort real sandbox with fallback", §4 below). The health contract (visible Chromium window via `xdotool`, spec 002 §6) is unchanged and is now also enforced by the watchdog.
- Stop-on-red per section; finish with the Acceptance Checklist (§8).

## 1. Problem statement (observed)

1. **Blank screen / no restart on user close.** When the user closes the Chromium window through the virtual desktop, sometimes a new window appears, sometimes the display stays blank. Root cause: closing the last window does not reliably terminate the `chromium` process (background/keep-alive behavior), so the s6 longrun never exits and never restarts, leaving an empty root window. Requirement: Chromium is **always** brought back to a single visible window.
2. **`--no-sandbox` warning bar.** Chromium shows a "You are using an unsupported command-line flag: --no-sandbox" infobar.
3. **Profile changes don't persist.** Creating a Chrome profile / choosing a profile color through the remote desktop does not survive a restart. Root cause: Chromium is killed without a clean shutdown, so `Preferences`/`Local State` are never flushed; the per-boot singleton-lock cleanup (spec 002 §5) does not flush profile writes.
4. **Restore-pages prompt.** After an unclean exit Chromium offers to restore pages / shows a crash bubble, which is noise for a single-purpose kiosk-like browser.

## 2. Canonical launch: consistent flags, single tab

Replace `svc-chromium/run` with a launcher that (a) rewrites the profile's exit state to clean **before** every launch, (b) purges session/tab-restore state while preserving settings/cookies/history, and (c) launches with one fixed, documented flag set opening exactly one `about:blank` tab.

**`docker/rootfs/etc/s6-overlay/s6-rc.d/svc-chromium/run`** (mode 755) — superseding contents:

```bash
#!/command/with-contenv bash
export DISPLAY="$VM_DISPLAY"
PROFILE="$VM_DATA_DIR/chromium"

# Wait for X (same pattern as openbox/x11vnc).
until xdpyinfo >/dev/null 2>&1; do sleep 0.2; done

# 1. Mark the last session as cleanly exited so Chromium never offers to
#    restore pages or shows the crash bubble. jq is not available (zero extra
#    deps); use a minimal sed on the known JSON keys, tolerant of absence.
PREFS="$PROFILE/Default/Preferences"
if [ -f "$PREFS" ]; then
  sed -i 's/"exit_type":"[^"]*"/"exit_type":"Normal"/g; s/"exited_cleanly":false/"exited_cleanly":true/g' "$PREFS" || true
fi

# 2. Drop session/tab-restore state (keeps cookies, history, settings).
rm -rf "$PROFILE/Default/Sessions" \
       "$PROFILE/Default/Session Storage" \
       "$PROFILE/Default/Current Tabs" \
       "$PROFILE/Default/Current Session" \
       "$PROFILE/Default/Last Tabs" \
       "$PROFILE/Default/Last Session" 2>/dev/null || true

# 3. Canonical flag set. Exactly one about:blank tab. Sandbox mode is chosen
#    by the helper below (real sandbox when the kernel allows, else disabled
#    with the infobar suppressed).
# shellcheck source=/dev/null
source /usr/local/lib/virtualme/chromium-sandbox.sh   # sets SANDBOX_FLAGS array

exec chromium \
  "${SANDBOX_FLAGS[@]}" \
  --user-data-dir="$PROFILE" \
  --class=chromium \
  --no-first-run \
  --no-default-browser-check \
  --disable-session-crashed-bubble \
  --hide-crash-restore-bubble \
  --disable-features=InfiniteSessionRestore,Translate \
  --disable-background-mode \
  --disable-backgrounding-occluded-windows \
  --disable-renderer-backgrounding \
  --start-maximized \
  --window-position=0,0 \
  about:blank
```

Notes:

- `sed` on `Preferences` is deliberately minimal (no JSON parser, zero deps); it is a no-op when the file or keys are absent (first boot) and is safe because Chromium is not running at that instant.
- `--disable-background-mode` + the backgrounding flags make the process exit when its last window closes, which is what lets s6 restart it (see §3). Keeping `--class=chromium` fixed guarantees the health probe and watchdog `xdotool ... --class chromium` keep matching.
- Session/tab-restore files are removed each launch → single tab, no restored windows. Cookies/history/`Preferences`/`Local State` are untouched → settings persist (paired with the clean shutdown in §5).

## 3. Deterministic restart: window watchdog

Even with §2's flags, a wedged renderer or a compositor hiccup can leave a live process with no visible window. Add an independent watchdog that restarts `svc-chromium` whenever no visible Chromium window exists.

New service **`docker/rootfs/etc/s6-overlay/s6-rc.d/svc-chromium-watchdog/`**:

- `type` → `longrun`; `dependencies.d/base` and `dependencies.d/svc-chromium` (empty files); registered by adding an empty file `svc-chromium-watchdog` under `etc/s6-overlay/user-bundles.d/user/contents.d/` (spec 002 §5's user bundle).
- `run` (mode 755):

```bash
#!/command/with-contenv bash
export DISPLAY="$VM_DISPLAY"
until xdpyinfo >/dev/null 2>&1; do sleep 0.2; done

MISS=0
GRACE="${VM_CHROMIUM_WATCHDOG_GRACE:-3}"   # consecutive misses (× 2s) before restart
while sleep 2; do
  if xdotool search --onlyvisible --class chromium >/dev/null 2>&1; then
    MISS=0
    continue
  fi
  MISS=$((MISS + 1))
  if [ "$MISS" -ge "$GRACE" ]; then
    echo "chromium-watchdog: no visible window for $((GRACE * 2))s; restarting svc-chromium" >&2
    s6-svc -r /run/service/svc-chromium || true
    MISS=0
    sleep 5   # let it come up before re-checking
  fi
done
```

- `s6-svc -r` restarts the service cleanly (sends the down/up cycle → §5's finish script runs → profile flushes). The grace window avoids restarting during the brief windowless moment right after the user closes a window and before the process re-launches on its own.
- Because `svc-chromium` exits on last-window-close (§2), the common path is: user closes window → process exits → s6 restarts `svc-chromium` immediately (before the watchdog's grace elapses). The watchdog is the backstop for the "process alive but blank" case.

## 4. Sandbox: best-effort real, graceful fallback

Chromium's setuid sandbox is unavailable to an unprivileged in-container user, but the **namespace sandbox** works when the kernel permits unprivileged user namespaces. Detect and use it; otherwise fall back to `--no-sandbox` with the infobar suppressed.

New helper **`docker/rootfs/usr/local/lib/virtualme/chromium-sandbox.sh`** (sourced by §2; sets a `SANDBOX_FLAGS` bash array):

```bash
#!/command/with-contenv bash
# Decide Chromium sandbox flags. Real namespace sandbox when unprivileged user
# namespaces are available; otherwise disable the sandbox and suppress the
# resulting infobar. Never fails the launch.
SANDBOX_FLAGS=()
userns_ok=0
if [ -r /proc/sys/kernel/unprivileged_userns_clone ]; then
  [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" = "1" ] && userns_ok=1
else
  # Sysctl absent (non-Debian kernels default to enabled); probe with unshare.
  unshare --user --map-root-user true >/dev/null 2>&1 && userns_ok=1
fi

if [ "$userns_ok" = "1" ]; then
  # Real sandbox: no --no-sandbox, no infobar. Namespace sandbox needs no setuid helper.
  SANDBOX_FLAGS+=(--disable-gpu)
else
  # No usable sandbox in this container/kernel: disable it and hide the warning.
  # --test-type removes the "unsupported command-line flag" infobar.
  SANDBOX_FLAGS+=(--no-sandbox --test-type --disable-gpu)
fi
```

**CLI cooperation** (`src/commands/start.js`): to make the real sandbox reachable, the container must be allowed unprivileged user namespaces and the Chromium seccomp profile. Add these to the `docker run` argument list, gated by an opt-out flag `--no-browser-sandbox` (default: attempt the sandbox):

- `--security-opt seccomp=unconfined` is **not** used (too broad); instead pass `--security-opt seccomp=/path` only if a profile is bundled — for v1 the simplest portable enablement is `--cap-add SYS_ADMIN` **only when** `--browser-sandbox` is explicitly requested. Default behavior adds nothing and relies on the host kernel's `unprivileged_userns_clone` (enabled by default on modern Debian/Ubuntu) so the namespace sandbox works with **no extra privileges**; the helper's `unshare` probe confirms at runtime.

Concretely, `start.js`:

- gains `--no-browser-sandbox` (boolean) parsed via `parseArgs`; when set, export `VM_CHROMIUM_NO_SANDBOX=1` into the container (`-e VM_CHROMIUM_NO_SANDBOX=1`) which the helper honors by forcing the fallback branch.
- adds no new default privileges (keeps the spec 002 §1 invocation intact) — the namespace sandbox is used opportunistically when the kernel already allows it, and transparently falls back otherwise. This keeps the trusted-network prototype posture (constitution rule 8) while removing the infobar and enabling real sandboxing on hosts that support it.

Superseding trust-model text (replaces spec 002's "`--no-sandbox` is required"): *Chromium runs with its namespace sandbox when the host kernel allows unprivileged user namespaces (the default on current Debian/Ubuntu); on kernels that do not, it falls back to `--no-sandbox` with the warning infobar suppressed. The container adds no new capabilities by default. This is unchanged from the prototype trust model: the container is the security boundary on a trusted private network.*

## 5. Profile persistence: clean shutdown

Add an s6 `finish` script so the profile flushes on every stop/restart (service crash, watchdog restart, container stop):

**`docker/rootfs/etc/s6-overlay/s6-rc.d/svc-chromium/finish`** (mode 755):

```bash
#!/command/with-contenv bash
# Give Chromium time to flush Preferences/Local State on shutdown.
export DISPLAY="$VM_DISPLAY"
pkill -TERM -f "chromium .*--user-data-dir=$VM_DATA_DIR/chromium" 2>/dev/null || true
for _ in $(seq 1 20); do
  pgrep -f "chromium .*--user-data-dir=$VM_DATA_DIR/chromium" >/dev/null 2>&1 || break
  sleep 0.25
done
pkill -KILL -f "chromium .*--user-data-dir=$VM_DATA_DIR/chromium" 2>/dev/null || true
```

- SIGTERM lets Chromium write `Preferences`/`Local State` (profile color, created profiles, settings) before exit; the loop waits up to 5 s, then SIGKILLs any stragglers.
- `procps` (`pkill`/`pgrep`) is already installed (layer 001). Combined with §2's clean-exit-state rewrite and the singleton-lock cleanup (spec 002 §5, unchanged), a profile change made through the remote desktop now survives a restart.
- The window close button is **kept** (no Openbox decoration changes): reliability comes from clean restart + flush, not from preventing close.

## 6. Smoke-test additions (`test/smoke.sh`)

Append (existing steps unchanged):

- **Restart brings the window back**: after the initial all-green wait, `docker exec` kill the Chromium window (`xdotool search --onlyvisible --class chromium windowkill`), then poll up to 30 s for `/healthz` to be green again and a visible Chromium window to reappear.
- **Single tab**: assert exactly one visible Chromium top-level window (`xdotool search --onlyvisible --class chromium | wc -l` → `1`).
- **Profile persistence**: write a sentinel into the profile through the running container (`docker exec` appends a known key to `Default/Preferences` via the same `sed`-safe approach, or touches `Default/vm-sentinel`), `./cli.sh`-style stop+start on the same data dir (reuse the smoke restart), then assert the sentinel is still present under `$DATA_DIR/chromium/Default/` — proving the finish-script flush + data-mount persistence.
- **Sandbox mode reported**: `docker exec` reads `/proc/$(pgrep -n chromium)/cmdline` and asserts it contains **either** no `--no-sandbox` (real sandbox) **or** `--no-sandbox --test-type` together (fallback with infobar suppressed) — never bare `--no-sandbox` without `--test-type`.

## 7. Docs refresh (constitution rule 9)

Run the `/master-update` skill procedure. Expected changes:

- README/`operate` skill troubleshooting: closing the browser window auto-restarts it to a single blank tab; `--no-browser-sandbox` flag documented on `start`; note that profile settings persist under `~/.virtualme/chromium`.
- `develop` skill: s6 service list gains `svc-chromium-watchdog`; note the `chromium-sandbox.sh` helper and the `finish` script.
- `AGENTS.md`: no command-table change.

## 8. Acceptance checklist (run every item)

| # | Command / action | Expected |
|---|---|---|
| 1 | `npm run check` | `check: OK` (shell-syntax gate now covers the new run/finish/watchdog/helper scripts and `start.js` changes typecheck) |
| 2 | `docker build -f docker/Dockerfile -t virtualme:dev .` | succeeds |
| 3 | `bash test/smoke.sh` | `smoke: OK` including the §6 additions |
| 4 | Manual: open `/desktop-view`, close the Chromium window | a single blank Chromium window reappears within a few seconds; screen never stays blank |
| 5 | Manual: in the container, `kill -STOP` the renderer to force a windowless-but-alive state | watchdog restarts `svc-chromium` within ~6–8 s (grace × 2 s + startup) |
| 6 | Manual: change the profile color/create a profile via the desktop, `./cli.sh stop && ./cli.sh start` on the same data dir | the change persists |
| 7 | Running container: `docker exec virtualme sh -c 'cat /proc/$(pgrep -n chromium)/cmdline \| tr "\0" " "'` | either sandboxed (no `--no-sandbox`) or `--no-sandbox --test-type`; no unsuppressed infobar |
| 8 | `docker exec -e DISPLAY=:99 virtualme xdotool search --onlyvisible --class chromium \| wc -l` | `1` |
| 9 | `./cli.sh start --no-browser-sandbox` then check 7 | forced fallback: `--no-sandbox --test-type` present |
| 10 | `/master-update` run | §7 changes present |

Commit as `spec 006: reliable Chromium supervision, sandbox fallback, profile persistence`.

## Amendments

### 2026-07-23 — Runtime user-namespace probe and isolated smoke coverage

The sandbox helper always runs the `unshare --user --map-root-user` probe after
the sysctl permits user namespaces, instead of trusting the sysctl value alone.
Container seccomp or LSM policy can still reject namespace creation when the
host sysctl is `1`; the executable probe is the reliable graceful-fallback
boundary required by §4.

The smoke test uses a per-process container name so it cannot replace an
unrelated user container. It also automates acceptance items 5 and 9: unmapping
the live browser window exercises the watchdog's process-alive/window-absent
path, and an isolated replacement container verifies the forced
`--no-sandbox --test-type` launch.

The prior s6/read-only-root concern required no code change. The tracked tree
contains the user bundle only under `user-bundles.d/` (not the root-owned
`s6-rc.d/user` path that s6 rewrites), `/run` remains an executable uid-owned
tmpfs, `S6_READ_ONLY_ROOT=1` stages s6 runtime state there, and the container
root filesystem remains mounted read-write. No numbered Docker layer was
edited.

### 2026-07-23 — Superseded by spec 016

Spec 016 supersedes §2's Chromium launch arguments with the documented
deterministic automation flag array. It also supersedes §3's first-visible
window selection with most-recent mapped Chromium-window selection so
full-screen popups become the active surface. Hidden Chromium-class X clients
are filtered out, preserving this spec's live-process/no-visible-window
restart guarantee. No numbered Docker layer changed.
