#!/usr/bin/env bash

# tzro native plugin installer script
set -euo pipefail

# Detect if stdin is a terminal. If not (e.g. curl|bash), individual read
# calls must redirect from /dev/tty. We avoid `exec < /dev/tty` because it
# causes a malloc double-free crash in macOS bash 3.2 during exit cleanup.
_STDIN_IS_TTY=true
if [ ! -t 0 ]; then
    _STDIN_IS_TTY=false
fi

# Wrapper: read from /dev/tty when stdin is not a terminal
_tty_read() {
    if [ "${_STDIN_IS_TTY}" = "true" ]; then
        read "$@"
    elif [ -e /dev/tty ]; then
        read "$@" < /dev/tty
    else
        read "$@"
    fi
}

# ANSI color codes for premium aesthetics
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m' # No Color

echo -e "${BOLD}${CYAN}==========================================================${NC}"
echo -e "${BOLD}${CYAN}            tzro Native Plugin Installer                   ${NC}"
echo -e "${BOLD}${CYAN}==========================================================${NC}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")/.." && pwd)"

# Function to install Antigravity IDE plugin
install_antigravity_ide() {
    echo -e "\n${BLUE}[1/3] Installing Antigravity IDE plugin...${NC}"
    
    # Resolve ONE canonical plugins directory.
    # Priority: ~/.gemini/config (where the IDE reads) > antigravity-ide > antigravity > root .gemini
    # Installing to multiple locations causes duplicate plugin loading.
    local target_dir=""
    if [ -d "$HOME/.gemini/config" ]; then
        target_dir="$HOME/.gemini/config/plugins/tzro-plugin"
    elif [ -d "$HOME/.gemini/antigravity-ide" ]; then
        target_dir="$HOME/.gemini/antigravity-ide/plugins/tzro-plugin"
    elif [ -d "$HOME/.gemini/antigravity" ]; then
        target_dir="$HOME/.gemini/antigravity/plugins/tzro-plugin"
    elif [ -d "$HOME/.gemini" ]; then
        target_dir="$HOME/.gemini/plugins/tzro-plugin"
    else
        mkdir -p "$HOME/.gemini/config/plugins"
        target_dir="$HOME/.gemini/config/plugins/tzro-plugin"
    fi

    local target_dirs=("${target_dir}")

    if [ ${#target_dirs[@]} -eq 0 ]; then
        echo -e "  ${YELLOW}⚠ No Antigravity IDE plugins directories found.${NC}"
        echo -e "  Skipping IDE plugin installation."
        return
    fi

    for TARGET_DIR in "${target_dirs[@]}"; do
        if [ -L "${TARGET_DIR}" ] || [ -d "${TARGET_DIR}" ]; then
            echo -e "  ${YELLOW}⚠ IDE plugin already installed at ${TARGET_DIR}${NC}"
            local overwrite=true
            if [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
                _tty_read -p "  Do you want to overwrite it? (y/n) " -n 1 -r
                echo
                if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                    overwrite=false
                fi
            fi
            if [ "$overwrite" = "true" ]; then
                rm -rf "${TARGET_DIR}"
            else
                echo -e "  Skipping IDE plugin installation at ${TARGET_DIR}."
                continue
            fi
        fi

        # Copy plugin files (not symlink — the plugin dir may have different lifecycle)
        mkdir -p "${TARGET_DIR}/skills/tzro"
        mkdir -p "${TARGET_DIR}/references"

        # Check if plugin files exist in the repo
        PLUGIN_SOURCE="${REPO_ROOT}/plugins/antigravity/ide"
        if [ -d "${PLUGIN_SOURCE}" ]; then
            cp -r "${PLUGIN_SOURCE}"/* "${TARGET_DIR}/"
        else
            echo -e "  ${YELLOW}⚠ IDE plugin source not found at ${PLUGIN_SOURCE}${NC}"
            echo -e "  Creating minimal plugin structure..."
            cat > "${TARGET_DIR}/plugin.json" << 'PLUGIN_EOF'
{
  "name": "tzro-plugin",
  "version": "0.1.0",
  "description": "Durable local-first agentic execution engine — delegate complex multi-step workflows, access relational memory, and manage micro-skills via tzro.",
  "author": {
    "name": "tzro"
  },
  "repository": "https://github.com/The18thWarrior/tzro",
  "license": "Apache-2.0"
}
PLUGIN_EOF
        fi

        echo -e "  ${GREEN}✔ IDE plugin installed at: ${BOLD}${TARGET_DIR}${NC}"
    done
}

# Function to install Antigravity SDK plugin
install_antigravity_sdk() {
    echo -e "\n${BLUE}[2/3] Installing Google Antigravity SDK plugin...${NC}"
    echo -e "  The SDK integration module will be symlinked to your Python project."
    echo
    local target_project=""
    if [ "${TZRO_NON_INTERACTIVE:-}" = "true" ]; then
        if [ -n "${TZRO_SDK_PROJECT:-}" ]; then
            target_project="${TZRO_SDK_PROJECT}"
        else
            echo -e "  ${YELLOW}⚠ Skipping SDK plugin installation (no TZRO_SDK_PROJECT specified in non-interactive mode).${NC}"
            return
        fi
    else
        _tty_read -p "  Enter the absolute path to your Python project directory: " target_project
    fi
    
    # Resolve tilde
    TARGET_PROJECT="${target_project/#\~/$HOME}"
    
    if [ ! -d "${TARGET_PROJECT}" ]; then
        echo -e "  ${YELLOW}⚠ Target directory '${TARGET_PROJECT}' does not exist.${NC}"
        local create_dir=true
        if [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
            _tty_read -p "  Do you want to create it? (y/n) " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                create_dir=false
            fi
        fi
        if [ "$create_dir" = "true" ]; then
            mkdir -p "${TARGET_PROJECT}"
        else
            echo -e "  Skipping SDK plugin installation."
            return
        fi
    fi
    
    # Symlink the self-contained plugin package
    TARGET_TZRO="${TARGET_PROJECT}/tzro_plugin"
    
    if [ -L "${TARGET_TZRO}" ] || [ -d "${TARGET_TZRO}" ]; then
        rm -rf "${TARGET_TZRO}"
    fi
    
    ln -sf "${REPO_ROOT}/plugins/antigravity/tzro" "${TARGET_TZRO}"
    
    echo -e "  ${GREEN}✔ SDK plugin symlinked to: ${BOLD}${TARGET_TZRO}${NC}"
    echo -e "\n  ${BOLD}Usage:${NC}"
    echo -e "  --------------------------------------------------------"
    echo -e "  ${CYAN}from google.antigravity import Agent, LocalAgentConfig"
    echo -e "  from tzro_plugin.connection import TzroConnection"
    echo -e "  from tzro_plugin.agent import register_tzro_tools"
    echo -e ""
    echo -e "  config = LocalAgentConfig("
    echo -e "      connection=TzroConnection(),"
    echo -e "  )"
    echo -e "  register_tzro_tools(config)"
    echo -e ""
    echo -e "  async with Agent(config=config) as agent:"
    echo -e "      result = await agent.chat(\"Sync my HubSpot leads to SQL database\")"
    echo -e "      print(result)${NC}"
    echo -e "  --------------------------------------------------------"
}

# Function to install Hermes Agent plugin
install_hermes() {
    echo -e "\n${BLUE}[3/3] Installing Hermes Agent plugin...${NC}"
    HERMES_PLUGINS_DIR="$HOME/.hermes/plugins"

    if [ ! -d "${HERMES_PLUGINS_DIR}" ]; then
        echo -e "  ${YELLOW}⚠ Hermes plugins directory not found at ${HERMES_PLUGINS_DIR}${NC}"
        local create_dir=true
        if [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
            _tty_read -p "  Do you want to create it? (y/n) " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                create_dir=false
            fi
        fi
        if [ "$create_dir" = "true" ]; then
            mkdir -p "${HERMES_PLUGINS_DIR}"
        else
            echo -e "  Skipping Hermes plugin installation."
            return
        fi
    fi

    TARGET_TZRO="${HERMES_PLUGINS_DIR}/tzro"

    if [ -L "${TARGET_TZRO}" ] || [ -d "${TARGET_TZRO}" ]; then
        echo -e "  ${YELLOW}⚠ Hermes plugin already installed at ${TARGET_TZRO}${NC}"
        local overwrite=true
        if [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
            _tty_read -p "  Do you want to overwrite it? (y/n) " -n 1 -r
            echo
            if [[ ! $REPLY =~ ^[Yy]$ ]]; then
                overwrite=false
            fi
        fi
        if [ "$overwrite" = "true" ]; then
            rm -rf "${TARGET_TZRO}"
        else
            echo -e "  Skipping Hermes plugin installation."
            return
        fi
    fi

    # Symlink the plugin package (live-linked to repo)
    ln -sf "${REPO_ROOT}/plugins/hermes/tzro" "${TARGET_TZRO}"

    echo -e "  ${GREEN}✔ Hermes plugin symlinked to: ${BOLD}${TARGET_TZRO}${NC}"
    echo -e "\n  ${BOLD}Usage:${NC}"
    echo -e "  --------------------------------------------------------"
    echo -e "  ${CYAN}# In your Hermes plugin config or startup script:"
    echo -e "  from tzro import register"
    echo -e ""
    echo -e "  plugin = register(ctx)"
    echo -e ""
    echo -e "  # Or manually:"
    echo -e "  from tzro.plugin import TzroPlugin"
    echo -e ""
    echo -e "  plugin = TzroPlugin()"
    echo -e "  plugin.on_load(ctx)${NC}"
    echo -e "  --------------------------------------------------------"
}

# Main installer flow — IDE plugin always installs
install_antigravity_ide

# SDK and Hermes are opt-in (advanced integrations)
INSTALL_SDK="${TZRO_INSTALL_SDK:-false}"
INSTALL_HERMES="${TZRO_INSTALL_HERMES:-false}"

if [ "${INSTALL_SDK}" != "true" ] && [ "${INSTALL_HERMES}" != "true" ] && [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
    echo
    echo -e "  ${BOLD}Optional integrations:${NC}"
    echo -e "  ${DIM}These are for developers who want to use tzro from Python or Hermes agents.${NC}"
    echo -e "  ${DIM}You can always run this installer again later.${NC}"
    echo
    _tty_read -p "  Install SDK + Hermes plugins? (y/N) " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        INSTALL_SDK=true
        INSTALL_HERMES=true
    fi
fi

if [ "${INSTALL_SDK}" = "true" ]; then
    install_antigravity_sdk
fi

if [ "${INSTALL_HERMES}" = "true" ]; then
    install_hermes
fi

echo -e "\n${BOLD}${GREEN}==========================================================${NC}"
echo -e "${BOLD}${GREEN}✔ PLUGIN INSTALLATION COMPLETE${NC}"
echo -e "${BOLD}${GREEN}==========================================================${NC}\n"
