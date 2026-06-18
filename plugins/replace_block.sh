#!/usr/bin/env bash

# replace_block.sh — Idempotent marker-delimited block replacement
#
# Usage:
#   bash plugins/replace_block.sh <target_file> <marker_name> <content_file>
#
# Example:
#   bash plugins/replace_block.sh AGENTS.md "TZRO INSTRUCTIONS" plugins/mcp/tzro-agent-instructions.md
#
# Behavior:
#   1. If target_file doesn't exist → create it with content wrapped in markers
#   2. If markers found → delete ALL existing marker blocks, then re-append
#   3. If no markers found → append with markers
#   4. Also cleans up legacy "TZRO DELEGATION" markers if present
#
# Environment:
#   TZRO_DRY_RUN=true  — preview mode, no files modified

set -euo pipefail

# ANSI colors
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
DIM='\033[2m'
NC='\033[0m'

DRY_RUN="${TZRO_DRY_RUN:-false}"

# ---------------------------------------------------------------------------
# Arguments
# ---------------------------------------------------------------------------
TARGET_FILE="${1:-}"
MARKER_NAME="${2:-}"
CONTENT_FILE="${3:-}"

if [ -z "${TARGET_FILE}" ] || [ -z "${MARKER_NAME}" ] || [ -z "${CONTENT_FILE}" ]; then
    echo "Usage: replace_block.sh <target_file> <marker_name> <content_file>" >&2
    exit 1
fi

if [ ! -f "${CONTENT_FILE}" ]; then
    echo "Error: content file '${CONTENT_FILE}' does not exist." >&2
    exit 1
fi

BEGIN_MARKER="<!-- BEGIN ${MARKER_NAME} -->"
END_MARKER="<!-- END ${MARKER_NAME} -->"

# Legacy markers to also clean up
LEGACY_BEGIN="<!-- BEGIN TZRO DELEGATION -->"
LEGACY_END="<!-- END TZRO DELEGATION -->"

# ---------------------------------------------------------------------------
# Helper: delete all occurrences of a marker block from a file
# Uses a temp file approach for macOS sed compatibility
# ---------------------------------------------------------------------------
delete_marker_blocks() {
    local file="$1"
    local begin_pattern="$2"
    local end_pattern="$3"

    if ! grep -q "${begin_pattern}" "${file}" 2>/dev/null; then
        return 0
    fi

    local tmp_file="${file}.tzro_replace_tmp"

    # Use awk to delete everything between begin and end markers (inclusive)
    # This is more portable than sed for multi-line deletion
    awk -v begin="${begin_pattern}" -v end="${end_pattern}" '
    BEGIN { skip = 0 }
    index($0, begin) { skip = 1; next }
    index($0, end) { skip = 0; next }
    !skip { print }
    ' "${file}" > "${tmp_file}"

    mv "${tmp_file}" "${file}"
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

# Case 1: Target file doesn't exist — create it
if [ ! -f "${TARGET_FILE}" ]; then
    if [ "${DRY_RUN}" = "true" ]; then
        echo -e "    ${DIM}[DRY RUN] Would create ${TARGET_FILE} with ${MARKER_NAME} block${NC}"
        exit 0
    fi

    mkdir -p "$(dirname "${TARGET_FILE}")"
    {
        echo "${BEGIN_MARKER}"
        echo ""
        cat "${CONTENT_FILE}"
        echo ""
        echo "${END_MARKER}"
    } > "${TARGET_FILE}"

    echo -e "    ${GREEN}✔ Created ${TARGET_FILE} with ${MARKER_NAME} block${NC}"
    exit 0
fi

# Case 2 & 3: Target file exists
if [ "${DRY_RUN}" = "true" ]; then
    if grep -q "${BEGIN_MARKER}" "${TARGET_FILE}" 2>/dev/null; then
        echo -e "    ${DIM}[DRY RUN] Would replace ${MARKER_NAME} block in ${TARGET_FILE}${NC}"
    elif grep -q "${LEGACY_BEGIN}" "${TARGET_FILE}" 2>/dev/null; then
        echo -e "    ${DIM}[DRY RUN] Would replace legacy TZRO DELEGATION block in ${TARGET_FILE}${NC}"
    else
        echo -e "    ${DIM}[DRY RUN] Would append ${MARKER_NAME} block to ${TARGET_FILE}${NC}"
    fi
    exit 0
fi

# Track whether we found and replaced anything
REPLACED=false

# Delete existing marker blocks (current marker name)
if grep -q "${BEGIN_MARKER}" "${TARGET_FILE}" 2>/dev/null; then
    delete_marker_blocks "${TARGET_FILE}" "${BEGIN_MARKER}" "${END_MARKER}"
    REPLACED=true
fi

# Delete legacy marker blocks
if grep -q "${LEGACY_BEGIN}" "${TARGET_FILE}" 2>/dev/null; then
    delete_marker_blocks "${TARGET_FILE}" "${LEGACY_BEGIN}" "${LEGACY_END}"
    REPLACED=true
fi

# Remove trailing blank lines left by deletion (clean up whitespace)
# Use a temp file for macOS compatibility
if [ "${REPLACED}" = "true" ]; then
    local_tmp="${TARGET_FILE}.tzro_trim_tmp"
    # Remove trailing newlines from end of file
    awk 'NF {p=1} p' "${TARGET_FILE}" > "${local_tmp}" || cp "${TARGET_FILE}" "${local_tmp}"
    mv "${local_tmp}" "${TARGET_FILE}"
fi

# Append new block
{
    echo ""
    echo "${BEGIN_MARKER}"
    echo ""
    cat "${CONTENT_FILE}"
    echo ""
    echo "${END_MARKER}"
} >> "${TARGET_FILE}"

if [ "${REPLACED}" = "true" ]; then
    echo -e "    ${GREEN}✔ Replaced ${MARKER_NAME} block in ${TARGET_FILE}${NC}"
else
    echo -e "    ${GREEN}✔ Appended ${MARKER_NAME} block to ${TARGET_FILE}${NC}"
fi
