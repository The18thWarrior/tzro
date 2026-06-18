#!/usr/bin/env bash

# tzro — Generic MCP + Agent Skills Installer
# Auto-detects agentic interfaces and injects MCP server config + skill instructions.
#
# Supported interfaces:
#   Claude Code, Cursor, Windsurf, GitHub Copilot, OpenCode, Antigravity IDE
#
# Usage:
#   bash plugins/install_mcp.sh              # interactive mode
#   TZRO_DRY_RUN=true bash plugins/install_mcp.sh  # preview without modifying files

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

# ---------------------------------------------------------------------------
# ANSI colors
# ---------------------------------------------------------------------------
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'

DRY_RUN="${TZRO_DRY_RUN:-false}"

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")/.." && pwd)"
REPLACE_BLOCK="${REPO_ROOT}/plugins/replace_block.sh"
INSTRUCTION_MARKER="TZRO INSTRUCTIONS"

# Default instructions source — updated by tier selection step
INSTRUCTIONS_SOURCE="${REPO_ROOT}/plugins/mcp/tzro-agent-instructions.md"

# Interface registry (parallel arrays — bash 3.2 compatible)
IFACE_IDS=(   claude        cursor        windsurf      copilot          opencode      antigravity)
IFACE_NAMES=("Claude Code" "Cursor"      "Windsurf"    "GitHub Copilot" "OpenCode"    "Gemini / Antigravity IDE")
IFACE_DETECTED=(false false false false false false)
IFACE_SELECTED=(false false false false false false)

# ---------------------------------------------------------------------------
# Banner
# ---------------------------------------------------------------------------
echo -e "${BOLD}${CYAN}"
echo "  ┌─────────────────────────────────────────────────────┐"
echo "  │         tzro MCP + Agent Skills Installer           │"
echo "  │     Wire up durable execution in any AI editor      │"
echo "  └─────────────────────────────────────────────────────┘"
echo -e "${NC}"

if [ "${DRY_RUN}" = "true" ]; then
    echo -e "  ${YELLOW}${BOLD}⚡ DRY RUN MODE — no files will be modified${NC}\n"
fi

# ---------------------------------------------------------------------------
# Step 1: Resolve tzro-mcp binary
# ---------------------------------------------------------------------------
echo -e "${BLUE}[1/5] Resolving tzro-mcp binary...${NC}"

TZRO_MCP_BIN=""
CANDIDATES=(
    "${TZRO_MCP_PATH:-}"
    "${REPO_ROOT}/bin/tzro-mcp"
    "${REPO_ROOT}/tzro-mcp"
    "$HOME/.tzro/bin/tzro-mcp"
)

for candidate in "${CANDIDATES[@]}"; do
    if [ -n "${candidate}" ] && [ -f "${candidate}" ] && [ -x "${candidate}" ]; then
        TZRO_MCP_BIN="${candidate}"
        break
    fi
done

if [ -z "${TZRO_MCP_BIN}" ]; then
    echo -e "  ${YELLOW}⚠ Could not auto-detect tzro-mcp binary.${NC}"
    if [ "${TZRO_NON_INTERACTIVE:-}" = "true" ]; then
        echo -e "  ${RED}Error: Cannot auto-detect tzro-mcp binary in non-interactive mode.${NC}"
        exit 1
    fi
    _tty_read -p "  Enter the absolute path to tzro-mcp: " TZRO_MCP_BIN
    TZRO_MCP_BIN="${TZRO_MCP_BIN/#\~/$HOME}"
    if [ ! -f "${TZRO_MCP_BIN}" ]; then
        echo -e "  ${RED}Error: '${TZRO_MCP_BIN}' does not exist.${NC}"
        exit 1
    fi
fi

echo -e "  ${GREEN}✔ Binary: ${BOLD}${TZRO_MCP_BIN}${NC}"

# ---------------------------------------------------------------------------
# Step 2: Check jq
# ---------------------------------------------------------------------------
echo -e "\n${BLUE}[2/5] Checking dependencies...${NC}"

HAS_JQ=false
if command -v jq &>/dev/null; then
    HAS_JQ=true
    echo -e "  ${GREEN}✔ jq found${NC}"
