#!/usr/bin/env bash
# Build the Vite-based MCP app and copy the output to the Go embed path.
set -euo pipefail
cd "$(dirname "$0")/app-src"

# Install dependencies if node_modules is missing
if [ ! -d "node_modules" ]; then
  npm install --no-audit --no-fund
fi

# Build using the build:only script (skip tsc for speed in go generate)
npx vite build

# Copy to the Go embed path
cp dist/index.html ../app/progress.html
echo "✓ app/progress.html updated"
