#!/usr/bin/env bash
# Layer 009: unprivileged runtime user. The CLI overrides uid/gid at run time
# with --user to match the host user; this account provides the default
# identity, the home path, and the data mountpoint parent.
set -euo pipefail
groupadd --gid 1000 virtualme
useradd --uid 1000 --gid 1000 --create-home --shell /usr/sbin/nologin virtualme