else
    echo -e "  ${YELLOW}⚠ jq not found — JSON config injection requires jq.${NC}"
    echo -e "  ${DIM}Install: brew install jq (macOS) or apt install jq (Linux)${NC}"
    echo -e "  ${DIM}Continuing with manual instruction output for config files.${NC}"
fi

# ---------------------------------------------------------------------------
# Step 3: Auto-detect interfaces
# ---------------------------------------------------------------------------
echo -e "\n${BLUE}[3/5] Detecting agentic interfaces...${NC}"

# Claude Code
if [ -f "$HOME/.claude.json" ] || [ -d "$HOME/.claude" ]; then
    IFACE_DETECTED[0]=true
fi

# Cursor
if [ -d "$HOME/.cursor" ]; then
    IFACE_DETECTED[1]=true
fi

# Windsurf
if [ -d "$HOME/.codeium/windsurf" ] || [ -d "$HOME/.windsurf" ]; then
    IFACE_DETECTED[2]=true
fi

# GitHub Copilot
if [ -d "$HOME/.vscode/extensions" ] || [ -d "$HOME/.vscode-insiders/extensions" ]; then
    IFACE_DETECTED[3]=true
fi

# OpenCode
if [ -d "$HOME/.config/opencode" ] || [ -f "$HOME/.opencode.json" ]; then
    IFACE_DETECTED[4]=true
fi

# Gemini CLI / Antigravity IDE
if [ -d "$HOME/.gemini" ] || [ -d "$HOME/.gemini/config" ] || [ -d "$HOME/.gemini/antigravity" ] || [ -d "$HOME/.gemini/antigravity-ide" ]; then
    IFACE_DETECTED[5]=true
fi

# Display detection results
for i in "${!IFACE_IDS[@]}"; do
    if [ "${IFACE_DETECTED[$i]}" = "true" ]; then
        echo -e "  ${GREEN}✔ ${IFACE_NAMES[$i]}${NC}"
    else
        echo -e "  ${DIM}○ ${IFACE_NAMES[$i]} (not detected)${NC}"
    fi
done

# Ask which to install
echo ""
echo -e "  ${BOLD}Select interfaces to configure:${NC}"
echo -e "  ${DIM}Press Enter to accept detected defaults, or type numbers to select${NC}"
echo ""

for i in "${!IFACE_IDS[@]}"; do
    num=$((i + 1))
    if [ "${IFACE_DETECTED[$i]}" = "true" ]; then
        echo -e "  ${GREEN}✔${NC} ${num}) ${IFACE_NAMES[$i]}"
    else
        echo -e "    ${num}) ${IFACE_NAMES[$i]}"
    fi
done

echo ""
if [ "${TZRO_NON_INTERACTIVE:-}" = "true" ]; then
    SELECTION_INPUT=""
else
    _tty_read -p "  Enter numbers (e.g., 1 2 3) or press Enter for detected: " SELECTION_INPUT
fi

# Build selected list
if [ -z "${SELECTION_INPUT}" ]; then
    # Use detected interfaces
    for i in "${!IFACE_IDS[@]}"; do
        IFACE_SELECTED[$i]="${IFACE_DETECTED[$i]}"
    done
else
    # Parse user selections
    for sel in ${SELECTION_INPUT}; do
        idx=$((sel - 1))
        if [ "${idx}" -ge 0 ] && [ "${idx}" -lt "${#IFACE_IDS[@]}" ]; then
            IFACE_SELECTED[$idx]=true
        fi
    done
fi

# Count selections
SELECTED_COUNT=0
for i in "${!IFACE_IDS[@]}"; do
    if [ "${IFACE_SELECTED[$i]}" = "true" ]; then
        SELECTED_COUNT=$((SELECTED_COUNT + 1))
    fi
done

if [ "${SELECTED_COUNT}" -eq 0 ]; then
    echo -e "\n  ${YELLOW}No interfaces selected. Skipping to universal fallback.${NC}"
fi

# ---------------------------------------------------------------------------
# Step 3.5: Integration Tier Selection
# ---------------------------------------------------------------------------
echo -e "\n${BLUE}[3.5/5] Selecting integration tier...${NC}"
echo -e "  ${DIM}Controls how aggressively agents delegate work to tzro.${NC}"
echo ""

