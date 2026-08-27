package hooks

import (
	"os"
	"path/filepath"
	"strings"
)

// tzroSkillMD is the SKILL.md content for the tzro skill, following the
// Agent Skills specification (agentskills.io). This is the canonical format
// supported by Copilot, Pi-Coder, Antigravity, and Hermes via .agents/skills/.
const tzroSkillMD = `---
name: tzro
description: >-
  Token optimization, codebase discovery, and tabular data analysis
  using the tzro CLI. Use when exploring a codebase, reading large files,
  compacting verbose test/build output, or analyzing CSV/TSV/JSON data.
  Provides sub-millisecond local symbol search, AST-aware file
  skeletonization, log compaction, and SQL-queryable tabular import.
---

# tzro — The Local Token Shield & Context Optimization Engine

` + "`tzro`" + ` is a compiled, zero-dependency native Go engine (<50 MB RAM) that eliminates cloud API rate limits, locks prompt cache prefixes (guaranteeing 90% cache read discounts), and provides sub-millisecond local codebase discovery (` + "`tzro probe`" + `).

---

## Token Optimization & Discovery Guidelines

When operating in this codebase, coding agents **must** use tzro's local discovery and compaction tools rather than burning quadratic cloud context.

### 1. Codebase Exploration: Use ` + "`tzro probe`" + `
Never make 10–20 sequential cloud tool calls to locate symbols or understand architecture.
- Run ` + "`tzro probe \"<search goal>\"`" + ` via run_command to get exact line numbers, symbol signatures, and content hashes in <5ms with 0 cloud tokens.

### 2. Large File Reads: Use ` + "`tzro skeleton`" + ` & ` + "`tzro expand`" + `
- If you only need to understand the interface, imports, or method signatures of a large file, run ` + "`tzro skeleton <filepath>`" + `.
- If you need to edit a specific method body that was elided (` + "`// [body elided: #hash]`" + `), retrieve only those lines using ` + "`tzro expand <hash>`" + `.

### 3. Log & Test Output Compaction: Pipe through ` + "`tzro compact`" + `
- When running test suites, pipe verbose outputs through ` + "`tzro compact`" + ` to strip redundant runtime stack frames and flatten JSON arrays into compact Markdown tables.

### 4. Tabular Data Analysis: Use ` + "`tzro ingest`" + ` & ` + "`tzro query`" + `
- When you encounter large CSV, TSV, or JSON array data (from files or API responses), import it with ` + "`tzro ingest <file>`" + ` or pipe it via ` + "`cat data.csv | tzro ingest -`" + `.
- This imports the data into a local SQLite table and returns a compact envelope with schema, sample rows, and a table pointer.
- Query the data with SQL: ` + "`tzro query <table> \"SELECT col, COUNT(*) FROM <table> GROUP BY col\"`" + `.
- All columns are stored as TEXT — use ` + "`CAST(col AS INTEGER)`" + ` or ` + "`CAST(col AS REAL)`" + ` for numeric operations.
- **Never** dump a full large CSV into context. Ingest it and query instead — **97%+ prompt token reduction** on tabular workloads.

---

## CLI Reference for Agents

| Command | Purpose | Token Impact |
| :--- | :--- | :--- |
| ` + "`tzro probe \"<query>\"`" + ` | Fast local symbol and file discovery using ripgrep + Tree-sitter AST | **0 cloud tokens (<500 tokens output)** |
| ` + "`tzro skeleton <file>`" + ` | Skeletons a code file, eliding function bodies into SHA-256 hashes | **70%–90% token reduction** |
| ` + "`tzro expand <hash>`" + ` | Retrieves the full original function body from local SQLite | Fetches only the required ~20 lines |
| ` + "`tzro compact`" + ` | Stdin/stdout pipe for log, stack trace, and JSON array compaction | **80% token reduction on test/build logs** |
| ` + "`tzro ingest <file>`" + ` | Import CSV/TSV/JSON into SQLite, returns envelope with table pointer | **97%+ token reduction on tabular data** |
| ` + "`tzro query <table> \"<sql>\"`" + ` | Execute read-only SQL against imported tabular data | Fetches only the query results |
| ` + "`tzro start --port 7878`" + `| Launches the transparent loopback reverse proxy | **Locks KV-cache prefix for 90% discount** |
| ` + "`tzro status`" + ` | Displays real-time shielded tokens, memory usage, and proxy metrics | Diagnostic monitoring |
`

// WriteTzroSkill writes the tzro SKILL.md to the given skills directory.
// It creates <skillsDir>/tzro/SKILL.md following the Agent Skills specification.
// If the file already exists, it is overwritten (idempotent re-runs).
// Parent directories are created as needed.
func WriteTzroSkill(skillsDir string) error {
	targetDir := filepath.Join(skillsDir, "tzro")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(targetDir, "SKILL.md"), []byte(tzroSkillMD), 0644)
}

// --- Legacy marker-based instruction writer (for copilot-instructions.md) ---

const tzroInstructionsBeginMarker = "<!-- BEGIN TZRO INSTRUCTIONS -->"
const tzroInstructionsEndMarker = "<!-- END TZRO INSTRUCTIONS -->"

// WriteTzroInstructions writes the tzro agent instruction block to the given file path.
// If the file already contains the marker block, the section is replaced in-place.
// If the file exists but has no markers, the block is appended.
// If the file does not exist, it is created with just the instruction block.
// Parent directories are created as needed.
func WriteTzroInstructions(filePath string) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return err
	}

	existing, err := os.ReadFile(filePath)
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	block := tzroInstructionsBeginMarker + "\n\n" + tzroSkillMD + tzroInstructionsEndMarker + "\n"

	content := string(existing)
	updated := upsertInstructionBlock(content, block)

	return os.WriteFile(filePath, []byte(updated), 0644)
}

// upsertInstructionBlock replaces the marker-delimited section if it exists,
// otherwise appends the block to the end.
func upsertInstructionBlock(existing, block string) string {
	beginIdx := strings.Index(existing, tzroInstructionsBeginMarker)
	endIdx := strings.Index(existing, tzroInstructionsEndMarker)

	if beginIdx >= 0 && endIdx >= 0 {
		// Replace existing block (end marker + its trailing newline)
		endOfEndMarker := endIdx + len(tzroInstructionsEndMarker)
		if endOfEndMarker < len(existing) && existing[endOfEndMarker] == '\n' {
			endOfEndMarker++
		}
		return existing[:beginIdx] + block + existing[endOfEndMarker:]
	}

	// Append — ensure there's a blank line separator if file has content
	if len(existing) > 0 && !strings.HasSuffix(existing, "\n\n") {
		if !strings.HasSuffix(existing, "\n") {
			existing += "\n"
		}
		existing += "\n"
	}
	return existing + block
}
