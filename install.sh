#!/usr/bin/env bash

# tzro v2 - The Local Token Shield & Context Optimization Engine
# Zero-dependency, native installer for macOS and Linux

set -euo pipefail

# ANSI color codes
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m' # No Color

echo -e "${BOLD}${CYAN}"
echo "████████╗███████╗██████╗  ██████╗ "
echo "╚══██╔══╝╚══███╔╝██╔══██╗██╔═══██╗"
echo "   ██║     ███╔╝ ██████╔╝██║   ██║"
echo "   ██║    ███╔╝  ██╔══██╗██║   ██║"
echo "   ██║   ███████╗██║  ██║╚██████╔╝"
echo "   ╚═╝   ╚══════╝╚═╝  ╚═╝ ╚═════╝ "
echo -e "   -- The Local Token Shield (v2) --${NC}\n"

# 1. Resolve Installation Directory
INSTALL_DIR="${TZRO_INSTALL_DIR:-$HOME/.tzro}"
echo -e "${BLUE}[1/4] Configuring workspace boundary...${NC}"
echo -e "  Target Install Dir: ${BOLD}${INSTALL_DIR}${NC}"
mkdir -p "${INSTALL_DIR}/bin"

# 2. Detect System Platform and CPU Architecture
echo -e "\n${BLUE}[2/4] Detecting OS and Hardware Architecture...${NC}"
OS="$(uname -s)"
ARCH="$(uname -m)"
echo -e "  Detected OS:   ${BOLD}${OS}${NC}"
echo -e "  Detected Arch: ${BOLD}${ARCH}${NC}"

# 3. Provision Native Binary
echo -e "\n${BLUE}[3/4] Installing Tzro v2 Binary...${NC}"
if [ "${TZRO_MOCK_DOWNLOAD:-false}" = "true" ]; then
    if [ -n "${TZRO_SOURCE_BIN:-}" ] && [ -f "${TZRO_SOURCE_BIN}" ]; then
        cp "${TZRO_SOURCE_BIN}" "${INSTALL_DIR}/bin/tzro"
        chmod +x "${INSTALL_DIR}/bin/tzro"
    else
        echo "#!/bin/sh" > "${INSTALL_DIR}/bin/tzro"
        echo "echo 'mock tzro'" >> "${INSTALL_DIR}/bin/tzro"
        chmod +x "${INSTALL_DIR}/bin/tzro"
    fi
    echo -e "  ${GREEN}✔ [MOCK] Installed tzro binary${NC}"
else
    if command -v go &>/dev/null && [ -f "./cmd/tzro/main.go" ]; then
        echo -e "  ${GREEN}✔ Go compiler detected. Building tzro binary from source...${NC}"
        go build -o "${INSTALL_DIR}/bin/tzro" ./cmd/tzro
        echo -e "  ${GREEN}✔ Built tzro CLI binary${NC}"
    elif [ -f "./bin/tzro" ]; then
        cp "./bin/tzro" "${INSTALL_DIR}/bin/tzro"
        chmod +x "${INSTALL_DIR}/bin/tzro"
        echo -e "  ${GREEN}✔ Installed local tzro binary${NC}"
    else
        echo -e "  ${RED}Error: Go compiler is required to build from source.${NC}"
        exit 1
    fi
fi

if [ "${OS}" = "Darwin" ]; then
    xattr -d com.apple.quarantine "${INSTALL_DIR}/bin/tzro" 2>/dev/null || true
fi

# 4. Configure Agent Lifecycle Hooks
echo -e "\n${BLUE}[4/5] Configuring Agent Lifecycle Hooks (Antigravity, Claude, Hermes, Copilot, Pi-Coder)...${NC}"
if [ -x "${INSTALL_DIR}/bin/tzro" ]; then
    "${INSTALL_DIR}/bin/tzro" init --hooks auto 2>/dev/null || true
fi

# 5. Check Path & Print Dashboard
echo -e "\n${BLUE}[5/5] Checking Pathing Alignment...${NC}"
PATH_OK=false
if [[ ":$PATH:" == *":${INSTALL_DIR}/bin:"* ]]; then
    PATH_OK=true
fi

echo -e "=========================================================="
echo -e "           ${BOLD}${GREEN}✔ TZRO v2 INSTALLATION COMPLETE${NC}"
echo -e "=========================================================="
echo -e "  ${BOLD}Binary Location:${NC}     ${INSTALL_DIR}/bin/tzro"
echo -e "  ${BOLD}Memory Footprint:${NC}    < 50 MB RAM (Zero GPU/ML Bloat)"
echo -e "  ${BOLD}Proxy Target:${NC}        http://127.0.0.1:7878"
echo -e "  ${BOLD}Supported Hooks:${NC}     Antigravity, Claude Code, Hermes, GitHub Copilot, Pi-Coder"
echo -e "=========================================================="

if [ "$PATH_OK" = "true" ]; then
    echo -e "\n  ${GREEN}Awesome! ${INSTALL_DIR}/bin is already in your PATH.${NC}"
else
    echo -e "\n  ${YELLOW}Path Alignment Required:${NC}"
    echo -e "  Please add the following line to ${BOLD}~/.zshrc${NC} or ${BOLD}~/.bashrc${NC}:"
    echo -e "    ${MAGENTA}export PATH=\"\$PATH:${INSTALL_DIR}/bin\"${NC}"
fi

echo -e "\n  ${BOLD}Getting Started:${NC}"
echo -e "    1. Start the Token Shield: ${CYAN}tzro start${NC}"
echo -e "    2. Explore Codebase:      ${CYAN}tzro probe \"<query>\"${NC}"
echo -e "    3. Configure Agent Hooks:  ${CYAN}tzro init --hooks all${NC}"
echo -e "    4. Connect Your Agents:   ${CYAN}export ANTHROPIC_BASE_URL=http://localhost:7878${NC}"
echo -e "                              ${CYAN}export OPENAI_BASE_URL=http://localhost:7878/v1${NC}"
echo -e "==========================================================\n"
