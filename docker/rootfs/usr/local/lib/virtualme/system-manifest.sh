#!/usr/bin/env bash
# Generate image-baked system context for the local agent.
set -euo pipefail

output="${1:-/opt/agent/system-manifest.json}"
mkdir -p "$(dirname "$output")"

# Pure-bash JSON string escaping (spec 012 §4: the image has no Node).
# Handles backslash, double quote, and control characters; version strings
# never contain anything more exotic.
json_string() {
  local input escaped="" char code
  input="$(cat)"
  input="${input#"${input%%[![:space:]]*}"}"
  input="${input%"${input##*[![:space:]]}"}"
  local i
  for (( i = 0; i < ${#input}; i++ )); do
    char="${input:i:1}"
    case "$char" in
      '\') escaped+='\\' ;;
      '"') escaped+='\"' ;;
      $'\n') escaped+='\n' ;;
      $'\r') escaped+='\r' ;;
      $'\t') escaped+='\t' ;;
      *)
        printf -v code '%d' "'$char"
        if (( code < 32 )); then
          printf -v char '\\u%04x' "$code"
        fi
        escaped+="$char"
        ;;
    esac
  done
  printf '"%s"\n' "$escaped"
}

os_release="$(. /etc/os-release && printf '%s %s' "$NAME" "$VERSION")"
chromium_version="$(chromium --version 2>/dev/null || true)"
bash_version="$(bash --version | sed -n '1p')"
xdotool_version="$(xdotool version 2>/dev/null || true)"
scrot_version="$(scrot --version 2>/dev/null | sed -n '1p' || true)"
llama_version="$(/opt/llama/llama-server --version 2>&1 | sed -n '1p' || true)"

cat >"$output" <<EOF
{
  "os": $(printf '%s' "$os_release" | json_string),
  "tools": {
    "chromium": $(printf '%s' "$chromium_version" | json_string),
    "bash": $(printf '%s' "$bash_version" | json_string),
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
