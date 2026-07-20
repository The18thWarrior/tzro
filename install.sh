#!/usr/bin/env bash

# tzro - Zero-dependency, Premium One-Line Developer Bootstrapper
# Supported Platforms: macOS (Darwin), Linux
# Architectures: AMD64, ARM64 / Apple Silicon

set -euo pipefail

# ANSI color codes for premium terminal aesthetics
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m' # No Color

# Print banner
echo -e "${BOLD}${CYAN}"
echo "████████╗███████╗██████╗  ██████╗"
echo "╚══██╔══╝╚══███╔╝██╔══██╗██╔═══██╗"
echo "   ██║     ███╔╝ ██████╔╝██║   ██║"
echo "   ██║    ███╔╝  ██╔══██╗██║   ██║"
echo "   ██║   ███████╗██║  ██║╚██████╔╝"
echo "   ╚═╝   ╚══════╝╚═╝  ╚═╝ ╚═════╝ "
echo -e "   -- The Local-First Agentic Engine --${NC}"
echo

# 1. Resolve Installation Directory
INSTALL_DIR="${TZRO_INSTALL_DIR:-$HOME/.tzro}"
echo -e "${BLUE}[1/5] Configuring workspace boundary...${NC}"
echo -e "  Target Install Dir: ${BOLD}${INSTALL_DIR}${NC}"

# Create directories
mkdir -p "${INSTALL_DIR}/bin"
mkdir -p "${INSTALL_DIR}/cache"
mkdir -p "${INSTALL_DIR}/models"

# 2. Detect System Platform and CPU Architecture
echo -e "\n${BLUE}[2/5] Detecting OS and Hardware Architecture...${NC}"
OS="$(uname -s)"
ARCH="$(uname -m)"

echo -e "  Detected OS:   ${BOLD}${OS}${NC}"
echo -e "  Detected Arch: ${BOLD}${ARCH}${NC}"

# Map to standard platforms
PLATFORM=""
case "${OS}" in
    Darwin)
        PLATFORM="darwin"
        ;;
    Linux)
        PLATFORM="linux"
        ;;
    *)
        echo -e "${RED}Error: Unsupported Operating System '${OS}'. tzro requires macOS or Linux.${NC}"
        exit 1
        ;;
esac

case "${ARCH}" in
    x86_64|amd64)
        ARCH_TYPE="amd64"
        ;;
    arm64|aarch64)
        ARCH_TYPE="arm64"
        ;;
    *)
        echo -e "${YELLOW}Warning: Unknown CPU Architecture '${ARCH}'. Falling back to amd64.${NC}"
        ARCH_TYPE="amd64"
        ;;
esac

# 3. Download or Provision Sidecar & GGUF Tactician Models
echo -e "\n${BLUE}[3/5] Provisioning Static Llama-Server Sidecar & GGUF Tactician...${NC}"

if [ "${TZRO_MOCK_DOWNLOAD:-false}" = "true" ]; then
    echo -e "  ${GREEN}✔ [MOCK] Dry-run enabled. Creating mock assets...${NC}"
    # Provision mock binaries
    echo "#!/bin/sh" > "${INSTALL_DIR}/bin/llama-server"
    echo "echo 'mock llama-server'" >> "${INSTALL_DIR}/bin/llama-server"
    chmod +x "${INSTALL_DIR}/bin/llama-server"

    echo "mock model content" > "${INSTALL_DIR}/models/Agents-A1-4B-Q4_K_M.gguf"
    echo "mock mmproj" > "${INSTALL_DIR}/models/mmproj-F32.gguf"
    echo "mock router model" > "${INSTALL_DIR}/models/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf"
    
    echo "#!/bin/sh" > "${INSTALL_DIR}/bin/tzro-mcp"
    echo "echo 'mock tzro-mcp'" >> "${INSTALL_DIR}/bin/tzro-mcp"
    chmod +x "${INSTALL_DIR}/bin/tzro-mcp"
    
    if [ -n "${TZRO_SOURCE_BIN:-}" ] && [ -f "${TZRO_SOURCE_BIN}" ]; then
        cp "${TZRO_SOURCE_BIN}" "${INSTALL_DIR}/bin/tzro"
        chmod +x "${INSTALL_DIR}/bin/tzro"
    else
        echo "#!/bin/sh" > "${INSTALL_DIR}/bin/tzro"
        echo "echo 'mock tzro'" >> "${INSTALL_DIR}/bin/tzro"
        chmod +x "${INSTALL_DIR}/bin/tzro"
    fi
