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

cdp_page_counts() {
  local payload page_count blank_title_count
  payload=$(docker exec "$NAME" curl -fsS http://127.0.0.1:9222/json 2>/dev/null) \
    || return 1
  page_count=$(printf '%s\n' "$payload" \
    | awk '/"type"[[:space:]]*:[[:space:]]*"page"/ { count++ } END { print count + 0 }')
  blank_title_count=$(printf '%s\n' "$payload" \
    | awk '/"title"[[:space:]]*:[[:space:]]*"about:blank"/ { count++ } END { print count + 0 }')
  printf '%s %s\n' "$page_count" "$blank_title_count"
}

check_chromium_posture() {
  local resolution window_ids windows geometry active page_count blank_title_count
  local attempt

  resolution=$(docker exec "$NAME" bash -c \
    'source /run/virtualme/config.env; printf "%s\n" "$VM_EFFECTIVE_RESOLUTION"')
  resolution="${resolution%x*}"

  window_ids=$(docker exec -e DISPLAY=:99 "$NAME" \
    xdotool search --onlyvisible --class chromium)
  windows=$(printf '%s\n' "$window_ids" | sed '/^$/d' | wc -l)
  [ "$windows" = "1" ] \
    || fail "expected one mapped chromium-class window, found $windows"

  active=""
  for attempt in $(seq 1 25); do
    active=$(docker exec -e DISPLAY=:99 "$NAME" xdotool getactivewindow 2>/dev/null || true)
    [ "$active" = "$window_ids" ] && break
    sleep 0.2
  done
  [ "$active" = "$window_ids" ] \
    || fail "active X window $active is not mapped Chromium window $window_ids"

  geometry=$(docker exec -e DISPLAY=:99 "$NAME" bash -c \
    'xdotool getactivewindow getwindowgeometry --shell') \
    || fail "could not read active Chromium geometry"
  printf '%s\n' "$geometry" | grep -qx 'X=0' \
    || fail "active chromium window is not at X=0: $geometry"
  printf '%s\n' "$geometry" | grep -qx 'Y=0' \
    || fail "active chromium window is not at Y=0: $geometry"
  printf '%s\n' "$geometry" | grep -qx "WIDTH=${resolution%x*}" \
    || fail "active chromium width does not match $resolution: $geometry"
  printf '%s\n' "$geometry" | grep -qx "HEIGHT=${resolution#*x}" \
    || fail "active chromium height does not match $resolution: $geometry"

  read -r page_count blank_title_count < <(cdp_page_counts)
  [ "$page_count" = "1" ] && [ "$blank_title_count" = "1" ] \
    || fail "expected one about:blank CDP page target, found pages=$page_count blank_titles=$blank_title_count"
}

check_chromium_restart_timing() {
  local iteration old_pid new_pid started_ms now_ms elapsed_ms
  local page_count blank_title_count

  for iteration in 1 2 3; do
    old_pid=$(chromium_pid) \
      || fail "could not find chromium before timed restart $iteration"
    started_ms=$(date +%s%3N)
    docker exec "$NAME" /command/s6-svc -r /run/service/svc-chromium \
      || fail "could not restart svc-chromium on timed restart $iteration"
    while true; do
      new_pid=$(chromium_pid 2>/dev/null || true)
      read -r page_count blank_title_count < <(cdp_page_counts || printf '0 0\n')
      if [ -n "$new_pid" ] && [ "$new_pid" != "$old_pid" ] &&
         [ "$page_count" = "1" ] && [ "$blank_title_count" = "1" ]; then
        elapsed_ms=$(( $(date +%s%3N) - started_ms ))
        [ "$elapsed_ms" -lt 15000 ] \
          || fail "timed chromium restart $iteration took ${elapsed_ms}ms"
        echo "smoke: chromium restart $iteration exposed one page in ${elapsed_ms}ms"
        break
      fi
      now_ms=$(date +%s%3N)
      [ "$now_ms" -lt $((started_ms + 15000)) ] \
        || fail "timed chromium restart $iteration did not expose one page within 15s"
      sleep 0.2
    done
  done
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
[ -d "$DATA_DIR/tts-cache" ] || fail "data dir missing tts-cache"
docker exec "$NAME" pgrep -f 'svc-mailq' >/dev/null \
  || fail "svc-mailq is not supervised and up"
docker exec "$NAME" sh -c \
  'grep -q "0100007F:1F92" /proc/net/tcp && ! grep -q "00000000:1F92" /proc/net/tcp' \
  || fail "ttsd is not bound exclusively to 127.0.0.1:8082"

echo "smoke: checking vision projector pin and read-only CDP endpoint"
docker exec "$NAME" sh -c \
  'echo "140be8d7849741f88c50757d529b84373ee8e27052cc2236855b537f4a8215fa  /opt/models/mmproj-gemma-4-E2B-F16.gguf" | sha256sum -c -' \
  >/dev/null || fail "vision projector checksum mismatch"

echo "smoke: checking llama vision completion"
tiny_png="iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
vision_body=$(printf '{"stream":false,"max_tokens":1,"messages":[{"role":"user","content":[{"type":"text","text":"Reply OK."},{"type":"image_url","image_url":{"url":"data:image/png;base64,%s"}}]}]}' "$tiny_png")
docker exec "$NAME" curl -fsS --max-time "$TIMEOUT" \
  -H 'Content-Type: application/json' -d "$vision_body" \
  http://127.0.0.1:8081/v1/chat/completions \
  | grep -q '"choices"' || fail "llama vision completion failed"

echo "smoke: checking full-screen single-window chromium posture"
check_chromium_posture
echo "smoke: checking three bounded chromium restarts"
check_chromium_restart_timing
check_chromium_posture

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
    valkey|chromium|xdg|metrics|agent|mail|projects|tts-cache|controller-lifecycle.json|virtualme.config.yaml|virtualme.config.yaml.lock|.virtualme.config.yaml.tmp-*) ;;
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
echo "smoke: checking forced-fallback chromium posture and startup timing"
check_chromium_posture
check_chromium_restart_timing
check_chromium_posture
cmdline=$(chromium_cmdline) || fail "could not read forced-fallback command line"
case "$cmdline" in
  *--no-sandbox*--test-type*|*--test-type*--no-sandbox*) ;;
  *) fail "forced fallback did not include --no-sandbox and --test-type" ;;
esac

echo "smoke: OK"
