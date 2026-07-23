---
name: master-update
description: Meta-review gate. Audits and updates all other AI skills, README link tables, AGENTS.md, and CLAUDE.md against the actual repo state, then runs the quality gates. Run after any structural change or completed spec.
---

# /master-update — docs and skills meta-updater

Docs follow code, never the reverse. Procedure:

## 1. Enumerate ground truth (from the tree, not from memory)

- `package.json`: name, version, scripts, bin, engines, devDependencies
- `src/commands/*.js`: the real CLI surface
- `.github/workflows/*.yml`: job names, triggers, required secrets
- `docker/layers/*.sh`: layer numbers, artifacts, pinned versions/hashes
- `docker/rootfs/etc/s6-overlay/s6-rc.d/*`: service list and dependencies
- `controller/`: endpoints (grep `mux.Handle`/`HandleFunc`), ws message types
- `specs/*.md`: spec index
- `.cursor/skills/*/SKILL.md`: skill inventory

## 2. Diff against documentation

Check each of: `README.md` (User's Guide tables: artifacts, CI, skills,
specs, ports, commands), `AGENTS.md`, `CLAUDE.md`, every `SKILL.md`.
Flag: missing entries, stale versions/ports/flags, removed features still
documented, broken relative links, drift between skill tables and
`src/commands/`.

## 3. Apply updates

Edit the docs to match ground truth. Keep prose terse. Do not invent
features. Do not change code to match docs — if code looks wrong, stop and
report instead.

## 4. Validate

Run `npm run check`. If Docker is available and `test/smoke.sh` exists and
container-affecting docs changed, also run `bash test/smoke.sh`.

## 5. Report

Summarize: files changed, drift found, anything suspicious left alone.