TIER=""
if [ "${TZRO_NON_INTERACTIVE:-}" = "true" ]; then
    TIER="${TZRO_DELEGATION_TIER:-balanced}"
    echo -e "  ${GREEN}✔ Non-interactive: using '${TIER}' tier${NC}"
else
    echo -e "  ${BOLD}Select integration tier:${NC}"
    echo ""
    echo -e "    1) ${BOLD}Conservative${NC}"
    echo -e "       ${DIM}Delegate only for significant cost savings on bulk operations.${NC}"
    echo -e "       ${DIM}Triggers at 8+ sequential tool calls. Softer 'may delegate' language.${NC}"
    echo ""
    echo -e "    2) ${BOLD}Balanced${NC} ${GREEN}(recommended)${NC}"
    echo -e "       ${DIM}Delegate when task doesn't require frontier model reasoning.${NC}"
    echo -e "       ${DIM}Triggers at 3+ sequential tool calls. 'Must delegate' for known patterns.${NC}"
    echo ""
    echo -e "    3) ${BOLD}Aggressive${NC}"
    echo -e "       ${DIM}Route everything possible through tzro for maximum local execution.${NC}"
    echo -e "       ${DIM}Triggers at 2+ sequential tool calls. 'MUST delegate ALL' multi-step tasks.${NC}"
    echo ""
    _tty_read -p "  Enter 1-3 or press Enter for [2]: " TIER_INPUT
    case "${TIER_INPUT}" in
        1) TIER="conservative" ;;
        3) TIER="aggressive" ;;
        *) TIER="balanced" ;;
    esac
fi

# Validate tier value
case "${TIER}" in
    conservative|balanced|aggressive) ;;
    *)
        echo -e "  ${YELLOW}⚠ Unknown tier '${TIER}', defaulting to balanced${NC}"
        TIER="balanced"
        ;;
esac

echo -e "  ${GREEN}✔ Integration tier: ${BOLD}${TIER}${NC}"

# Set instructions source based on tier
if [ "${TIER}" = "balanced" ]; then
    INSTRUCTIONS_SOURCE="${REPO_ROOT}/plugins/mcp/tzro-agent-instructions.md"
else
    INSTRUCTIONS_SOURCE="${REPO_ROOT}/plugins/mcp/tzro-agent-instructions-${TIER}.md"
fi

if [ ! -f "${INSTRUCTIONS_SOURCE}" ]; then
    echo -e "  ${YELLOW}⚠ Tier file not found: ${INSTRUCTIONS_SOURCE}${NC}"
    echo -e "  ${YELLOW}  Falling back to balanced instructions${NC}"
    INSTRUCTIONS_SOURCE="${REPO_ROOT}/plugins/mcp/tzro-agent-instructions.md"
    TIER="balanced"
fi

# Write delegationMode to config.json
INSTALL_DIR="${TZRO_INSTALL_DIR:-$HOME/.tzro}"
TZRO_CONFIG="${INSTALL_DIR}/config.json"
if [ "${HAS_JQ}" = "true" ] && [ "${DRY_RUN}" != "true" ]; then
    if [ -f "${TZRO_CONFIG}" ]; then
        local_tmp="${TZRO_CONFIG}.tzro_tier_tmp"
        jq --arg mode "${TIER}" '.delegationMode = $mode' "${TZRO_CONFIG}" > "${local_tmp}" && mv "${local_tmp}" "${TZRO_CONFIG}"
        echo -e "  ${GREEN}✔ Set delegationMode='${TIER}' in config.json${NC}"
    else
        echo -e "  ${DIM}config.json not found at ${TZRO_CONFIG} — delegationMode will be set on next config write${NC}"
    fi
elif [ "${DRY_RUN}" = "true" ]; then
    echo -e "  ${DIM}[DRY RUN] Would set delegationMode='${TIER}' in config.json${NC}"
else
    echo -e "  ${DIM}jq not available — manually set \"delegationMode\": \"${TIER}\" in ${TZRO_CONFIG}${NC}"
fi

