#!/bin/bash
set -e

# Ensure output directory exists
mkdir -p bin

echo "==> Building Tzro v2 (cmd/tzro) -> bin/tzro..."
go build -o bin/tzro ./cmd/tzro

echo "==> Tzro v2 binary built successfully at bin/tzro!"