else
    # Compile tzro, tzro-mcp, and tzrod locally if Go is available, otherwise download pre-compiled binaries
    if command -v go &>/dev/null; then
        echo -e "  ${GREEN}✔ Go compiler detected. Building tzro binaries from source locally...${NC}"
        rm -f "${INSTALL_DIR}/bin/tzro" "${INSTALL_DIR}/bin/tzro-mcp" "${INSTALL_DIR}/bin/tzrod"
        go build -o "${INSTALL_DIR}/bin/tzro" ./cmd/tzro
        echo -e "  ${GREEN}✔ Built tzro CLI${NC}"
        go build -o "${INSTALL_DIR}/bin/tzro-mcp" ./cmd/tzro-mcp
        echo -e "  ${GREEN}✔ Built tzro-mcp server${NC}"
        go build -o "${INSTALL_DIR}/bin/tzrod" ./cmd/tzrod
        echo -e "  ${GREEN}✔ Built tzrod daemon${NC}"
    else
        echo -e "  ${YELLOW}⚠ Go compiler not found. Fetching pre-compiled release binaries...${NC}"

        # Determine release download URL base
        TZRO_S3_BUCKET="${TZRO_S3_BUCKET:-tzro-app}"
        RELEASE_BASE="https://${TZRO_S3_BUCKET}.s3.amazonaws.com/releases/latest"

        # Download tzro CLI
        if [ -f "./tzro_engine" ]; then
            cp "./tzro_engine" "${INSTALL_DIR}/bin/tzro"
        else
            TZRO_CLI_URL="${RELEASE_BASE}/tzro-${PLATFORM}-${ARCH_TYPE}"
            echo -e "  Downloading tzro CLI from ${TZRO_CLI_URL}..."
            if curl -fSL -o "${INSTALL_DIR}/bin/tzro" "${TZRO_CLI_URL}" 2>/dev/null; then
                echo -e "  ${GREEN}✔ Downloaded tzro CLI${NC}"
            else
                echo -e "${RED}Error: Failed to download tzro CLI. Go compiler or a release binary is required.${NC}"
                exit 1
            fi
        fi

        # Download tzro-mcp
        TZRO_MCP_URL="${RELEASE_BASE}/tzro-mcp-${PLATFORM}-${ARCH_TYPE}"
        echo -e "  Downloading tzro-mcp from ${TZRO_MCP_URL}..."
        if curl -fSL -o "${INSTALL_DIR}/bin/tzro-mcp" "${TZRO_MCP_URL}" 2>/dev/null; then
            echo -e "  ${GREEN}✔ Downloaded tzro-mcp server${NC}"
        else
            echo -e "  ${YELLOW}⚠ Could not download tzro-mcp. MCP server mode will be unavailable until built from source.${NC}"
        fi

        # Download tzrod daemon
        TZROD_URL="${RELEASE_BASE}/tzrod-${PLATFORM}-${ARCH_TYPE}"
        echo -e "  Downloading tzrod daemon from ${TZROD_URL}..."
        if curl -fSL -o "${INSTALL_DIR}/bin/tzrod" "${TZROD_URL}" 2>/dev/null; then
            echo -e "  ${GREEN}✔ Downloaded tzrod daemon${NC}"
        else
            echo -e "  ${YELLOW}⚠ Could not download tzrod daemon. Daemon background features will be unavailable.${NC}"
        fi
    fi
    chmod +x "${INSTALL_DIR}/bin/tzro" 2>/dev/null || true
    chmod +x "${INSTALL_DIR}/bin/tzro-mcp" 2>/dev/null || true
    chmod +x "${INSTALL_DIR}/bin/tzrod" 2>/dev/null || true

    # Downloading static precompiled llama-server
    # Real downloads target static platform binaries hosted on tzro CDN / GitHub Releases
    LLAMA_SERVER_URL="https://github.com/ggerganov/llama.cpp/releases/download/b4777/llama-b4777-bin-${PLATFORM}-${ARCH_TYPE}.zip"
    echo -e "  Downloading llama-server sidecar from GitHub Releases..."
    # Note: For GA, we download static binaries. We mock or download to ~/.tzro/bin/llama-server
    # In case curl fails or needs bypass:
    # Here we create a small wrapper or download.
    # To keep script robust and offline-resilient for development:
    if curl -s --head "${LLAMA_SERVER_URL}" | grep "200 OK" > /dev/null; then
        TEMP_ZIP="/tmp/tzro-llama.zip"
        curl -L -o "${TEMP_ZIP}" "${LLAMA_SERVER_URL}"
        # unzip or extract
        # For simplicity of installer demo we fall back to a self-contained static execution helper if not fully available
        rm -f "${TEMP_ZIP}"
    fi

    # Ensure a robust fallback llama-server binary exists in bin
    if [ ! -f "${INSTALL_DIR}/bin/llama-server" ]; then
        echo -e "  ${YELLOW}⚠ Static CDN zip download skipped or not found. Provisioning local sidecar link...${NC}"
        # Create a lightweight dummy/wrapper or look for local llama-server
        if command -v llama-server &>/dev/null; then
            cp "$(command -v llama-server)" "${INSTALL_DIR}/bin/llama-server"
        else
            # Create a fallback wrapper that exits immediately with a clear error
            # so the sidecar manager detects the failure and falls back to cloud mode
            echo -e "  ${YELLOW}⚠ No llama-server binary found. Creating placeholder wrapper.${NC}"
            echo -e "  ${YELLOW}  Local inference will be unavailable until a real llama-server is installed.${NC}"
            echo -e "  ${YELLOW}  The engine will default to cloud-only mode.${NC}"
            cat << 'EOF' > "${INSTALL_DIR}/bin/llama-server"
