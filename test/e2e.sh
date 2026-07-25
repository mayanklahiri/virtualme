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

start_vm() {
  if [ "${E2E_GPU:-0}" = "1" ]; then
    ./cli.sh start --gpus all
  else
    ./cli.sh start
  fi
}

if [ "${E2E_SKIP_BUILD:-0}" = "1" ]; then
  echo "e2e: [1/20] skipping CLI build (E2E_SKIP_BUILD=1)"
else
  echo "e2e: [1/20] CLI build (tags :dev and the start tag)"
  ./cli.sh build >/dev/null || fail "cli build"
fi

expect_invalid_config() {
  local label="$1"
  local expected="$2"
  local content="$3"
  local invalid_root
  invalid_root="$(mktemp -d)"
  printf '%s' "$content" >"$invalid_root/virtualme.config.yaml"
  chmod 600 "$invalid_root/virtualme.config.yaml"
  VIRTUALME_DATA="$invalid_root" ./cli.sh start >/dev/null || true
  local deadline=$(( $(date +%s) + 30 ))
  local logs=""
  until logs="$(docker logs "$NAME" 2>&1 || true)" && printf '%s' "$logs" | grep -q "$expected"; do
    [ "$(date +%s)" -lt "$deadline" ] || fail "$label config did not fail with '$expected'"
    sleep 1
  done
  docker exec "$NAME" pgrep -x controller >/dev/null 2>&1 \
    && fail "$label config started controller despite preflight failure"
  printf '%s' "$logs" | grep -q 'DO_NOT_LEAK_031' && fail "$label config leaked sentinel"
  VIRTUALME_DATA="$invalid_root" ./cli.sh stop >/dev/null 2>&1 || true
  rm -rf "$invalid_root"
}

./cli.sh stop >/dev/null 2>&1 || true
echo "e2e: [2a/20] invalid master configs fail before longruns"
expect_invalid_config "malformed" "indentation must use two spaces" $'version:\n   bad: 1\n'
expect_invalid_config "unknown-key" "unknown configuration key" $'version: 1\nunknown: true\n'
expect_invalid_config "wrong-type" "agent.maxSteps" $'version: 1\nagent:\n  maxSteps: fast\n'
expect_invalid_config "duplicate-key" "duplicate key" $'version: 1\nversion: 1\n'
expect_invalid_config "bad-secret" "mail.smarthost.password" \
  $'version: 1\nmail:\n  smarthost:\n    host: smtp.example\n    username: user\n    password: DO_NOT_LEAK_031\n'

echo "e2e: [2b/20] preflight applies YAML and legacy service settings"
CONFIG_ROOT="$(mktemp -d)"
printf '%s' $'desktop:\n  resolution: 1280x720x24\n' >"$CONFIG_ROOT/virtualme.config.yaml"
chmod 600 "$CONFIG_ROOT/virtualme.config.yaml"
VIRTUALME_DATA="$CONFIG_ROOT" ./cli.sh start >/dev/null || fail "non-default config start"
deadline=$(( $(date +%s) + TIMEOUT ))
until docker exec "$NAME" grep -q "VM_EFFECTIVE_RESOLUTION='1280x720x24'" /run/virtualme/config.env 2>/dev/null; do
  [ "$(date +%s)" -lt "$deadline" ] || fail "non-default resolution was not exported"
  sleep 1
done
docker exec "$NAME" sh -c "tr '\\0' ' ' </proc/\$(pgrep -x Xvfb)/cmdline" \
  | grep -q '1280x720x24' || fail "Xvfb did not consume configured resolution"
VIRTUALME_DATA="$CONFIG_ROOT" ./cli.sh stop >/dev/null || fail "non-default config stop"
rm -rf "$CONFIG_ROOT"

LEGACY_ROOT="$(mktemp -d)"
VM_RESOLUTION=1366x768x24 VIRTUALME_DATA="$LEGACY_ROOT" ./cli.sh start >/dev/null \
  || fail "legacy config start"
deadline=$(( $(date +%s) + TIMEOUT ))
until docker exec "$NAME" test -f /run/virtualme/config.env 2>/dev/null; do
  [ "$(date +%s)" -lt "$deadline" ] || fail "legacy preflight did not complete"
  sleep 1
