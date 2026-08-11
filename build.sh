#!/bin/bash
set -e

# Ensure output directory exists
mkdir -p bin

# Build Dashboard frontend -> static/
echo "==> Building Dashboard frontend..."
(cd dashboard && npm install --silent && npm run build)

# Build MCP progress app -> cmd/tzro-mcp/app/progress.html
echo "==> Building MCP progress app..."
(cd cmd/tzro-mcp && bash build-app.sh)

echo "==> Building tzro CLI (cmd/tzro) -> bin/tzro..."
go build -o bin/tzro ./cmd/tzro

echo "==> Building tzro MCP (cmd/tzro-mcp) -> bin/tzro-mcp..."
go build -o bin/tzro-mcp ./cmd/tzro-mcp

echo "==> Building tzrod daemon (cmd/tzrod) -> bin/tzrod..."
go build -o bin/tzrod ./cmd/tzrod

echo "==> All binaries built successfully!"
