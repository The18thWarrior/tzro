#!/usr/bin/env bash
# Build the optional native decision worker against an installed libllama.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-"${HERE}/bin/jev-score"}"

if [ -z "${LLAMA_DIR:-}" ]; then
    for prefix in /opt/homebrew /usr/local; do
        if [ -f "${prefix}/opt/llama.cpp/include/llama.h" ]; then
            LLAMA_DIR="${prefix}/opt/llama.cpp"
            break
        fi
    done
fi
if [ -z "${LLAMA_DIR:-}" ] || [ ! -f "${LLAMA_DIR}/include/llama.h" ]; then
    echo "Error: libllama headers not found. Install llama.cpp or set LLAMA_DIR to its installed prefix." >&2
    exit 1
fi

flags=(-I"${LLAMA_DIR}/include" -L"${LLAMA_DIR}/lib" -Wl,-rpath,"${LLAMA_DIR}/lib")
# Homebrew packages ggml separately. Custom installs can specify GGML_DIR.
for prefix in /opt/homebrew /usr/local; do
    if [[ "$LLAMA_DIR" == "$prefix/"* ]]; then
        flags+=(-I"${prefix}/include" -L"${prefix}/lib" -Wl,-rpath,"${prefix}/lib")
    fi
done
if [ -n "${GGML_DIR:-}" ]; then
    flags+=(-I"${GGML_DIR}/include" -L"${GGML_DIR}/lib" -Wl,-rpath,"${GGML_DIR}/lib")
fi
mkdir -p "$(dirname "$OUT")"
build_output="${OUT}.tmp.$$"
trap 'rm -f "$build_output"' EXIT
"${CXX:-c++}" -std=c++17 -O3 -Wall -Wextra "${flags[@]}" \
    "${HERE}/cmd/jev-score/main.cpp" -o "$build_output" -lllama
mv "$build_output" "$OUT"
echo "Built ${OUT} against ${LLAMA_DIR}"
