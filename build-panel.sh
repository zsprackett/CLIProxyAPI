#!/usr/bin/env bash
# Build the Management Center UI in web/management-center into one HTML file.
# Usage: ./build-panel.sh [output path, default static/management.html]
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ui="$root/web/management-center"
out="${1:-$root/static/management.html}"

(cd "$ui" && bun install --frozen-lockfile && bun run build)
mkdir -p "$(dirname "$out")"
# Copy then rename, so a running server never serves a half-written file.
cp "$ui/dist/index.html" "$out.tmp"
mv "$out.tmp" "$out"
echo "built $out"
