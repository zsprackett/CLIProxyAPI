#!/usr/bin/env bash
# Refresh the embedded LiteLLM price snapshot from upstream.
# Usage: scripts/update-model-prices.sh [url]
#   url  LiteLLM model_prices_and_context_window.json (default: BerriAI/litellm main on GitHub)
# Writes internal/usagehistory/prices/litellm_trimmed.json. The old file stays if any step fails.
set -euo pipefail

repo="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
url="${1:-https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json}"
out="$repo/internal/usagehistory/prices/litellm_trimmed.json"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL --max-time 120 -o "$tmp/litellm.json" "$url"
(cd "$repo" && go run ./cmd/trim-prices < "$tmp/litellm.json" > "$tmp/trimmed.json")
mv "$tmp/trimmed.json" "$out"
echo "update-model-prices: wrote $out ($(wc -c < "$out") bytes)"