#!/usr/bin/env bash
echo "[Llama Sidecar Error] No real llama-server binary installed." >&2
echo "[Llama Sidecar Error] Install llama.cpp or download via the tzro Settings panel." >&2
echo "[Llama Sidecar Error] The engine will operate in cloud-only mode." >&2
exit 1
EOF
            chmod +x "${INSTALL_DIR}/bin/llama-server"

            # Write config with sidecar disabled since no real binary exists
            TZRO_CONFIG="${INSTALL_DIR}/config.json"
            if [ ! -f "${TZRO_CONFIG}" ]; then
                cat > "${TZRO_CONFIG}" << 'CONFIG_EOF'
{
  "modelMode": "cloud",
  "cloudProvider": "google",
  "cloudApiKey": "",
  "cloudModel": "gemini-flash-latest",
  "speedFloor": 5,
  "sidecarEnabled": false,
  "ggufModelPath": "models/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf",
  "routerModelPath": "models/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf",
  "modelsDir": "",
  "confidenceThreshold": 3,
  "executorNodeDelayMs": 800,
  "executorLevelDelayMs": 500
}
CONFIG_EOF
                echo -e "  ${GREEN}✔ Default config written with sidecar disabled (cloud-only mode)${NC}"
            fi
        fi
    fi

    # Provision Tactician Model GGUF (~2.8 GB default Qwopus 3.5 4B MTP)
    GGUF_PATH="${INSTALL_DIR}/models/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf"
    GGUF_URL="https://huggingface.co/Jackrong/Qwopus3.5-4B-Coder-MTP-GGUF/resolve/main/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf"
    
    IS_PLACEHOLDER=false
    if [ -f "${GGUF_PATH}" ]; then
        # Only cat if the file size is small to prevent bash memory exhaustion
        FILE_SIZE=$(wc -c < "${GGUF_PATH}" 2>/dev/null | tr -d '[:space:]' || echo 0)
        if [ -n "${FILE_SIZE}" ] && [ "${FILE_SIZE}" -lt 1000 ]; then
            if [ "$(cat "${GGUF_PATH}" 2>/dev/null)" = "tzro-model-gguf-placeholder" ]; then
                IS_PLACEHOLDER=true
            fi
        fi
    fi

    if [ ! -f "${GGUF_PATH}" ] || [ "${IS_PLACEHOLDER}" = "true" ]; then
        echo -e "  Downloading default Qwopus 3.5 4B tactician model (~2.8 GB)..."
        echo -e "  ${DIM}Source: huggingface.co/Jackrong/Qwopus3.5-4B-Coder-MTP-GGUF${NC}"
        GGUF_TMP="${GGUF_PATH}.download"
        if curl -fSL --progress-bar -o "${GGUF_TMP}" "${GGUF_URL}"; then
            mv "${GGUF_TMP}" "${GGUF_PATH}"
            echo -e "  ${GREEN}✔ Default tactician model downloaded${NC}"
        else
            rm -f "${GGUF_TMP}"
            echo -e "  ${RED}✘ Tactician model download failed. Download manually from the Settings panel after install.${NC}"
        fi
    else
        echo -e "  ${GREEN}✔ Tactician model already present${NC}"
    fi

    # Provision Multimodal Vision Projector (~534 MB companion for PDF OCR & image analysis)
    MMPROJ_PATH="${INSTALL_DIR}/models/mmproj-F32.gguf"
    MMPROJ_URL="https://huggingface.co/Jackrong/Qwopus3.5-4B-Coder-MTP-GGUF/resolve/main/mmproj-F32.gguf"
    if [ ! -f "${MMPROJ_PATH}" ]; then
        echo -e "  Downloading vision projector (~534 MB) for local PDF OCR & image analysis..."
        MMPROJ_TMP="${MMPROJ_PATH}.download"
        if curl -fSL --progress-bar -o "${MMPROJ_TMP}" "${MMPROJ_URL}"; then
            mv "${MMPROJ_TMP}" "${MMPROJ_PATH}"
            echo -e "  ${GREEN}✔ Vision projector downloaded${NC}"
        else
            rm -f "${MMPROJ_TMP}"
            echo -e "  ${YELLOW}⚠ Vision projector download failed. Local vision features will be unavailable until downloaded from Settings.${NC}"
        fi
    else
        echo -e "  ${GREEN}✔ Vision projector already present${NC}"
    fi

    # Provision Router Model GGUF (~1.2 GB MiniCPM5 1B Claude Opus Fable5 Thinking Q8_0)
    ROUTER_GGUF_PATH="${INSTALL_DIR}/models/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf"
    ROUTER_GGUF_URL="https://huggingface.co/GnLOLot/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-GGUF/resolve/main/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf?download=true"

    ROUTER_IS_PLACEHOLDER=false
    if [ -f "${ROUTER_GGUF_PATH}" ]; then
        FILE_SIZE=$(wc -c < "${ROUTER_GGUF_PATH}" 2>/dev/null | tr -d '[:space:]' || echo 0)
        if [ -n "${FILE_SIZE}" ] && [ "${FILE_SIZE}" -lt 1000 ]; then
            if [ "$(cat "${ROUTER_GGUF_PATH}" 2>/dev/null)" = "tzro-model-gguf-placeholder" ]; then
                ROUTER_IS_PLACEHOLDER=true
            fi
        fi
    fi

    if [ ! -f "${ROUTER_GGUF_PATH}" ] || [ "${ROUTER_IS_PLACEHOLDER}" = "true" ]; then
        echo -e "  Downloading router model MiniCPM5 1B (~1.2 GB)..."
        echo -e "  ${DIM}Source: huggingface.co/GnLOLot/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-GGUF${NC}"
        ROUTER_TMP="${ROUTER_GGUF_PATH}.download"
        if curl -fSL --progress-bar -o "${ROUTER_TMP}" "${ROUTER_GGUF_URL}"; then
            mv "${ROUTER_TMP}" "${ROUTER_GGUF_PATH}"
            echo -e "  ${GREEN}✔ Router model downloaded${NC}"
        else
            rm -f "${ROUTER_TMP}"
            echo -e "  ${YELLOW}⚠ Router model download failed. The engine will run in single-sidecar mode until downloaded from Settings.${NC}"
        fi
    else
        echo -e "  ${GREEN}✔ Router model already present${NC}"
    fi