done
[ "$(docker logs "$NAME" 2>&1 | grep -c 'legacy VM_RESOLUTION overrides desktop.resolution')" = 1 ] \
  || fail "legacy override did not emit exactly one deprecation warning"
docker exec "$NAME" grep -q "VM_EFFECTIVE_RESOLUTION='1366x768x24'" /run/virtualme/config.env \
  || fail "legacy override did not win"
VIRTUALME_DATA="$LEGACY_ROOT" ./cli.sh stop >/dev/null || fail "legacy config stop"
rm -rf "$LEGACY_ROOT"

echo "e2e: [2/20] starting SMTP sink and CLI on fresh data dir ${DATA_DIR}"
MAIL_SINK_HOST=0.0.0.0 MAIL_SINK_ACCEPT_DELAY_MS=1500 \
  node test/mail-sink.mjs "$MAIL_CAPTURE" >/tmp/virtualme-mail-sink.log 2>&1 &
MAIL_SINK_PID=$!
sleep 1
kill -0 "$MAIL_SINK_PID" 2>/dev/null || fail "mail sink did not start"
export VM_MAIL_SMARTHOST=vmhost
export VM_MAIL_SMARTHOST_PORT=2525
export VM_MAIL_DKIM_DOMAIN=example.test
export VM_MAIL_FLUSH_SEC=5
start_vm >/dev/null || fail "cli start"

echo "e2e: [3/20] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_healthy
[ -f "$DATA_DIR/virtualme.config.yaml" ] || fail "master config was not seeded"
[ "$(stat -c %a "$DATA_DIR/virtualme.config.yaml")" = 600 ] || fail "master config mode is not 600"
curl -fsS "$BASE/api/config/schema" | grep -q '"schemaVersion":1' \
  || fail "config schema endpoint missing"
curl -fsS "$BASE/api/config" | grep -q '"pendingRestart":false' \
  || fail "config state endpoint missing"
docker exec "$NAME" configctl docs --check \
  --output /opt/virtualme/docs/src/generated/config-reference.json >/dev/null \
  || fail "config documentation stale"

echo "e2e: [3a/20] config save conflicts, preflight failure, and restart lifecycle"
controller_pid=$(docker exec "$NAME" pgrep -x controller)
llama_pid=$(docker exec "$NAME" pgrep -f '/llama-server')
docker exec -u 0 "$NAME" chmod 500 /run/virtualme
node test/config-probe.mjs --preflight-failure "$BASE" \
  || fail "config preflight-failure probe"
docker exec -u 0 "$NAME" chmod 700 /run/virtualme
[ "$(docker exec "$NAME" pgrep -x controller)" = "$controller_pid" ] \
  || fail "controller restarted after failed preflight"
[ "$(docker exec "$NAME" pgrep -f '/llama-server')" = "$llama_pid" ] \
  || fail "llama restarted after failed preflight"
curl -fsS "$BASE/api/config" | grep -q '"pendingRestart":true' \
  || fail "failed preflight did not leave pending config"
wait_healthy
sleep 1

controller_pid=$(docker exec "$NAME" pgrep -x controller)
llama_pid=$(docker exec "$NAME" pgrep -f '/llama-server')
node test/config-probe.mjs --restart "$BASE" || fail "config restart probe"
wait_healthy
[ "$(docker exec "$NAME" pgrep -x controller)" != "$controller_pid" ] \
  || fail "controller did not respawn after config restart"
[ "$(docker exec "$NAME" pgrep -f '/llama-server')" != "$llama_pid" ] \
  || fail "llama did not respawn after config restart"
node -e '
  const response = await fetch(process.argv[1] + "/api/config");
  const body = await response.json();
  if (!response.ok || body.pendingRestart ||
      body.fileHash !== body.startupHash ||
      body.effective.llama.contextTokens !== 32769) process.exit(1);
' "$BASE" || fail "restarted config hashes/effective value did not converge"

echo "e2e: [4/20] orchestrator serves branded SPA assets and sourcemaps"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"
curl -fsS "$BASE/js/generated-theme-boot.js" | grep -q '"arctic","solar","studio"' \
  || fail "SPA boot script does not list all eight themes"
