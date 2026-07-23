#!/usr/bin/env bash
# Layer 005: Chromium from Debian (no snap; sandbox disabled at runtime in-container).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends chromium fonts-liberation
rm -rf /var/lib/apt/lists/*
