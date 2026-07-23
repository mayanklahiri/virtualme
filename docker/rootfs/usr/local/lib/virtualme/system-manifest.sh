#!/usr/bin/env bash
# Generate image-baked system context for the local agent.
set -euo pipefail

output="${1:-/opt/agent/system-manifest.json}"
mkdir -p "$(dirname "$output")"

json_string() {
  node -e 'let s="";process.stdin.on("data",d=>s+=d);process.stdin.on("end",()=>console.log(JSON.stringify(s.trim())))'
}

os_release="$(. /etc/os-release && printf '%s %s' "$NAME" "$VERSION")"
chromium_version="$(chromium --version 2>/dev/null || true)"
bash_version="$(bash --version | sed -n '1p')"
node_version="$(node --version 2>/dev/null || true)"
xdotool_version="$(xdotool version 2>/dev/null || true)"
scrot_version="$(scrot --version 2>/dev/null | sed -n '1p' || true)"
llama_version="$(/opt/llama/llama-server --version 2>&1 | sed -n '1p' || true)"

cat >"$output" <<EOF
{
  "os": $(printf '%s' "$os_release" | json_string),
  "tools": {
    "chromium": $(printf '%s' "$chromium_version" | json_string),
    "bash": $(printf '%s' "$bash_version" | json_string),
    "node": $(printf '%s' "$node_version" | json_string),
    "xdotool": $(printf '%s' "$xdotool_version" | json_string),
    "scrot": $(printf '%s' "$scrot_version" | json_string),
    "llama": $(printf '%s' "$llama_version" | json_string)
  },
  "paths": {
    "data": "/home/virtualme/.virtualme",
    "agent": "/home/virtualme/.virtualme/agent",
    "models": "/opt/models",
    "runtime": "/opt/llama"
  },
  "display": ":99",
  "resolution": "1600x900x24"
}
EOF
chmod 0444 "$output"
