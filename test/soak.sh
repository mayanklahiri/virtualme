#!/usr/bin/env bash
# Soak suite orchestration (spec 012 §5b): rebuild the image (unless
# --no-build), restart the container on a fresh temp data dir, run the live
# soak flows in test/soak.mjs against the running controller, then restore
# the previous deployment if one was running.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

BUILD=1
for arg in "$@"; do
  case "$arg" in
    --no-build) BUILD=0 ;;
    *) echo "soak: unknown argument: $arg" >&2; exit 2 ;;
  esac
done

NAME="virtualme"
PORT=8080
BASE="http://127.0.0.1:${PORT}"
TIMEOUT="${SOAK_HEALTH_TIMEOUT:-300}"
DATA_DIR="$(mktemp -d)"
WAS_RUNNING=0
if docker inspect -f '{{.State.Running}}' "$NAME" 2>/dev/null | grep -q true; then
  WAS_RUNNING=1
fi

say() { printf '\033[1msoak:\033[0m %s\n' "$*"; }

cleanup() {
  local code=$?
  say "stopping soak container"
  ./cli.sh stop >/dev/null 2>&1 || true
  rm -rf "$DATA_DIR"
  if [ "$WAS_RUNNING" = 1 ]; then
    say "restoring previous deployment on the default data dir"
    ./cli.sh start >/dev/null 2>&1 || say "WARNING: could not restart previous container"
  fi
  exit "$code"
}
trap cleanup EXIT

fail() {
  echo "soak: FAIL: $*" >&2
  echo "--- container logs (tail) ---" >&2
  docker logs "$NAME" 2>&1 | tail -100 >&2 || true
  exit 1
}

if [ "$BUILD" = 1 ]; then
  say "building image (./cli.sh build; pass --no-build to skip)"
  ./cli.sh build || fail "image build"
else
  say "skipping image build (--no-build)"
fi

say "restarting container on fresh data dir ${DATA_DIR}"
./cli.sh stop >/dev/null 2>&1 || true
VIRTUALME_DATA="$DATA_DIR" ./cli.sh start >/dev/null || fail "cli start"

say "waiting for all-green /healthz (timeout ${TIMEOUT}s)"
deadline=$(( $(date +%s) + TIMEOUT ))
until curl -fsS "$BASE/healthz" 2>/dev/null | grep -q '"ok":true'; do
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "healthz not green within ${TIMEOUT}s: $(curl -s "$BASE/healthz" || echo unreachable)"
  fi
  sleep 5
done

say "running soak flows (test/soak.mjs)"
SOAK_DATA_DIR="$DATA_DIR" node test/soak.mjs