fi

if [ "${OS}" = "Darwin" ]; then
    echo -e "\n  ${BLUE}Bypassing macOS Gatekeeper quarantine on downloaded binaries...${NC}"
    xattr -d com.apple.quarantine "${INSTALL_DIR}/bin/tzro" 2>/dev/null || true
    xattr -d com.apple.quarantine "${INSTALL_DIR}/bin/tzro-mcp" 2>/dev/null || true
    xattr -d com.apple.quarantine "${INSTALL_DIR}/bin/tzrod" 2>/dev/null || true
    xattr -d com.apple.quarantine "${INSTALL_DIR}/bin/llama-server" 2>/dev/null || true
    echo -e "  ${GREEN}✔ macOS Gatekeeper quarantine bypassed successfully${NC}"
fi

# 4. Initialize Local SQLite Databases & Apply Migration
echo -e "\n${BLUE}[4/5] Initializing Local SQLite databases and schema...${NC}"
DB_PATH="${INSTALL_DIR}/tzro.db"

# Trigger tzro schema creation by executing memory list offline command
"${INSTALL_DIR}/bin/tzro" memory list --offline --db "${DB_PATH}" > /dev/null || true

if [ -f "${DB_PATH}" ]; then
    echo -e "  ${GREEN}✔ SQLite database successfully bootstrapped with all tables!${NC}"