content_type=$(curl -fsS -o /dev/null -w '%{content_type}' "$BASE/brand/hero.jpg")
[ "$content_type" = "image/jpeg" ] || fail "hero image content type is $content_type"
curl -fsS -o /dev/null "$BASE/favicon.svg" || fail "favicon.svg not served"
curl -fsS -o /dev/null "$BASE/favicon.ico" || fail "favicon.ico not served"
curl -fsS -o /dev/null "$BASE/apple-touch-icon.png" || fail "apple-touch-icon.png not served"
curl -fsS -o /dev/null "$BASE/brand/virtualme-mark.png" || fail "brand mark PNG not served"
curl -fsS "$BASE/icons.svg" | grep -q 'id="i-virtualme-mark"' || fail "brand mark missing from icon sprite"
curl -fsS "$BASE/js/app.js" | grep -q "sourceMappingURL" || fail "app.js missing sourcemap pointer"
curl -fsS -o /dev/null "$BASE/js/app.js.map" || fail "app.js.map not served"
curl -fsS -o /dev/null "$BASE/css/app.css.map" || fail "app.css.map not served"

echo "e2e: [5/20] SPA history routes fall back while missing assets stay 404"
for route in projects status speech mail desktop-view data; do
  code=$(curl -s -o /tmp/e2e-route.html -w '%{http_code}' "$BASE/$route")
  [ "$code" = 200 ] && grep -q "Virtual Me" /tmp/e2e-route.html \
    || fail "SPA fallback failed for /$route"
done
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/js/nope.js")
[ "$code" = 404 ] || fail "missing asset returned $code (expected 404)"

echo "e2e: [6/20] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [7/20] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [8/20] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "e2e: [9/20] state frames include hostname and disk capacity"
node test/state-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "state probe"
if [ "${E2E_GPU:-0}" = "1" ]; then
  node test/gpu-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "GPU probe"
else
  echo "e2e: GPU probe skipped (set E2E_GPU=1 on an NVIDIA host)"
fi
node test/jiggler-probe.mjs true "ws://127.0.0.1:${PORT}/ws" || fail "jiggler enable"
node test/jiggler-probe.mjs false "ws://127.0.0.1:${PORT}/ws" || fail "jiggler disable"

echo "e2e: [10/20] metrics protocol returns raw and 15-minute tiers"
node test/metrics-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "metrics probe"


echo "e2e: [11/20] data explorer lists the volume and rejects traversal"
list_json=$(curl -fsS "$BASE/api/data/list")
echo "$list_json" | grep -Eq '"name":"(metrics|valkey)"' \
  || fail "data list missing metrics/valkey: $list_json"
du_json=$(curl -fsS "$BASE/api/data/du")
echo "$du_json" | grep -q '"sizes":{' \
  || fail "data du missing sizes object: $du_json"
for probe in \
  "$BASE/api/data/file?path=../../etc/passwd" \
  "$BASE/api/data/file?path=%2e%2e%2f%2e%2e%2fetc%2fpasswd" \
  "$BASE/api/data/du?path=../../etc"
do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$probe")
  [ "$code" = 404 ] || fail "traversal probe $probe returned $code (expected 404)"
done
printf '{"e2e":true}\n' > "$DATA_DIR/e2e-data.json"
code=$(curl -s -o /tmp/e2e-data-file -w '%{http_code}' "$BASE/api/data/file?path=e2e-data.json")
[ "$code" = 200 ] || fail "GET e2e-data.json returned $code"
ctype=$(curl -fsS -o /dev/null -w '%{content_type}' "$BASE/api/data/file?path=e2e-data.json")
echo "$ctype" | grep -qi 'json' || fail "e2e-data.json content type is $ctype"
grep -q '"e2e":true' /tmp/e2e-data-file || fail "e2e-data.json body mismatch"

echo "e2e: [12/20] local TTS streams speech and serves OpenAI-compatible WAV"
node test/tts-probe.mjs "ws://127.0.0.1:${PORT}/ws" "$BASE" || fail "tts probe"

echo "e2e: [13/20] outbound mail reaches sink with MIME, CID, and DKIM"
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

echo "e2e: [14/20] queue probe runs through pushed, running, and finished states"
node test/queue-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "queue probe"

echo "e2e: [15/20] project CRUD and manual run persist through the queue"
project_output=$(node test/projects-probe.mjs "ws://127.0.0.1:${PORT}/ws") || fail "projects probe"
printf '%s\n' "$project_output"
project_id="${project_output##*id=}"
[ -n "$project_id" ] || fail "projects probe did not report id"