# ---------------------------------------------------------------------------
# Helper: inject MCP config into a JSON file
# ---------------------------------------------------------------------------
inject_mcp_config() {
    local config_file="$1"
    local json_key="$2"  # e.g., "mcpServers" or "servers"
    local iface_name="$3"

    if [ "${HAS_JQ}" != "true" ]; then
        echo -e "    ${YELLOW}⚠ jq required for JSON injection. Manual config:${NC}"
        echo -e "    ${DIM}Add to ${config_file} under \"${json_key}\":${NC}"
        echo -e "    ${CYAN}\"tzro\": { \"command\": \"${TZRO_MCP_BIN}\", \"args\": [] }${NC}"
        return
    fi

    if [ "${DRY_RUN}" = "true" ]; then
        echo -e "    ${DIM}[DRY RUN] Would inject tzro into ${config_file} → ${json_key}${NC}"
        return
    fi

    # Create config file if it doesn't exist
    if [ ! -f "${config_file}" ]; then
        mkdir -p "$(dirname "${config_file}")"
        echo '{}' > "${config_file}"
    fi

    # Check if tzro entry already exists
    local existing
    existing=$(jq -r ".${json_key}.tzro // empty" "${config_file}" 2>/dev/null || true)
    if [ -n "${existing}" ]; then
        echo -e "    ${DIM}Updating existing tzro entry in ${config_file}${NC}"
    fi

    # Build env object
    local tzro_dir_val="${REPO_ROOT}"
    if [ -n "${TZRO_REPO_ROOT:-}" ] && [ -f "${TZRO_REPO_ROOT}/install.sh" ]; then
        tzro_dir_val="${TZRO_REPO_ROOT}"
    fi

    local env_json=""
    if [ "${iface_name}" = "Antigravity IDE" ]; then
        env_json='{"TZRO_DIR": "'"${tzro_dir_val}"'", "ANTIGRAVITY_AGENT": "$ANTIGRAVITY_AGENT", "ANTIGRAVITY_TRAJECTORY_ID": "$ANTIGRAVITY_TRAJECTORY_ID", "ANTIGRAVITY_LS_ADDRESS": "$ANTIGRAVITY_LS_ADDRESS", "ANTIGRAVITY_CSRF_TOKEN": "$ANTIGRAVITY_CSRF_TOKEN"}'
    else
        env_json='{"TZRO_DIR": "'"${tzro_dir_val}"'"}'
    fi

    # Inject the tzro MCP server entry
    local tmp_file="${config_file}.tzro_tmp"
    if jq --arg cmd "${TZRO_MCP_BIN}" \
       --argjson env "${env_json}" \
       ".${json_key}.tzro = {\"command\": \$cmd, \"args\": [], \"env\": \$env}" \
       "${config_file}" > "${tmp_file}" 2>/dev/null; then
        mv "${tmp_file}" "${config_file}"
        # Verify the write actually persisted (detect external overwrites)
        local verify
        verify=$(jq -r ".${json_key}.tzro.command // empty" "${config_file}" 2>/dev/null || true)
        if [ -n "${verify}" ]; then
            echo -e "    ${GREEN}✔ MCP config injected into ${config_file}${NC}"
        else
            echo -e "    ${RED}✘ Write succeeded but verification failed — file was overwritten externally${NC}"
            echo -e "    ${DIM}Current contents of ${config_file}:${NC}"
            cat "${config_file}" 2>/dev/null | head -10 | while IFS= read -r line; do
                echo -e "    ${DIM}  ${line}${NC}"
            done
            echo -e "    ${YELLOW}Hint: An IDE or editor may be watching this file and resetting it.${NC}"
            echo -e "    ${YELLOW}Try closing Antigravity IDE / Gemini CLI before running the installer.${NC}"
        fi
    else
        rm -f "${tmp_file}"
        echo -e "    ${RED}✘ Failed to inject MCP config into ${config_file}${NC}"
        echo -e "    ${DIM}The file may contain invalid JSON. Contents:${NC}"
        head -5 "${config_file}" 2>/dev/null | while IFS= read -r line; do
            echo -e "    ${DIM}  ${line}${NC}"
        done
        echo -e "    ${YELLOW}Creating fresh config file and retrying...${NC}"
        echo '{}' > "${config_file}"
        if jq --arg cmd "${TZRO_MCP_BIN}" \
           --argjson env "${env_json}" \
           ".${json_key}.tzro = {\"command\": \$cmd, \"args\": [], \"env\": \$env}" \
           "${config_file}" > "${tmp_file}" 2>/dev/null; then
            mv "${tmp_file}" "${config_file}"
            echo -e "    ${GREEN}✔ MCP config injected into ${config_file} (after reset)${NC}"
        else
            rm -f "${tmp_file}"
            echo -e "    ${RED}✘ Retry also failed. Skipping ${config_file}${NC}"
        fi
    fi
}

