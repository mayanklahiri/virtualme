#!/usr/bin/env bash
# Full E2E: CLI-driven lifecycle; health, minified SPA + sourcemaps, ws, desktop
# proxy, visible browser, streaming chat, and a restart cycle on the same data
# dir (catches image-tag drift and stale-profile-lock regressions).
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

NAME="virtualme"
PORT=8080
TIMEOUT="${E2E_TIMEOUT:-300}"
BASE="http://127.0.0.1:${PORT}"
DATA_DIR="$(mktemp -d)"
export VIRTUALME_DATA="$DATA_DIR"

fail() {
  echo "e2e: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  ./cli.sh stop >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
  exit 1
}

wait_healthy() {
  local deadline=$(( $(date +%s) + TIMEOUT ))
  until curl -fsS "$BASE/healthz" 2>/dev/null | grep -q '"ok":true'; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "healthz not green within ${TIMEOUT}s: $(curl -s "$BASE/healthz" || echo unreachable)"
    fi
    sleep 5
  done
}

echo "e2e: [1/9] CLI build (tags :dev and the start tag)"
./cli.sh build >/dev/null || fail "cli build"

./cli.sh stop >/dev/null 2>&1 || true
echo "e2e: [2/9] CLI start on fresh data dir ${DATA_DIR}"
./cli.sh start >/dev/null || fail "cli start"

echo "e2e: [3/9] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_healthy

echo "e2e: [4/9] orchestrator serves the minified SPA and sourcemaps"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"
curl -fsS "$BASE/js/app.js" | grep -q "sourceMappingURL" || fail "app.js missing sourcemap pointer"
curl -fsS -o /dev/null "$BASE/js/app.js.map" || fail "app.js.map not served"
curl -fsS -o /dev/null "$BASE/css/app.css.map" || fail "app.css.map not served"

echo "e2e: [5/9] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [6/9] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [7/9] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "e2e: [8/9] chat round-trip streams at least one delta"
node test/chat-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "chat probe"

echo "e2e: [9/9] restart cycle on the same data dir stays healthy, chat history survives"
./cli.sh stop >/dev/null || fail "cli stop"
./cli.sh start >/dev/null || fail "cli start (restart)"
wait_healthy
node test/chat-probe.mjs --history-only "ws://127.0.0.1:${PORT}/ws" \
  || fail "chat history lost across restart"

./cli.sh stop >/dev/null
rm -rf "$DATA_DIR"
echo "e2e: OK"
