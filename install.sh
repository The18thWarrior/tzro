#!/bin/sh
# Install the native CLI and configure detected agents. No model downloads.
set -eu

fail() { printf 'tzro install: %s\n' "$*" >&2; exit 1; }
shell_quote() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }
configure_path_file() {
    if ! grep -Fqx "$PATH_LINE" "$1" 2>/dev/null; then
        mkdir -p "$(dirname "$1")"
        printf '\n%s\n' "$PATH_LINE" >> "$1"
    fi
    printf 'PATH configured in %s. Open a new shell for interactive use.\n' "$1"
}
CLI_ONLY=false
MODIFY_PATH=true
[ "${TZRO_NO_MODIFY_PATH:-0}" = 1 ] && MODIFY_PATH=false
for arg in "$@"; do
    case "$arg" in
        --cli-only) CLI_ONLY=true ;;
        --no-modify-path) MODIFY_PATH=false ;;
        --help) printf '%s\n' 'Usage: sh install.sh [--cli-only] [--no-modify-path]' 'Environment: TZRO_INSTALL_DIR, TZRO_VERSION, TZRO_DOWNLOAD_BASE_URL, TZRO_SOURCE_BIN'; exit 0 ;;
        *) fail "unknown option: $arg" ;;
    esac
done

INSTALL_DIR=${TZRO_INSTALL_DIR:-"$HOME/.tzro"}
mkdir -p "$INSTALL_DIR/bin"
INSTALL_DIR=$(cd "$INSTALL_DIR" && pwd -P)
STAGE=$(mktemp -d "$INSTALL_DIR/bin/.install.XXXXXX")
trap 'rm -rf "$STAGE"' EXIT HUP INT TERM

if [ -n "${TZRO_SOURCE_BIN:-}" ]; then
    [ -f "$TZRO_SOURCE_BIN" ] || fail 'TZRO_SOURCE_BIN does not name a file'
    cp "$TZRO_SOURCE_BIN" "$STAGE/tzro"
else
    case "$(uname -s)/$(uname -m)" in
        Darwin/arm64|Darwin/aarch64) PLATFORM=darwin-arm64 ;;
        Darwin/x86_64|Darwin/amd64) PLATFORM=darwin-amd64 ;;
        Linux/x86_64|Linux/amd64) PLATFORM=linux-amd64 ;;
        *) fail "unsupported platform: $(uname -s) $(uname -m)" ;;
    esac
    command -v curl >/dev/null 2>&1 || fail 'curl is required'
    DOWNLOAD_BASE=${TZRO_DOWNLOAD_BASE_URL:-https://tzro-app.s3.amazonaws.com/releases}
    fetch() { curl --fail --location --silent --show-error --proto '=https,file' --proto-redir '=https,file' --connect-timeout 15 --max-time 180 "$1" -o "$2"; }
    VERSION=${TZRO_VERSION:-latest}
    if [ "$VERSION" = latest ]; then
        fetch "$DOWNLOAD_BASE/latest/version.txt" "$STAGE/version.txt" || fail 'cannot resolve the latest release'
        VERSION=$(cat "$STAGE/version.txt")
    fi
    case "$VERSION" in
        ''|*[!a-zA-Z0-9._-]*|.*) fail 'invalid release version' ;;
    esac
    ARTIFACT=tzro-$PLATFORM
    printf 'Downloading tzro %s (%s)...\n' "$VERSION" "$PLATFORM"
    fetch "$DOWNLOAD_BASE/$VERSION/SHA256SUMS" "$STAGE/SHA256SUMS" || fail 'cannot download release checksums'
    EXPECTED=$(awk -v name="$ARTIFACT" '$2 == name {print $1}' "$STAGE/SHA256SUMS")
    [ "${#EXPECTED}" -eq 64 ] || fail 'missing or invalid artifact checksum'
    case "$EXPECTED" in *[!a-fA-F0-9]*) fail 'invalid artifact checksum' ;; esac
    fetch "$DOWNLOAD_BASE/$VERSION/$ARTIFACT" "$STAGE/tzro" || fail 'binary download failed'
    if command -v sha256sum >/dev/null 2>&1; then
        ACTUAL=$(sha256sum "$STAGE/tzro" | awk '{print $1}')
    elif command -v shasum >/dev/null 2>&1; then
        ACTUAL=$(shasum -a 256 "$STAGE/tzro" | awk '{print $1}')
    else
        fail 'sha256sum or shasum is required to verify the download'
    fi
    [ "$ACTUAL" = "$EXPECTED" ] || fail 'checksum mismatch; existing installation preserved'
fi
chmod 755 "$STAGE/tzro"
mv -f "$STAGE/tzro" "$INSTALL_DIR/bin/tzro"

# Child processes get a working PATH immediately. The parent shell cannot be changed.
case ":$PATH:" in
    *":$INSTALL_DIR/bin:"*) ;;
    *)
        PATH="$INSTALL_DIR/bin:$PATH"
        export PATH
        if [ "$MODIFY_PATH" = true ]; then
            case "${SHELL:-/bin/sh}" in
                */zsh) SHELL_RC="${ZDOTDIR:-$HOME}/.zshrc" ;;
                */bash) SHELL_RC="$HOME/.bashrc" ;;
                */sh) SHELL_RC="$HOME/.profile" ;;
                *) SHELL_RC= ;;
            esac
            if [ -n "$SHELL_RC" ]; then
                PATH_LINE="case :\$PATH: in *:$(shell_quote "$INSTALL_DIR/bin"):*) ;; *) export PATH=$(shell_quote "$INSTALL_DIR/bin"):\$PATH ;; esac # tzro installer"
                configure_path_file "$SHELL_RC"
                case "${SHELL:-/bin/sh}" in
                    */bash)
                        # Login Bash reads the first existing profile, not .bashrc.
                        LOGIN_RC="$HOME/.profile"
                        for profile in "$HOME/.bash_profile" "$HOME/.bash_login" "$HOME/.profile"; do
                            if [ -f "$profile" ]; then LOGIN_RC=$profile; break; fi
                        done
                        configure_path_file "$LOGIN_RC"
                        ;;
                esac
            fi
        fi
        printf 'For this shell: export PATH=%s:$PATH\n' "$(shell_quote "$INSTALL_DIR/bin")"
        ;;
esac

if [ "$CLI_ONLY" = false ]; then
    if ! "$INSTALL_DIR/bin/tzro" init --hooks auto </dev/null; then
        printf 'CLI installed; agent setup is incomplete. Repair with: %s init --hooks auto\n' "$(shell_quote "$INSTALL_DIR/bin/tzro")" >&2
        exit 1
    fi
fi
printf '\nTZRO v2 INSTALLATION COMPLETE\nBinary: %s/bin/tzro\n' "$INSTALL_DIR"
printf '%s\n' 'Agent integrations can require a client restart or native approval; see setup results above.'
printf 'Try: %s probe "<symbol>"\n' "$(shell_quote "$INSTALL_DIR/bin/tzro")"
