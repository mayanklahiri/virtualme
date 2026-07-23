#!/usr/bin/env bash
# Deterministic, network-free runtime-locality and persistence-map checks.
set -euo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

rc=0

runtime_files() {
  git ls-files 'controller/**/*.go' 'docker/rootfs/**' 2>/dev/null \
    | grep -v '_test\.go$' || true
}

spa_files() {
  git ls-files 'controller/web/static/js/**' 'controller/web/static/css/**' \
    2>/dev/null || true
}

report_matches() {
  local label="$1"
  local pattern="$2"
  local file="$3"
  local matches

  matches="$(grep -nEI "$pattern" "$file" 2>/dev/null || true)"
  if [[ -n "$matches" ]]; then
    while IFS= read -r match; do
      printf 'locality: %s: %s:%s\n' "$label" "$file" "$match" >&2
    done <<<"$matches"
    rc=1
  fi
}

bad_hosts='openai\.com|openrouter\.ai|anthropic\.com|generativelanguage|googleapis\.com|api\.mistral|api\.groq|api\.together'
bad_keys='OPENAI_API_KEY|ANTHROPIC_API_KEY|OPENROUTER_API_KEY|GEMINI_API_KEY|GROQ_API_KEY|(^|[^[:alnum:]_])HF_TOKEN([^[:alnum:]_]|$)'
llm_surfaces='/v1/chat/completions|/v1/completions|/completion|/slots|/health|/props|llama|VM_LLAMA'

while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  report_matches "external LLM host" "$bad_hosts" "$file"
  report_matches "provider API key env" "$bad_keys" "$file"

  while IFS=: read -r line_number line; do
    [[ -n "$line_number" ]] || continue
    remainder="$line"
    while [[ "$remainder" =~ (https?://[^][[:space:]\"\']+) ]]; do
      url="${BASH_REMATCH[1]}"
      remainder="${remainder#*"${BASH_REMATCH[1]}"}"
      if [[ ! "$url" =~ ^https?://(127\.0\.0\.1|localhost)(:|/|$) ]]; then
        printf 'locality: non-loopback LLM URL: %s:%s:%s\n' \
          "$file" "$line_number" "$line" >&2
        rc=1
      fi
    done
  done < <(grep -nEI "$llm_surfaces" "$file" 2>/dev/null || true)
done < <(runtime_files)

# Strip JavaScript/CSS comments while retaining strings, then reject external
# origins in executable/style content. The scanner is deterministic and does
# not inspect generated or untracked files.
while IFS= read -r file; do
  [[ -n "$file" ]] || continue
  matches="$(
    awk '
      BEGIN { block = 0; quote = ""; escaped = 0 }
      {
        output = ""
        for (i = 1; i <= length($0); i++) {
          c = substr($0, i, 1)
          n = substr($0, i + 1, 1)
          if (block) {
            if (c == "*" && n == "/") { block = 0; i++ }
            continue
          }
          if (quote != "") {
            output = output c
            if (escaped) { escaped = 0; continue }
            if (c == "\\") { escaped = 1; continue }
            if (c == quote) { quote = "" }
            continue
          }
          if (c == "\"" || c == "\047" || c == "`") {
            quote = c
            output = output c
            continue
          }
          if (c == "/" && n == "*") { block = 1; i++; continue }
          if (c == "/" && n == "/") { break }
          output = output c
        }
        if (output ~ /https?:\/\//) { print NR ":" output }
      }
    ' "$file"
  )"
  if [[ -n "$matches" ]]; then
    while IFS= read -r match; do
      printf 'locality: external SPA origin: %s:%s\n' "$file" "$match" >&2
    done <<<"$matches"
    rc=1
  fi
done < <(spa_files)

data_dirs='docker/rootfs/etc/cont-init.d/10-data-dirs.sh'
required_paths=(
  valkey
  chromium
  xdg/config
  xdg/cache
  xdg/data
  metrics
  agent
  mail
)
for path in "${required_paths[@]}"; do
  if ! grep -Fq "\$VM_DATA_DIR/$path" "$data_dirs"; then
    printf 'locality: persistent path missing from %s: %s\n' \
      "$data_dirs" "$path" >&2
    rc=1
  fi
done

if [[ "$rc" -eq 0 ]]; then
  echo "locality: OK"
fi
exit "$rc"
