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
MAIL_CAPTURE="$DATA_DIR/mail-capture.eml"
MAIL_SINK_PID=""

cleanup() {
  if [ -n "$MAIL_SINK_PID" ]; then kill "$MAIL_SINK_PID" >/dev/null 2>&1 || true; fi
  ./cli.sh stop >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
}
trap cleanup EXIT

fail() {
  echo "e2e: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
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

echo "e2e: [1/17] CLI build (tags :dev and the start tag)"
./cli.sh build >/dev/null || fail "cli build"

./cli.sh stop >/dev/null 2>&1 || true
echo "e2e: [2/17] starting SMTP sink and CLI on fresh data dir ${DATA_DIR}"
MAIL_SINK_HOST=0.0.0.0 node test/mail-sink.mjs "$MAIL_CAPTURE" >/tmp/virtualme-mail-sink.log 2>&1 &
MAIL_SINK_PID=$!
sleep 1
kill -0 "$MAIL_SINK_PID" 2>/dev/null || fail "mail sink did not start"
export VM_MAIL_SMARTHOST=vmhost
export VM_MAIL_SMARTHOST_PORT=2525
export VM_MAIL_DKIM_DOMAIN=example.test
./cli.sh start >/dev/null || fail "cli start"

echo "e2e: [3/17] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_healthy

echo "e2e: [4/17] orchestrator serves branded SPA assets and sourcemaps"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"
grep -q '"arctic", "solar", "studio"' /tmp/e2e-index.html || fail "SPA boot script does not list all eight themes"
content_type=$(curl -fsS -o /dev/null -w '%{content_type}' "$BASE/img/hero-earthrise.jpg")
[ "$content_type" = "image/jpeg" ] || fail "hero image content type is $content_type"
curl -fsS -o /dev/null "$BASE/favicon.svg" || fail "favicon.svg not served"
curl -fsS "$BASE/icons.svg" | grep -q 'id="i-virtualme-mark"' || fail "brand mark missing from icon sprite"
curl -fsS "$BASE/js/app.js" | grep -q "sourceMappingURL" || fail "app.js missing sourcemap pointer"
curl -fsS -o /dev/null "$BASE/js/app.js.map" || fail "app.js.map not served"
curl -fsS -o /dev/null "$BASE/css/app.css.map" || fail "app.css.map not served"

echo "e2e: [5/17] SPA history routes fall back while missing assets stay 404"
for route in status speech mail desktop-view; do
  code=$(curl -s -o /tmp/e2e-route.html -w '%{http_code}' "$BASE/$route")
  [ "$code" = 200 ] && grep -q "Virtual Me" /tmp/e2e-route.html \
    || fail "SPA fallback failed for /$route"
done
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/js/nope.js")
[ "$code" = 404 ] || fail "missing asset returned $code (expected 404)"

echo "e2e: [6/17] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [7/17] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [8/17] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "e2e: [9/17] state frames include hostname and disk capacity"
node test/state-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "state probe"

echo "e2e: [10/17] metrics protocol returns raw and 15-minute tiers"
node test/metrics-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "metrics probe"

echo "e2e: [11/17] local TTS streams speech and serves OpenAI-compatible WAV"
node test/tts-probe.mjs "ws://127.0.0.1:${PORT}/ws" "$BASE" || fail "tts probe"

echo "e2e: [12/17] outbound mail reaches sink with MIME, CID, and DKIM"
# The stdlib fixture is intentionally plaintext; production remains strict
# STARTTLS. Permit fallback only in this disposable test configuration.
printf 'OPPORTUNISTIC_TLS\n' >> "$DATA_DIR/mail/dma.conf"
node test/mail-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "mail relay probe"
deadline=$(( $(date +%s) + 30 ))
while [ ! -s "$MAIL_CAPTURE" ] && [ "$(date +%s)" -lt "$deadline" ]; do sleep 1; done
[ -s "$MAIL_CAPTURE" ] || fail "SMTP sink captured no message"
grep -qi 'multipart/related' "$MAIL_CAPTURE" || fail "captured mail lacks multipart/related"
grep -qi '^Content-ID:' "$MAIL_CAPTURE" || fail "captured mail lacks Content-ID"
grep -qi 'cid:img1@virtualme' "$MAIL_CAPTURE" || fail "captured mail lacks cid reference"
grep -qi '^DKIM-Signature:' "$MAIL_CAPTURE" || fail "captured mail lacks DKIM signature"

echo "e2e: [13/17] chat round-trip streams at least one delta"
node test/chat-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "chat probe"

echo "e2e: [14/17] chat generation can be stopped after its first delta"
node test/chat-probe.mjs --stop "ws://127.0.0.1:${PORT}/ws" || fail "chat stop probe"

echo "e2e: [15/17] optional browser-agent tasks produce browser and speech steps"
if [ "${E2E_AGENT:-0}" = "1" ]; then
  probe_output=$(AGENT_E2E_TIMEOUT="${AGENT_E2E_TIMEOUT:-600}" \
    node test/agent-probe.mjs "ws://127.0.0.1:${PORT}/ws") || fail "agent probe"
  printf '%s\n' "$probe_output"
  task_id="${probe_output##*taskId=}"
  [ -n "$task_id" ] || fail "agent probe did not report task id"
  [ -s "$DATA_DIR/agent/$task_id/steps.jsonl" ] || fail "agent steps.jsonl missing"
  compgen -G "$DATA_DIR/agent/$task_id/step-*.jpg" >/dev/null \
    || fail "agent screenshot artifact missing"
  AGENT_E2E_TIMEOUT="${AGENT_E2E_TIMEOUT:-600}" \
    node test/agent-probe.mjs --speak "ws://127.0.0.1:${PORT}/ws" || fail "agent speak probe"
else
  echo "e2e: agent task skipped (set E2E_AGENT=1 to enable)"
fi

echo "e2e: [16/17] direct mode accepts and queues deferred mail"
./cli.sh stop >/dev/null || fail "cli stop before direct mode"
unset VM_MAIL_SMARTHOST VM_MAIL_SMARTHOST_PORT
./cli.sh start >/dev/null || fail "cli start (direct mode)"
wait_healthy
node test/mail-probe.mjs --direct "ws://127.0.0.1:${PORT}/ws" || fail "mail direct probe"

echo "e2e: [17/17] restart preserves chat, metrics, mail spool, and DKIM key"
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
[ -s "$DATA_DIR/mail/dkim.key" ] || fail "DKIM key missing after restart"
[ "$(stat -c %a "$DATA_DIR/mail/dkim.key")" = 600 ] || fail "DKIM key mode is not 600"
compgen -G "$DATA_DIR/mail/spool/*" >/dev/null || fail "mail spool lost across restart"

./cli.sh stop >/dev/null
echo "e2e: OK"