# ---------------------------------------------------------------------------
# Helper: write instruction file
# ---------------------------------------------------------------------------
write_instructions() {
    local target_file="$1"
    local header="${2:-}"  # optional frontmatter/header to prepend

    if [ "${DRY_RUN}" = "true" ]; then
        echo -e "    ${DIM}[DRY RUN] Would write instructions to ${target_file}${NC}"
        return
    fi

    mkdir -p "$(dirname "${target_file}")"

    # Build the content file (with optional header prepended)
    local content_tmp="${target_file}.tzro_content_tmp"
    if [ -n "${header}" ]; then
        {
            printf '%s\n\n' "${header}"
            cat "${INSTRUCTIONS_SOURCE}"
        } > "${content_tmp}"
    else
        cp "${INSTRUCTIONS_SOURCE}" "${content_tmp}"
    fi

    # For dedicated instruction files (not shared files like AGENTS.md),
    # just overwrite the entire file with the content
    mv "${content_tmp}" "${target_file}"

    echo -e "    ${GREEN}✔ Instructions written to ${target_file} (tier: ${TIER})${NC}"
}

# ---------------------------------------------------------------------------
# Helper: append instructions to an existing file
# ---------------------------------------------------------------------------
append_instructions() {
    local target_file="$1"

    # Use replace_block.sh for idempotent block replacement
    bash "${REPLACE_BLOCK}" "${target_file}" "${INSTRUCTION_MARKER}" "${INSTRUCTIONS_SOURCE}"
}

# ---------------------------------------------------------------------------
# Step 4: Install per-interface
# ---------------------------------------------------------------------------
echo -e "\n${BLUE}[4/5] Configuring selected interfaces...${NC}"

