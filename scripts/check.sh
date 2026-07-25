#!/usr/bin/env bash
# Canonical deterministic quality gate. Run by .githooks/pre-commit and CI.
# Env: CHECK_SKIP_GO=1 skips Go gates (used by the npm publish job).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

fail() { echo "check: FAIL: $*" >&2; exit 1; }
step() { echo "check: $*"; }

step "shell syntax (bash -n)"
shopt -s nullglob
for f in cli.sh scripts/*.sh .githooks/* test/*.sh docker/layers/*.sh \
         docker/rootfs/etc/cont-init.d/* \
         docker/rootfs/etc/s6-overlay/s6-rc.d/*/{run,finish} \
         docker/rootfs/usr/local/lib/virtualme/*.sh controller/tools/*.sh; do
  bash -n "$f" || fail "bash -n $f"
done
shopt -u nullglob

step "llm locality + spa origins + persistence map"
bash scripts/check-llm-local.sh || fail "llm locality"

step "eslint"
node_modules/.bin/eslint . || fail eslint

step "typecheck (tsc --checkJs)"
node_modules/.bin/tsc -p jsconfig.json || fail typecheck

step "unit tests (node --test)"
node --test test/*.test.js || fail "node tests"

step "generated themes"
node scripts/generate-themes.mjs --check || fail "generated themes"

step "documentation site"
npm --prefix docs run check || fail "documentation site"

step "CLI dry run"
node bin/virtualme.js help >/dev/null || fail "cli help"
node bin/virtualme.js version >/dev/null || fail "cli version"

if [[ -d controller && "${CHECK_SKIP_GO:-0}" != "1" ]]; then
  step "go gates"
  if [[ -f scripts/build-web.sh ]]; then
    step "web build (esbuild minify + sourcemaps)"
    bash scripts/build-web.sh || fail "build-web"
  fi
  (cd controller && [[ -z "$(gofmt -l .)" ]]) || fail "gofmt -l"
  (cd controller && go vet ./...) || fail "go vet"
  (cd controller && go test ./...) || fail "go test"
fi

echo "check: OK"
