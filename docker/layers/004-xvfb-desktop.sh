#!/usr/bin/env bash
# Layer 004: virtual display + window manager + VNC + noVNC + X tooling.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends \
  xvfb openbox x11vnc novnc websockify dbus-x11 xdotool x11-utils
rm -rf /var/lib/apt/lists/*
