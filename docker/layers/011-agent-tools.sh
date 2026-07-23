#!/usr/bin/env bash
# Layer 011: OS-level agent screen capture and image processing.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends imagemagick scrot
rm -rf /var/lib/apt/lists/*
