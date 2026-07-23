#!/usr/bin/env bash
# Layer 006: Valkey (Debian's Redis-compatible fork).
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends valkey-server
rm -rf /var/lib/apt/lists/*
