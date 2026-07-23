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

echo "e2e: [1/13] CLI build (tags :dev and the start tag)"
./cli.sh build >/dev/null || fail "cli build"

./cli.sh stop >/dev/null 2>&1 || true
echo "e2e: [2/13] CLI start on fresh data dir ${DATA_DIR}"
./cli.sh start >/dev/null || fail "cli start"

echo "e2e: [3/13] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_healthy

echo "e2e: [4/13] orchestrator serves the minified SPA and sourcemaps"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"
curl -fsS "$BASE/js/app.js" | grep -q "sourceMappingURL" || fail "app.js missing sourcemap pointer"
curl -fsS -o /dev/null "$BASE/js/app.js.map" || fail "app.js.map not served"
curl -fsS -o /dev/null "$BASE/css/app.css.map" || fail "app.css.map not served"

echo "e2e: [5/13] SPA history routes fall back while missing assets stay 404"
for route in status desktop-view; do
  code=$(curl -s -o /tmp/e2e-route.html -w '%{http_code}' "$BASE/$route")
  [ "$code" = 200 ] && grep -q "Virtual Me" /tmp/e2e-route.html \
    || fail "SPA fallback failed for /$route"
done
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/js/nope.js")
[ "$code" = 404 ] || fail "missing asset returned $code (expected 404)"

echo "e2e: [6/13] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [7/13] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [8/13] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "e2e: [9/13] metrics protocol returns raw and 15-minute tiers"
node test/metrics-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "metrics probe"

echo "e2e: [10/13] chat round-trip streams at least one delta"
node test/chat-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "chat probe"

echo "e2e: [11/13] chat generation can be stopped after its first delta"
node test/chat-probe.mjs --stop "ws://127.0.0.1:${PORT}/ws" || fail "chat stop probe"

echo "e2e: [12/13] optional browser-agent task produces steps and artifacts"
if [ "${E2E_AGENT:-0}" = "1" ]; then
  probe_output=$(AGENT_E2E_TIMEOUT="${AGENT_E2E_TIMEOUT:-600}" \
    node test/agent-probe.mjs "ws://127.0.0.1:${PORT}/ws") || fail "agent probe"
  printf '%s\n' "$probe_output"
  task_id="${probe_output##*taskId=}"
  [ -n "$task_id" ] || fail "agent probe did not report task id"
  [ -s "$DATA_DIR/agent/$task_id/steps.jsonl" ] || fail "agent steps.jsonl missing"
  compgen -G "$DATA_DIR/agent/$task_id/step-*.jpg" >/dev/null \
    || fail "agent screenshot artifact missing"
else
  echo "e2e: agent task skipped (set E2E_AGENT=1 to enable)"
fi

echo "e2e: [13/13] restart preserves chat and metrics history"
compgen -G "$DATA_DIR/valkey/*" >/dev/null \
  || fail "valkey persistence is empty before restart"
./cli.sh stop >/dev/null || fail "cli stop"
./cli.sh start >/dev/null || fail "cli start (restart)"
wait_healthy
[ -f "$DATA_DIR/metrics/tier0.json" ] || fail "metrics tier0.json missing after restart"
node test/metrics-probe.mjs --non-empty "ws://127.0.0.1:${PORT}/ws" \
  || fail "metrics history lost across restart"
node test/chat-probe.mjs --history-only "ws://127.0.0.1:${PORT}/ws" \
  || fail "chat history lost across restart"

./cli.sh stop >/dev/null
rm -rf "$DATA_DIR"
echo "e2e: OK"
