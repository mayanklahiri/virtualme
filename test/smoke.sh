#!/usr/bin/env bash
# Container smoke test: build image, boot with the production runtime posture
# (non-root user matching the invoking uid/gid, tmpfs /run and /tmp, single rw
# data mount), poll /healthz until all-green, and exercise Chromium
# supervision, sandbox selection, and profile persistence.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

IMAGE_TAG="virtualme:smoke"
NAME="virtualme-smoke-$$"
PORT="${SMOKE_PORT:-18080}"
TIMEOUT="${SMOKE_TIMEOUT:-300}"
DATA_DIR="$(mktemp -d)"

fail() {
  echo "smoke: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -200 >&2 || true
  exit 1
}

cleanup() {
  docker rm -f "$NAME" >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
}
trap cleanup EXIT

wait_green() {
  local timeout="${1:-$TIMEOUT}"
  local deadline=$(( $(date +%s) + timeout ))
  until curl -fsS "http://127.0.0.1:${PORT}/healthz" 2>/dev/null | grep -q '"ok":true'; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      fail "healthz not green within ${timeout}s: $(curl -s "http://127.0.0.1:${PORT}/healthz" || echo unreachable)"
    fi
    sleep 2
  done
}

chromium_pid() {
  docker exec "$NAME" bash -c '
    for proc in /proc/[0-9]*; do
      comm=$(cat "$proc/comm" 2>/dev/null) || continue
      case "$comm" in chromium*) ;; *) continue ;; esac
      cmd=$(tr "\0" " " < "$proc/cmdline" 2>/dev/null) || continue
      case "$cmd" in
        *"--user-data-dir=$VM_DATA_DIR/chromium"*) basename "$proc"; exit 0 ;;
      esac
    done
    exit 1
  '
}

chromium_cmdline() {
  docker exec "$NAME" bash -c '
    for proc in /proc/[0-9]*; do
      comm=$(cat "$proc/comm" 2>/dev/null) || continue
      case "$comm" in chromium*) ;; *) continue ;; esac
      cmd=$(tr "\0" " " < "$proc/cmdline" 2>/dev/null) || continue
      case "$cmd" in
        *"--user-data-dir=$VM_DATA_DIR/chromium"*) printf "%s\n" "$cmd"; exit 0 ;;
      esac
    done
    exit 1
  '
}

echo "smoke: building image"
docker build -f docker/Dockerfile -t "$IMAGE_TAG" . || { echo "smoke: FAIL: build" >&2; exit 1; }

echo "smoke: starting container (uid $(id -u))"
docker run -d --name "$NAME" --shm-size=1g \
  --user "$(id -u):$(id -g)" \
  --tmpfs "/run:exec,mode=755,uid=$(id -u),gid=$(id -g)" \
  --tmpfs /tmp:mode=1777 \
  -p "${PORT}:8080" \
  -v "${DATA_DIR}:/home/virtualme/.virtualme" \
  "$IMAGE_TAG" >/dev/null

echo "smoke: waiting for all-green /healthz (timeout ${TIMEOUT}s)"
wait_green
curl -fsS "http://127.0.0.1:${PORT}/healthz" \
  | grep -q '"name":"tts","ok":true' || fail "TTS health probe missing or unhealthy"
curl -fsS "http://127.0.0.1:${PORT}/healthz" \
  | grep -q '"name":"mail","ok":true' || fail "mail health probe missing or unhealthy"
[ -d "$DATA_DIR/mail/spool" ] || fail "data dir missing mail/spool"
docker exec "$NAME" pgrep -f 'svc-mailq' >/dev/null \
  || fail "svc-mailq is not supervised and up"
docker exec "$NAME" sh -c \
  'grep -q "0100007F:1F92" /proc/net/tcp && ! grep -q "00000000:1F92" /proc/net/tcp' \
  || fail "ttsd is not bound exclusively to 127.0.0.1:8082"

echo "smoke: checking vision projector pin and read-only CDP endpoint"
docker exec "$NAME" sh -c \
  'echo "140be8d7849741f88c50757d529b84373ee8e27052cc2236855b537f4a8215fa  /opt/models/mmproj-gemma-4-E2B-F16.gguf" | sha256sum -c -' \
  >/dev/null || fail "vision projector checksum mismatch"
docker exec "$NAME" curl -fsS http://127.0.0.1:9222/json \
  | grep -q '"webSocketDebuggerUrl"' || fail "Chromium CDP endpoint unavailable"

echo "smoke: checking llama vision completion"
tiny_png="iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
vision_body=$(printf '{"stream":false,"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"Reply OK."},{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}' "$tiny_png")
docker exec "$NAME" curl -fsS --max-time "$TIMEOUT" \
  -H 'Content-Type: application/json' -d "$vision_body" \
  http://127.0.0.1:8081/v1/chat/completions \
  | grep -q '"choices"' || fail "llama vision completion failed"

echo "smoke: checking for visible chromium window"
docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null \
  || fail "no visible chromium window on :99"

echo "smoke: checking exactly one chromium window"
window_count=$(docker exec -e DISPLAY=:99 "$NAME" bash -c \
  'xdotool search --onlyvisible --class chromium 2>/dev/null | wc -l')
