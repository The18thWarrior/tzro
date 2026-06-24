# Onboarding Tier Selection & Idempotent Instruction Injection

## Problem

Two related issues with the current onboarding flow:

1. **No tier selection.** The `delegationMode` config field exists (`conservative` / `balanced` / `aggressive`) and MCP tool descriptions already adapt to it, but there's no onboarding step where users choose their preferred integration depth. The agent instruction prose (`tzro-agent-instructions.md`) is a single static file — the same "balanced" content gets injected regardless of what the user wants.

2. **Broken idempotency on re-run.** The `install_mcp.sh` script uses `<!-- BEGIN TZRO DELEGATION -->` / `<!-- END TZRO DELEGATION -->` markers around injected content, but the `append_instructions()` function only checks for existence and skips if found — it never replaces. Running the installer multiple times (or running it after the markers are already present) results in duplicate blocks accumulating in AGENTS.md.

## Design

### Tier Definitions

| Tier | Config Value | Tone | Eval Trigger | Reassess Trigger | Mandatory Delegation |
|:---|:---|:---|:---|:---|:---|
| Conservative | `conservative` | "May delegate" — cost savings focus | 8+ sequential calls | 12+ in-context calls | Only bulk memory ingestion |
| Balanced | `balanced` | "Must delegate" — non-frontier work | 3+ sequential calls | 5+ in-context calls | Codebase exploration, web research, memory ingestion |
| Aggressive | `aggressive` | "MUST delegate ALL" — maximum tzro usage | 2+ sequential calls | 3+ in-context calls | All exploration, research, analysis, env inspection, log analysis, multi-file code analysis |

### File Layout

| Action | Path | Description |
|:---|:---|:---|
| NEW | `plugins/mcp/tzro-agent-instructions-conservative.md` | Conservative tier instructions (~80 lines). Softer language, higher thresholds, no cautionary example, no tool taxonomy, no Probe Node emphasis. |
| KEEP | `plugins/mcp/tzro-agent-instructions.md` | Balanced tier instructions (~190 lines). Unchanged from current file — this remains the default. |
| NEW | `plugins/mcp/tzro-agent-instructions-aggressive.md` | Aggressive tier instructions (~230 lines). Strongest language, lowest thresholds, full MCP tool taxonomy inline, expanded mandatory patterns, local inference arbitrage section. |
| NEW | `plugins/replace_block.sh` | Reusable idempotent block-replace helper script. |
| MODIFY | `plugins/install_mcp.sh` | Add tier selection step, update instruction injection to use `replace_block.sh`, write `delegationMode` to `config.json`. |

### `replace_block.sh` — Idempotent Block Replace Helper

**Interface:**
```bash
bash plugins/replace_block.sh <target_file> <marker_name> <content_file>
```

**Algorithm:**
1. If `target_file` doesn't exist → create it, append content wrapped in `<!-- BEGIN $MARKER -->` / `<!-- END $MARKER -->`.
2. If `target_file` exists:
   a. Check for `<!-- BEGIN $MARKER -->` markers.
   b. If found → use `sed` to delete all content between (and including) every `<!-- BEGIN $MARKER -->` / `<!-- END $MARKER -->` pair. This handles multiple stale blocks.
   c. Also check for legacy marker name `TZRO DELEGATION` and delete those blocks too.
   d. Append new content wrapped in the new markers.
3. If not found → append with markers.
4. Supports `TZRO_DRY_RUN=true` for preview mode.
5. Returns 0 on success, prints colored status.

**Marker format:**
```markdown
<!-- BEGIN TZRO INSTRUCTIONS -->

(tier-specific content here)

<!-- END TZRO INSTRUCTIONS -->
```

### `install_mcp.sh` Changes

**New step numbering:** Steps shift from `[4/4]` to `[5/5]`.

**New Step [3.5/5] — Integration Tier Selection:**
- Interactive mode: presents 3 numbered options with descriptions, defaults to balanced on Enter.
- Non-interactive mode (`TZRO_NON_INTERACTIVE=true`): defaults to `balanced`. Supports `TZRO_DELEGATION_TIER` env var override.
- Sets `INSTRUCTIONS_SOURCE` variable to the tier-specific file path.
- Writes `delegationMode` to `config.json` via `jq` (if `jq` is available; otherwise prints manual config instruction).

**Modified `append_instructions()` function:**
- Replaced with a call to `replace_block.sh` using the `TZRO INSTRUCTIONS` marker name.
- No longer skips when content exists — always replaces.

**Modified `write_instructions()` function:**
- Uses tier-specific `INSTRUCTIONS_SOURCE` instead of the hardcoded path.
- For files that may already exist (re-run scenario), uses `replace_block.sh` to do idempotent replacement.

**Universal Fallback section:**
- Uses `replace_block.sh` for AGENTS.md and CLAUDE.md injection.
- Creates new AGENTS.md with markers if neither file exists.

### Tier-Specific Instruction Content

**Conservative (~80 lines):**
- Offload policy with "may" language, framed as cost optimization
- Higher trigger thresholds (8+ / 12+)
- Only bulk memory ingestion as mandatory delegation
- Codebase exploration and web research listed as "recommended" not mandatory
- Wait protocol (same as balanced)
- Brief DAG prompt templates
- Domain glossary
- Omits: cautionary failure example, Probe Node section, MCP tool taxonomy table

**Balanced (~190 lines):**
- Unchanged from current `tzro-agent-instructions.md`

**Aggressive (~230 lines):**
- Offload policy with "MUST delegate ALL" language
- Lowest trigger thresholds (2+ / 3+)
- Expanded mandatory delegation patterns (adds env inspection, log analysis, multi-file code analysis, data profiling)
- Narrower "Do NOT Delegate" section (only truly single-turn frontier tasks)
- Full MCP tool taxonomy table included inline
- Local inference cost arbitrage section (promotes `tzro_completion` / `tzro_classification`)
- Everything else from balanced

### What's NOT Changed

- `install.sh` — no modifications needed
- `onboard.sh` — already calls `install_mcp.sh` with `TZRO_NON_INTERACTIVE=true`, tier defaults to balanced
- MCP tool descriptions (`delegationHint()` / `runDelegationHint()`) — already adapt based on `delegationMode` in config.json
- The repo's own hand-maintained `AGENTS.md` (the section above the injected block)

## Verification

1. **Fresh install:** Run `install_mcp.sh` → select each tier → verify correct instruction file is injected into AGENTS.md with correct markers.
2. **Re-run (same tier):** Run installer again → verify block is replaced (not duplicated), content identical.
3. **Re-run (different tier):** Run installer, pick different tier → verify old block replaced with new tier content, `delegationMode` updated in config.json.
4. **Legacy cleanup:** Start with an AGENTS.md containing `<!-- BEGIN TZRO DELEGATION -->` markers → run installer → verify old markers are cleaned up and replaced with new `<!-- BEGIN TZRO INSTRUCTIONS -->` markers.
5. **Non-interactive:** Run with `TZRO_NON_INTERACTIVE=true` → verify defaults to balanced. Run with `TZRO_DELEGATION_TIER=aggressive` → verify aggressive tier is used.
6. **Dry run:** Run with `TZRO_DRY_RUN=true` → verify no files modified, correct output printed.
