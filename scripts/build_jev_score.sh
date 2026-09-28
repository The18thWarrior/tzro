#!/usr/bin/env bash
# Build jev-score binary against libllama (macOS Metal / Linux CUDA/CPU)
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-"${HERE}/bin/jev-score"}"

mkdir -p "$(dirname "$OUT")"

# Check for Homebrew llama.cpp on macOS
if [ -d "/opt/homebrew/Cellar/llama.cpp" ]; then
    LLAMA_DIR="$(find /opt/homebrew/Cellar/llama.cpp -maxdepth 1 -mindepth 1 | sort -V | tail -n 1)"
    echo "Found Homebrew llama.cpp at ${LLAMA_DIR}"
    c++ -std=c++17 -O3 -Wall \
        -I"${LLAMA_DIR}/include" -I/opt/homebrew/include \
        "${HERE}/cmd/jev-score/main.cpp" -o "$OUT" \
        -L"${LLAMA_DIR}/lib" -L/opt/homebrew/lib -lllama -Wl,-rpath,"${LLAMA_DIR}/lib" -Wl,-rpath,"/opt/homebrew/lib"
    echo "Successfully built ${OUT}"
    exit 0
fi

# Fallback: check LLAMA_DIR from environment
if [ -n "${LLAMA_DIR:-}" ] && [ -f "${LLAMA_DIR}/include/llama.h" ]; then
    c++ -std=c++17 -O3 -Wall \
        -I"${LLAMA_DIR}/include" \
        "${HERE}/cmd/jev-score/main.cpp" -o "$OUT" \
        -L"${LLAMA_DIR}/lib" -lllama -Wl,-rpath,"${LLAMA_DIR}/lib"
    echo "Successfully built ${OUT}"
    exit 0
fi

echo "Error: llama.cpp not found. Install via 'brew install llama.cpp' or set LLAMA_DIR."
exit 1
