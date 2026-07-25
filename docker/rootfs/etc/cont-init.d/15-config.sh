#!/command/with-contenv bash
set -euo pipefail
mkdir -p /run/virtualme
configctl preflight --data-dir "$VM_DATA_DIR" \
  --service-env /run/virtualme/config.env \
  --mail-dir "$VM_DATA_DIR/mail"
