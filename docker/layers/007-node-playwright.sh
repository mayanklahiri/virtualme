#!/usr/bin/env bash
# Layer 007: Node.js + playwright-core for driving the system Chromium.
# PLAYWRIGHT_VERSION is exact-pinned; bump only via spec amendment.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

PLAYWRIGHT_VERSION="1.61.1"

apt-get update
apt-get install -y --no-install-recommends nodejs npm
rm -rf /var/lib/apt/lists/*

mkdir -p /opt/agent
cd /opt/agent
npm init -y >/dev/null
npm install --save-exact "playwright-core@${PLAYWRIGHT_VERSION}"
node -e "require('playwright-core'); console.log('playwright-core ok')"
