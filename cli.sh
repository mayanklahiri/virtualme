#!/usr/bin/env bash
# Shortcut to the Virtual Me CLI from a source checkout.
set -euo pipefail
exec node "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/bin/virtualme.js" "$@"