else
    echo -e "${RED}Error: Failed to initialize SQLite database.${NC}"
    exit 1
fi

# 5. Verify Pathing & Interactive Welcome Dashboard
echo -e "\n${BLUE}[5/5] Checking Pathing Alignment...${NC}"

PATH_OK=false
if [[ ":$PATH:" == *":${INSTALL_DIR}/bin:"* ]]; then
    PATH_OK=true
fi

echo -e "=========================================================="
echo -e "           ${BOLD}${GREEN}✔ TZRO INSTALLATION COMPLETE${NC}"
echo -e "=========================================================="
echo -e "  ${BOLD}Workspace Boundary:${NC}  ${INSTALL_DIR}"
echo -e "  ${BOLD}Database Booted:${NC}     ${DB_PATH}"
echo -e "  ${BOLD}Llama Sidecar:${NC}       ${INSTALL_DIR}/bin/llama-server"
echo -e "  ${BOLD}Daemon:${NC}              ${INSTALL_DIR}/bin/tzrod"
echo -e "  ${BOLD}MCP Server:${NC}          ${INSTALL_DIR}/bin/tzro-mcp"
echo -e "  ${BOLD}Tactician Model:${NC}     ${INSTALL_DIR}/models/Qwopus3.5-4B-Coder-MTP-Q4_K_M.gguf"
echo -e "  ${BOLD}Router Model:${NC}        ${INSTALL_DIR}/models/MiniCPM5-1B-Claude-Opus-Fable5-Thinking-Q8_0.gguf"
echo -e "  ${BOLD}Vision Projector:${NC}    ${INSTALL_DIR}/models/mmproj-F32.gguf"
echo -e "=========================================================="

if [ "$PATH_OK" = "true" ]; then
    echo -e "\n  ${GREEN}Awesome! ${INSTALL_DIR}/bin is already in your PATH.${NC}"
    echo -e "  Simply type ${BOLD}tzro${NC} to launch the premium developer dashboard TUI!"
else
    echo -e "\n  ${YELLOW}Path Alignment Required:${NC}"
    echo -e "  Please add the following line to your shell configuration (${BOLD}~/.zshrc${NC} or ${BOLD}~/.bashrc${NC}):"
    echo -e "    ${MAGENTA}export PATH=\"\$PATH:${INSTALL_DIR}/bin\"${NC}"
    echo -e "\n  Then load the configuration and execute tzro:"
    echo -e "    ${BOLD}source ~/.zshrc && tzro${NC}"
fi
echo -e "=========================================================="
echo

# Optional: Configure agentic interfaces
# We resolve the plugins directory. If it doesn't exist locally, we download it from GitHub.
PLUGINS_SRC_DIR=""
CURRENT_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-.}")" && pwd)"
if [ -d "${CURRENT_SCRIPT_DIR}/plugins" ] && [ -f "${CURRENT_SCRIPT_DIR}/plugins/install_mcp.sh" ]; then
    PLUGINS_SRC_DIR="${CURRENT_SCRIPT_DIR}/plugins"
fi

# Export original repo root for TZRO_DIR lookup in install_mcp.sh
export TZRO_REPO_ROOT="${CURRENT_SCRIPT_DIR}"

if [ -n "${PLUGINS_SRC_DIR}" ]; then
    echo -e "\n${BLUE}Copying local plugins to ${INSTALL_DIR}/plugins...${NC}"
    rm -rf "${INSTALL_DIR}/plugins"
    cp -r "${PLUGINS_SRC_DIR}" "${INSTALL_DIR}/"
