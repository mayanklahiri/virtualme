#!/usr/bin/env bash
# Container smoke test: build image, boot with the production runtime posture
# (non-root user matching the invoking uid/gid, tmpfs /run and /tmp, single rw
# data mount), poll /healthz until all-green, verify a visible
# Chromium window on the Xvfb display and host-owned data files.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

IMAGE_TAG="virtualme:smoke"
NAME="virtualme-smoke"
PORT="${SMOKE_PORT:-18080}"
TIMEOUT="${SMOKE_TIMEOUT:-300}"
DATA_DIR="$(mktemp -d)"

fail() {
  echo "smoke: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
  exit 1
}

echo "smoke: building image"
docker build -f docker/Dockerfile -t "$IMAGE_TAG" . || { echo "smoke: FAIL: build" >&2; exit 1; }

docker rm -f "$NAME" >/dev/null 2>&1 || true
echo "smoke: starting container (uid $(id -u))"
docker run -d --name "$NAME" --shm-size=1g \
  --user "$(id -u):$(id -g)" \
  --tmpfs "/run:exec,mode=755,uid=$(id -u),gid=$(id -g)" \
  --tmpfs /tmp:mode=1777 \
  -p "${PORT}:8080" \
  -v "${DATA_DIR}:/home/virtualme/.virtualme" \
  "$IMAGE_TAG" >/dev/null

echo "smoke: waiting for all-green /healthz (timeout ${TIMEOUT}s)"
deadline=$(( $(date +%s) + TIMEOUT ))
until curl -fsS "http://127.0.0.1:${PORT}/healthz" 2>/dev/null | grep -q '"ok":true'; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "healthz not green within ${TIMEOUT}s: $(curl -s "http://127.0.0.1:${PORT}/healthz" || echo unreachable)"
  fi
  sleep 5
done

echo "smoke: checking for visible chromium window"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "smoke: checking data dir is populated and host-owned"
[ -d "$DATA_DIR/valkey" ] || fail "data dir missing valkey/"
[ "$(stat -c %u "$DATA_DIR/valkey")" = "$(id -u)" ] || fail "data files not owned by host user"

echo "smoke: checking container runs unprivileged"
[ "$(docker exec "$NAME" id -u)" = "$(id -u)" ] || fail "container not running as host uid"

docker rm -f "$NAME" >/dev/null
rm -rf "$DATA_DIR"
echo "smoke: OK"
