#!/command/with-contenv bash
set -euo pipefail
mkdir -p "$VM_DATA_DIR/valkey" "$VM_DATA_DIR/chromium" "$VM_DATA_DIR/metrics" \
  "$VM_DATA_DIR/agent" "$VM_DATA_DIR/xdg/config" "$VM_DATA_DIR/xdg/cache" \
  "$VM_DATA_DIR/xdg/data"
rm -f "$VM_DATA_DIR/chromium/SingletonCookie" \
  "$VM_DATA_DIR/chromium/SingletonLock" \
  "$VM_DATA_DIR/chromium/SingletonSocket"