# --- Claude Code (index 0) ---
if [ "${IFACE_SELECTED[0]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}Claude Code${NC}"

    # MCP config: ~/.claude.json
    echo -e "  ${DIM}MCP Config:${NC}"
    inject_mcp_config "$HOME/.claude.json" "mcpServers" "Claude Code"

    # Instructions: ~/.claude/commands/tzro.md
    echo -e "  ${DIM}Instructions:${NC}"
    write_instructions "$HOME/.claude/commands/tzro.md"
fi

# --- Cursor (index 1) ---
if [ "${IFACE_SELECTED[1]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}Cursor${NC}"

    # MCP config: ~/.cursor/mcp.json
    echo -e "  ${DIM}MCP Config:${NC}"
    inject_mcp_config "$HOME/.cursor/mcp.json" "mcpServers" "Cursor"

    # Instructions: .cursor/rules/tzro.mdc (with Cursor frontmatter)
    CURSOR_HEADER='---
description: "Delegate complex multi-step workflows to the local tzro durable execution engine. Trigger on mentions of: tzro, delegate, durable workflow, DAG, multi-step automation."
alwaysApply: false
---'
    echo -e "  ${DIM}Instructions:${NC}"
    write_instructions ".cursor/rules/tzro.mdc" "${CURSOR_HEADER}"
fi

# --- Windsurf (index 2) ---
if [ "${IFACE_SELECTED[2]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}Windsurf${NC}"

    # MCP config: detect location
    WINDSURF_CONFIG=""
    if [ -d "$HOME/.codeium/windsurf" ]; then
        WINDSURF_CONFIG="$HOME/.codeium/windsurf/mcp_config.json"
    elif [ -d "$HOME/.windsurf" ]; then
        WINDSURF_CONFIG="$HOME/.windsurf/mcp_config.json"
    fi

    if [ -n "${WINDSURF_CONFIG}" ]; then
        echo -e "  ${DIM}MCP Config:${NC}"
        inject_mcp_config "${WINDSURF_CONFIG}" "mcpServers" "Windsurf"
    else
        echo -e "  ${YELLOW}⚠ Could not determine Windsurf config path${NC}"
    fi

    # Instructions: .windsurf/rules/tzro.md
    echo -e "  ${DIM}Instructions:${NC}"
    write_instructions ".windsurf/rules/tzro.md"
fi

# --- GitHub Copilot (index 3) ---
if [ "${IFACE_SELECTED[3]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}GitHub Copilot${NC}"

    # MCP config: .github/copilot-mcp.json (project-level)
    echo -e "  ${DIM}MCP Config:${NC}"
    inject_mcp_config ".github/copilot-mcp.json" "servers" "GitHub Copilot"

    # Instructions: .github/instructions/tzro.instructions.md
    COPILOT_HEADER='---
applyTo: "**/*"
---'
    echo -e "  ${DIM}Instructions:${NC}"
    write_instructions ".github/instructions/tzro.instructions.md" "${COPILOT_HEADER}"
fi

# --- OpenCode (index 4) ---
if [ "${IFACE_SELECTED[4]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}OpenCode${NC}"

    # MCP config: .opencode.json (project-level)
    echo -e "  ${DIM}MCP Config:${NC}"
    inject_mcp_config ".opencode.json" "mcpServers" "OpenCode"

    # Instructions: .opencode/agents/tzro.md
    echo -e "  ${DIM}Instructions:${NC}"
    write_instructions ".opencode/agents/tzro.md"
fi

# --- Antigravity IDE / Gemini CLI (index 5) ---
if [ "${IFACE_SELECTED[5]}" = "true" ]; then
    echo -e "\n  ${BOLD}${MAGENTA}Antigravity IDE / Gemini CLI${NC}"

    # MCP config: inject into ALL known config directories unconditionally
    echo -e "  ${DIM}MCP Config:${NC}"

    # Gemini CLI standard config path
    mkdir -p "$HOME/.gemini/config"
    inject_mcp_config "$HOME/.gemini/config/mcp_config.json" "mcpServers" "Antigravity IDE"

    # Antigravity IDE paths
    mkdir -p "$HOME/.gemini/antigravity"
    inject_mcp_config "$HOME/.gemini/antigravity/mcp_config.json" "mcpServers" "Antigravity IDE"

    mkdir -p "$HOME/.gemini/antigravity-ide"
    inject_mcp_config "$HOME/.gemini/antigravity-ide/mcp_config.json" "mcpServers" "Antigravity IDE"

    # Root .gemini fallback
    if [ -d "$HOME/.gemini" ]; then
        inject_mcp_config "$HOME/.gemini/mcp_config.json" "mcpServers" "Antigravity IDE"
    fi

    # Plugin installation is handled separately
    echo -e "  ${DIM}Plugin: run ${CYAN}bash plugins/install_plugins.sh${NC} ${DIM}to install/update the IDE plugin${NC}"
fi

# ---------------------------------------------------------------------------
# Universal fallback: AGENTS.md / CLAUDE.md
# ---------------------------------------------------------------------------
echo -e "\n  ${BOLD}${MAGENTA}Universal Fallback${NC}"
echo -e "  ${DIM}Injecting tzro delegation instructions (tier: ${TIER})...${NC}"

FALLBACK_DONE=false

if [ -f "AGENTS.md" ]; then
    echo -e "  ${DIM}Found AGENTS.md${NC}"
    append_instructions "AGENTS.md"
    FALLBACK_DONE=true
elif [ -f "CLAUDE.md" ]; then
    echo -e "  ${DIM}AGENTS.md not found, falling back to CLAUDE.md${NC}"
    append_instructions "CLAUDE.md"
    FALLBACK_DONE=true
else
    echo -e "  ${DIM}Neither AGENTS.md nor CLAUDE.md found.${NC}"
    create_agents=true
    if [ "${TZRO_NON_INTERACTIVE:-}" != "true" ]; then
        _tty_read -p "  Create AGENTS.md with tzro instructions? (y/n) " -n 1 -r
        echo
        if [[ ! $REPLY =~ ^[Yy]$ ]]; then
            create_agents=false
        fi
    fi
    if [ "$create_agents" = "true" ]; then
        # Use replace_block.sh to create the file with markers
        bash "${REPLACE_BLOCK}" "AGENTS.md" "${INSTRUCTION_MARKER}" "${INSTRUCTIONS_SOURCE}"
        FALLBACK_DONE=true
    fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo ""
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════════════════${NC}"
echo -e "${BOLD}${GREEN}  ✔ tzro MCP + Skills Installation Complete${NC}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════════════════${NC}"
echo -e "  ${BOLD}Binary:${NC}  ${TZRO_MCP_BIN}"

for i in "${!IFACE_IDS[@]}"; do
    if [ "${IFACE_SELECTED[$i]}" = "true" ]; then
        echo -e "  ${GREEN}✔${NC} ${IFACE_NAMES[$i]}"
    fi
done

if [ "${FALLBACK_DONE}" = "true" ]; then
    echo -e "  ${GREEN}✔${NC} Project instructions updated (tier: ${TIER})"
fi

echo ""
echo -e "  ${DIM}Agents will now discover tzro tools via MCP and know how${NC}"
echo -e "  ${DIM}to delegate complex workflows using the injected instructions.${NC}"
echo -e "${BOLD}${GREEN}══════════════════════════════════════════════════════════${NC}"

# Final verification: confirm config files actually have tzro entries
INSTALL_DIR="${TZRO_INSTALL_DIR:-$HOME/.tzro}"
_CONFIG_FAILURES=0
_verify_config() {
    local f="$1"
    if [ -f "$f" ]; then
        local cmd
        cmd=$(jq -r '.mcpServers.tzro.command // empty' "$f" 2>/dev/null || true)
        if [ -n "$cmd" ]; then
            echo -e "  ${GREEN}✔${NC} ${DIM}${f}${NC} → ${cmd}"
        else
            echo -e "  ${RED}✘${NC} ${f} — ${YELLOW}tzro entry missing or was overwritten${NC}"
            echo -e "    ${DIM}$(cat "$f" 2>/dev/null | head -3)${NC}"
            _CONFIG_FAILURES=$((_CONFIG_FAILURES + 1))
        fi
    fi
}

echo ""
echo -e "  ${BOLD}Config Verification:${NC}"
_verify_config "$HOME/.gemini/config/mcp_config.json"
_verify_config "$HOME/.gemini/antigravity/mcp_config.json"
_verify_config "$HOME/.gemini/antigravity-ide/mcp_config.json"
_verify_config "$HOME/.gemini/mcp_config.json"

if [ "${_CONFIG_FAILURES}" -gt 0 ]; then
    echo ""
    echo -e "  ${YELLOW}${BOLD}⚠ Some config files were overwritten by your IDE.${NC}"
    echo -e "  ${DIM}Your editor likely reset the config. Paste the prompt below${NC}"
    echo -e "  ${DIM}into your AI editor chat to fix it:${NC}"
    echo ""
    echo -e "  ${CYAN}─────────────── Copy below this line ───────────────${NC}"
    echo ""
    echo -e "  Add tzro as an MCP server to my mcp_config.json. Write this"
    echo -e "  JSON to ~/.gemini/config/mcp_config.json, merging with any"
    echo -e "  existing mcpServers entries (don't remove other servers):"
    echo ""
    echo -e "  ${BOLD}{${NC}"
    echo -e "  ${BOLD}  \"mcpServers\": {${NC}"
    echo -e "  ${BOLD}    \"tzro\": {${NC}"
    echo -e "  ${BOLD}      \"command\": \"${TZRO_MCP_BIN}\",${NC}"
    echo -e "  ${BOLD}      \"args\": [],${NC}"
    echo -e "  ${BOLD}      \"env\": {${NC}"
    echo -e "  ${BOLD}        \"TZRO_DIR\": \"${INSTALL_DIR}\"${NC}"
    echo -e "  ${BOLD}      }${NC}"
    echo -e "  ${BOLD}    }${NC}"
    echo -e "  ${BOLD}  }${NC}"
    echo -e "  ${BOLD}}${NC}"
    echo ""
    echo -e "  ${CYAN}─────────────── Copy above this line ───────────────${NC}"
else
    echo ""
fi
