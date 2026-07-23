#!/usr/bin/env bash
# Full E2E: container builds and goes healthy; master orchestrator answers 2xx;
# remote desktop is reachable through port 8080; a browser is visible on the
# virtual display.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

IMAGE_TAG="virtualme:e2e"
NAME="virtualme-e2e"
PORT="${E2E_PORT:-18081}"
TIMEOUT="${E2E_TIMEOUT:-300}"
BASE="http://127.0.0.1:${PORT}"

fail() {
  echo "e2e: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  exit 1
}

echo "e2e: building image"
docker build -f docker/Dockerfile -t "$IMAGE_TAG" . || { echo "e2e: FAIL: build" >&2; exit 1; }

docker rm -f "$NAME" >/dev/null 2>&1 || true
echo "e2e: starting container"
docker run -d --name "$NAME" --shm-size=1g -p "${PORT}:8080" "$IMAGE_TAG" >/dev/null

echo "e2e: [1/5] waiting for all-green /healthz (timeout ${TIMEOUT}s)"
deadline=$(( $(date +%s) + TIMEOUT ))
until curl -fsS "$BASE/healthz" 2>/dev/null | grep -q '"ok":true'; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "healthz not green within ${TIMEOUT}s: $(curl -s "$BASE/healthz" || echo unreachable)"
  fi
  sleep 5
done

echo "e2e: [2/5] orchestrator serves the SPA with 2xx"
code=$(curl -s -o /tmp/e2e-index.html -w '%{http_code}' "$BASE/")
[ "$code" = 200 ] || fail "GET / returned $code"
grep -q "Virtual Me" /tmp/e2e-index.html || fail "SPA markup missing from /"

echo "e2e: [3/5] websocket endpoint rejects non-upgrade with 400"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/ws")
[ "$code" = 400 ] || fail "GET /ws returned $code (expected 400)"

echo "e2e: [4/5] remote desktop (noVNC via reverse proxy) serves 2xx"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/desktop/vnc.html")
[ "$code" = 200 ] || fail "GET /desktop/vnc.html returned $code"

echo "e2e: [5/5] a browser window is visible on the virtual display"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

docker rm -f "$NAME" >/dev/null
echo "e2e: OK"
