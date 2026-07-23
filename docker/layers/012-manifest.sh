#!/usr/bin/env bash
# Layer 012: bake the local system manifest consumed by the agent.
set -euo pipefail

bash /usr/local/lib/virtualme/system-manifest.sh /opt/agent/system-manifest.json