[ "$window_count" = "1" ] || fail "expected one visible chromium window, found $window_count"

echo "smoke: checking chromium sandbox flags"
cmdline=$(chromium_cmdline) || fail "could not read chromium command line"
case "$cmdline" in
  *--no-sandbox*)
    case "$cmdline" in
      *--test-type*) echo "smoke: chromium sandbox mode: fallback" ;;
      *) fail "chromium fallback has --no-sandbox without --test-type" ;;
    esac
    ;;
  *) echo "smoke: chromium sandbox mode: namespace" ;;
esac

echo "smoke: closing chromium window and waiting for supervised restart"
old_pid=$(chromium_pid) || fail "could not find chromium browser process"
docker exec -e DISPLAY=:99 "$NAME" bash -c \
  'xdotool search --onlyvisible --class chromium windowkill' \
  || fail "could not close chromium window"
deadline=$(( $(date +%s) + 30 ))
while true; do
  new_pid=$(chromium_pid 2>/dev/null || true)
  if [ -n "$new_pid" ] && [ "$new_pid" != "$old_pid" ] &&
     docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null 2>&1 &&
     curl -fsS "http://127.0.0.1:${PORT}/healthz" 2>/dev/null | grep -q '"ok":true'; then
    break
  fi
  [ "$(date +%s)" -lt "$deadline" ] || fail "chromium window did not restart within 30s"
  sleep 1
done
window_count=$(docker exec -e DISPLAY=:99 "$NAME" bash -c \
  'xdotool search --onlyvisible --class chromium 2>/dev/null | wc -l')
[ "$window_count" = "1" ] || fail "restart produced $window_count visible chromium windows"

echo "smoke: checking watchdog recovers a live process with no visible window"
old_pid=$(chromium_pid) || fail "could not find chromium before watchdog test"
docker exec -e DISPLAY=:99 "$NAME" bash -c \
  'xdotool search --onlyvisible --class chromium windowunmap' \
  || fail "could not unmap chromium window"
docker exec "$NAME" test -d "/proc/$old_pid" \
  || fail "chromium exited instead of exercising the watchdog"
deadline=$(( $(date +%s) + 20 ))
while true; do
  new_pid=$(chromium_pid 2>/dev/null || true)
  if [ -n "$new_pid" ] && [ "$new_pid" != "$old_pid" ] &&
     docker exec -e DISPLAY=:99 "$NAME" xdotool search --onlyvisible --class chromium >/dev/null 2>&1; then
    break
  fi
  [ "$(date +%s)" -lt "$deadline" ] || fail "watchdog did not restore chromium within 20s"
  sleep 1
done

echo "smoke: checking data dir is populated and host-owned"
[ -d "$DATA_DIR/valkey" ] || fail "data dir missing valkey/"
compgen -G "$DATA_DIR/valkey/appendonly*" >/dev/null \
  || fail "valkey append-only persistence is missing"
for entry in "$DATA_DIR"/*; do
  [ -e "$entry" ] || continue
  case "$(basename "$entry")" in
    valkey|chromium|xdg|metrics|agent|mail) ;;
    *) fail "unexpected top-level data entry: $(basename "$entry")" ;;
  esac
done
[ "$(stat -c %u "$DATA_DIR/valkey")" = "$(id -u)" ] || fail "data files not owned by host user"

echo "smoke: checking container runs unprivileged"
[ "$(docker exec "$NAME" id -u)" = "$(id -u)" ] || fail "container not running as host uid"

echo "smoke: checking chromium profile persistence across container restart"
docker exec "$NAME" touch "/home/virtualme/.virtualme/chromium/Default/vm-sentinel" \
  || fail "could not write profile sentinel"
docker stop -t 15 "$NAME" >/dev/null || fail "container stop failed"
[ -f "$DATA_DIR/chromium/Default/vm-sentinel" ] || fail "profile sentinel missing after stop"
docker start "$NAME" >/dev/null || fail "container restart failed"
wait_green
docker exec "$NAME" test -f "/home/virtualme/.virtualme/chromium/Default/vm-sentinel" \
  || fail "profile sentinel missing after restart"

echo "smoke: checking forced sandbox fallback"
docker rm -f "$NAME" >/dev/null || fail "container replacement failed"
docker run -d --name "$NAME" --shm-size=1g \
  --user "$(id -u):$(id -g)" \
  --tmpfs "/run:exec,mode=755,uid=$(id -u),gid=$(id -g)" \
  --tmpfs /tmp:mode=1777 \
  -e VM_CHROMIUM_NO_SANDBOX=1 \
  -p "${PORT}:8080" \
  -v "${DATA_DIR}:/home/virtualme/.virtualme" \
  "$IMAGE_TAG" >/dev/null
wait_green
cmdline=$(chromium_cmdline) || fail "could not read forced-fallback command line"
case "$cmdline" in
  *--no-sandbox*--test-type*|*--test-type*--no-sandbox*) ;;
  *) fail "forced fallback did not include --no-sandbox and --test-type" ;;
esac

echo "smoke: OK"