echo "e2e: [16/20] chat round-trip streams at least one delta"
node test/chat-probe.mjs "ws://127.0.0.1:${PORT}/ws" || fail "chat probe"

echo "e2e: [17/20] chat generation can be stopped after its first delta"
node test/chat-probe.mjs --stop "ws://127.0.0.1:${PORT}/ws" || fail "chat stop probe"

echo "e2e: [18/20] optional browser-agent tasks produce browser and speech steps"
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

if [ "${E2E_JIGGLER:-0}" = "1" ]; then
  echo "e2e: optional jiggler motion and stop probe"
  node test/jiggler-probe.mjs true "ws://127.0.0.1:${PORT}/ws" || fail "jiggler enable"
  before=$(docker exec -e DISPLAY=:99 "$NAME" xdotool getmouselocation --shell) || fail "read cursor before jiggle"
  sleep 60
  after=$(docker exec -e DISPLAY=:99 "$NAME" xdotool getmouselocation --shell) || fail "read cursor after jiggle"
  [ "$before" != "$after" ] || fail "cursor did not move while jiggler enabled"
  node test/jiggler-probe.mjs false "ws://127.0.0.1:${PORT}/ws" || fail "jiggler disable"
  before=$(docker exec -e DISPLAY=:99 "$NAME" xdotool getmouselocation --shell) || fail "read cursor before disabled interval"
  sleep 60
  after=$(docker exec -e DISPLAY=:99 "$NAME" xdotool getmouselocation --shell) || fail "read cursor after disabled interval"
  [ "$before" = "$after" ] || fail "cursor moved while jiggler disabled"
else
  echo "e2e: jiggler motion skipped (set E2E_JIGGLER=1 to enable)"
fi

echo "e2e: [19/20] direct mode accepts and queues deferred mail"
./cli.sh stop >/dev/null || fail "cli stop before direct mode"
unset VM_MAIL_SMARTHOST VM_MAIL_SMARTHOST_PORT
start_vm >/dev/null || fail "cli start (direct mode)"
wait_healthy
node test/mail-probe.mjs --direct "ws://127.0.0.1:${PORT}/ws" || fail "mail direct probe"

echo "e2e: [20/20] restart preserves chat, projects, speech, metrics, mail spool, and DKIM key"
compgen -G "$DATA_DIR/valkey/*" >/dev/null \
  || fail "valkey persistence is empty before restart"
compgen -G "$DATA_DIR/tts-cache/*.wav" >/dev/null \
  || fail "speech cache is empty before restart"
config_hash=$(sha256sum "$DATA_DIR/virtualme.config.yaml" | cut -d' ' -f1)
./cli.sh stop >/dev/null || fail "cli stop"
start_vm >/dev/null || fail "cli start (restart)"
wait_healthy
[ "$(sha256sum "$DATA_DIR/virtualme.config.yaml" | cut -d' ' -f1)" = "$config_hash" ] \
  || fail "restart changed canonical master config bytes"
[ -f "$DATA_DIR/metrics/tier0.json" ] || fail "metrics tier0.json missing after restart"
node test/metrics-probe.mjs --non-empty "ws://127.0.0.1:${PORT}/ws" \
  || fail "metrics history lost across restart"
node test/chat-probe.mjs --history-only "ws://127.0.0.1:${PORT}/ws" \
  || fail "chat history lost across restart"
node test/speech-log-probe.mjs "ws://127.0.0.1:${PORT}/ws" \
  || fail "speech history lost across restart"
compgen -G "$DATA_DIR/tts-cache/*.wav" >/dev/null \
  || fail "speech cache lost across restart"
node test/projects-probe.mjs --verify-delete "$project_id" "ws://127.0.0.1:${PORT}/ws" \
  || fail "project persistence/delete probe"
node test/jiggler-probe.mjs --expect false "ws://127.0.0.1:${PORT}/ws" \
  || fail "jiggler setting lost across restart"
[ -s "$DATA_DIR/mail/dkim.key" ] || fail "DKIM key missing after restart"
[ "$(stat -c %a "$DATA_DIR/mail/dkim.key")" = 600 ] || fail "DKIM key mode is not 600"
compgen -G "$DATA_DIR/mail/spool/*" >/dev/null || fail "mail spool lost across restart"

./cli.sh stop >/dev/null
echo "e2e: OK"
