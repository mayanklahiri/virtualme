#!/command/with-contenv bash
set -euo pipefail

mail_dir="$VM_DATA_DIR/mail"
mkdir -p "$mail_dir/spool"
mailname="${VM_MAIL_MAILNAME:-$(hostname)}"
{
  printf 'MAILNAME %s\n' "$mailname"
  if [[ -n "${VM_MAIL_SMARTHOST:-}" ]]; then
    printf 'SMARTHOST %s\n' "$VM_MAIL_SMARTHOST"
    printf 'PORT %s\n' "${VM_MAIL_SMARTHOST_PORT:-587}"
    printf 'STARTTLS\nSECURETRANSFER\n'
    if [[ -n "${VM_MAIL_SMARTHOST_USER:-}" && -n "${VM_MAIL_SMARTHOST_PASS:-}" ]]; then
      printf 'AUTHPATH /etc/dma/auth.conf\n'
    fi
  fi
} > "$mail_dir/dma.conf"

if [[ -n "${VM_MAIL_SMARTHOST_USER:-}" && -n "${VM_MAIL_SMARTHOST_PASS:-}" && -n "${VM_MAIL_SMARTHOST:-}" ]]; then
  printf '%s|%s:%s\n' "$VM_MAIL_SMARTHOST_USER" "$VM_MAIL_SMARTHOST" \
    "$VM_MAIL_SMARTHOST_PASS" > "$mail_dir/auth.conf"
  chmod 0600 "$mail_dir/auth.conf"
else
  rm -f "$mail_dir/auth.conf"
fi
