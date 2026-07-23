#!/command/with-contenv bash
# Select Chromium's namespace sandbox when this container can create a user
# namespace. Fall back safely, with the unsupported-flag infobar suppressed.
SANDBOX_FLAGS=()
userns_ok=0

if [ "${VM_CHROMIUM_NO_SANDBOX:-0}" != "1" ]; then
  sysctl_ok=1
  if [ -r /proc/sys/kernel/unprivileged_userns_clone ] &&
     [ "$(cat /proc/sys/kernel/unprivileged_userns_clone)" != "1" ]; then
    sysctl_ok=0
  fi
  if [ "$sysctl_ok" = "1" ] &&
     unshare --user --map-root-user true >/dev/null 2>&1; then
    userns_ok=1
  fi
fi

if [ "$userns_ok" = "1" ]; then
  SANDBOX_FLAGS+=(--disable-gpu)
else
  SANDBOX_FLAGS+=(--no-sandbox --test-type --disable-gpu)
fi
