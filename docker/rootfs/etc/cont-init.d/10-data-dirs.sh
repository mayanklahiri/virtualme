#!/command/with-contenv bash
set -euo pipefail
mkdir -p "$VM_DATA_DIR/valkey" "$VM_DATA_DIR/chromium" "$VM_DATA_DIR/metrics" \
  "$VM_DATA_DIR/agent" "$VM_DATA_DIR/xdg/config" "$VM_DATA_DIR/xdg/cache" \
  "$VM_DATA_DIR/xdg/data" "$VM_DATA_DIR/mail/spool" "$VM_DATA_DIR/projects" \
  "$VM_DATA_DIR/tts-cache"
rm -f "$VM_DATA_DIR/chromium/SingletonCookie" \
  "$VM_DATA_DIR/chromium/SingletonLock" \
  "$VM_DATA_DIR/chromium/SingletonSocket"
