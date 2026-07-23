#!/usr/bin/env bash
# Layer 001: OS base. Slowest-moving layer; edit only via spec amendment.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get upgrade -y
apt-get install -y --no-install-recommends \
  ca-certificates curl xz-utils procps
rm -rf /var/lib/apt/lists/*
