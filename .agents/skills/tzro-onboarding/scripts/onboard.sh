#!/usr/bin/env bash

# Fully automated non-interactive tzro bootstrapper and editor integrator

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
INSTALL_DIR="${TZRO_INSTALL_DIR:-$HOME/.tzro}"

echo "========================================="
echo "Starting Automated tzro Onboarding..."
echo "========================================="

# 1. Run the main installer with pre-seeded inputs for non-interactive execution
# Inputs sequence:
# - 'y' to run the MCP installer
# - '' (Enter) to accept detected editors
# - 'y' to overwrite existing MCP configs if prompted
# - 'y' to create/append AGENTS.md instructions if prompted
echo "Running tzro installer..."
export TZRO_NON_INTERACTIVE=true
bash "${REPO_ROOT}/install.sh"

# 2. Automatically configure environment PATH if needed
PATH_LINE="export PATH=\"\$PATH:${INSTALL_DIR}/bin\""
if [[ ":$PATH:" != *":${INSTALL_DIR}/bin:"* ]]; then
    # Detect active shell config file
    SHELL_CONFIG=""
    CURRENT_SHELL="$(basename "${SHELL:-}")"
    
    if [ "${CURRENT_SHELL}" = "zsh" ] || [ -f "$HOME/.zshrc" ]; then
        SHELL_CONFIG="$HOME/.zshrc"
    elif [ "${CURRENT_SHELL}" = "bash" ] || [ -f "$HOME/.bashrc" ]; then
        SHELL_CONFIG="$HOME/.bashrc"
    else
        SHELL_CONFIG="$HOME/.profile"
    fi

    if [ -n "${SHELL_CONFIG}" ]; then
        # Check if already in file
        if ! grep -q "${INSTALL_DIR}/bin" "${SHELL_CONFIG}" 2>/dev/null; then
            echo -e "\n# tzro local-first engine binary path" >> "${SHELL_CONFIG}"
            echo "${PATH_LINE}" >> "${SHELL_CONFIG}"
            echo "✔ Added tzro to PATH in ${SHELL_CONFIG}"
        else
            echo "✔ tzro path export already configured in ${SHELL_CONFIG}"
        fi
    fi
fi

# 3. Ensure IDE plugin is installed
echo "Configuring Antigravity IDE and Hermes plugins..."
export TZRO_NON_INTERACTIVE=true
bash "${REPO_ROOT}/plugins/install_plugins.sh"

echo "========================================="
echo "✔ Automated tzro Onboarding Complete!"
echo "Please restart your terminal or run:"
echo "  source ~/.zshrc (or your shell config)"
echo "========================================="