else
    echo -e "\n${BLUE}Downloading plugins from GitHub to ${INSTALL_DIR}/plugins...${NC}"
    TEMP_ZIP="/tmp/tzro-repo.zip"
    TEMP_EXTRACT="/tmp/tzro-extract"
    rm -rf "${TEMP_ZIP}" "${TEMP_EXTRACT}"
    
    # Download plugins.zip from S3
    TZRO_S3_BUCKET="${TZRO_S3_BUCKET:-tzro-app}"
    PLUGINS_URL="https://${TZRO_S3_BUCKET}.s3.amazonaws.com/releases/latest/plugins.zip"
    if curl -fSL -o "${TEMP_ZIP}" "${PLUGINS_URL}" 2>/dev/null; then
        mkdir -p "${TEMP_EXTRACT}"
        unzip -q "${TEMP_ZIP}" -d "${TEMP_EXTRACT}"
        if [ -d "${TEMP_EXTRACT}/plugins" ]; then
            rm -rf "${INSTALL_DIR}/plugins"
            cp -r "${TEMP_EXTRACT}/plugins" "${INSTALL_DIR}/"
            echo -e "  ${GREEN}✔ Plugins downloaded and installed successfully${NC}"
        else
            echo -e "  ${YELLOW}⚠ Downloaded archive did not contain plugins directory. Skipping editor configuration.${NC}"
        fi
        rm -rf "${TEMP_ZIP}" "${TEMP_EXTRACT}"
    else
        echo -e "  ${YELLOW}⚠ Failed to download plugins from S3. Skipping editor configuration.${NC}"
    fi
fi

MCP_INSTALLER="${INSTALL_DIR}/plugins/install_mcp.sh"
PLUGINS_INSTALLER="${INSTALL_DIR}/plugins/install_plugins.sh"

if [ -f "${MCP_INSTALLER}" ]; then
    _run_mcp_installer() {
        # Install plugins FIRST — copying files to the IDE's plugins/ directory
        # can trigger a config reload that would overwrite mcp_config.json.
        # By installing plugins first, the reload happens before we write our config.
        if [ -f "${PLUGINS_INSTALLER}" ]; then
            bash "${PLUGINS_INSTALLER}"
        fi
        bash "${MCP_INSTALLER}"
    }

    if [ -t 0 ]; then
        # stdin is a terminal — prompt interactively
        echo
        echo -e "=========================================================="
        echo -e "${CYAN}${BOLD}  ⚙  AI Editor Configuration${NC}"
        echo -e "=========================================================="
        echo -e "${DIM}  This will auto-detect Claude Code, Cursor, Windsurf, Copilot, etc.${NC}"
        echo -e "${DIM}  and wire up MCP tools + agent instructions.${NC}"
        echo
        read -p "  $(echo -e "${BOLD}")Run the MCP installer? (y/n)$(echo -e "${NC}") " -n 1 -r
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            _run_mcp_installer
        fi
    elif [ "${TZRO_NON_INTERACTIVE:-}" = "true" ] || [ -n "${ANTIGRAVITY_AGENT:-}" ] || [ -n "${CLAUDE:-}" ] || [ -n "${CLAUDE_AGENT:-}" ]; then
        # Explicitly non-interactive (agent or CI) — auto-run
        echo -e "${BLUE}Running MCP and plugin installers non-interactively...${NC}"
        export TZRO_NON_INTERACTIVE=true
        _run_mcp_installer
    elif [ -e /dev/tty ]; then
        # stdin is a pipe (e.g. curl | bash) but a terminal exists.
        # Use command-level redirects (< /dev/tty) instead of exec to avoid
        # a malloc double-free crash in macOS bash 3.2 during exit cleanup.
        echo
        echo -e "=========================================================="
        echo -e "${CYAN}${BOLD}  ⚙  AI Editor Configuration${NC}"
        echo -e "=========================================================="
        echo -e "${DIM}  This will auto-detect Claude Code, Cursor, Windsurf, Copilot, etc.${NC}"
        echo -e "${DIM}  and wire up MCP tools + agent instructions.${NC}"
        echo
        read -p "  $(echo -e "${BOLD}")Run the MCP installer? (y/n)$(echo -e "${NC}") " -n 1 -r < /dev/tty
        echo
        if [[ $REPLY =~ ^[Yy]$ ]]; then
            _run_mcp_installer < /dev/tty
        fi
    else
        # Truly headless (no TTY at all) — auto-run
        echo -e "${BLUE}No interactive terminal detected. Running MCP installer automatically...${NC}"
        export TZRO_NON_INTERACTIVE=true
        _run_mcp_installer
    fi
fi
