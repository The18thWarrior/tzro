package hooks

import (
	"os"
	"path/filepath"
	"strings"
)

// tzroSkillMD is the lightweight SKILL.md content for the tzro skill, following the
// Agent Skills specification (agentskills.io) progressive disclosure pattern.
// Kept ultra-compact (~200 tokens) to minimize per-turn prompt token overhead.
const tzroSkillMD = `---
name: tzro
description: >-
  Token optimization, codebase discovery, and tabular data analysis
  using the tzro CLI. Use when exploring a codebase, reading large files,
  compacting verbose test/build output, or analyzing CSV/TSV/JSON data.
---

# tzro — Local Context Engine

tzro is a fast, zero-dependency local CLI (<50 MB RAM) for token optimization, symbol discovery, and output compaction.

## When to Use tzro

- **Codebase Exploration**: Run ` + "`tzro probe \"<symbol or query>\"`" + ` for sub-millisecond AST symbol discovery (<5ms, 0 cloud tokens).
- **Large Files (>200 lines)**: Run ` + "`tzro skeleton <file>`" + ` to view structural declarations and signatures when a file is too large to read in full.
- **Verbose Command & Test Output**: Run ` + "`tzro compact --run \"<cmd>\"`" + ` or pipe ` + "`... | tzro compact`" + ` to compact compiler diagnostics, test logs, or large JSON responses into high-signal summaries.
- **Tabular Data (CSV, TSV, JSON)**: Ingest with ` + "`tzro ingest <file>`" + ` and query with ` + "`tzro query <table> \"<sql>\"`" + ` instead of dumping large files into context.
- **Blast Radius Analysis**: Run ` + "`tzro impact [files...]`" + ` before modifying shared code to discover callers, dependents, and tests to run.

For complete CLI reference and options (including ` + "`tzro expand`" + `, ` + "`tzro context`" + `, and System 1 graph calls), see ` + "`REFERENCE.md`" + ` in this skill directory.
`

// tzroReferenceMD is the detailed reference manual stored alongside SKILL.md.
// It is read on-demand by agents when detailed CLI flags or graph schemas are needed.
const tzroReferenceMD = `# tzro CLI Reference Manual

Detailed reference for tzro commands, System 1 graph calls, and proxy administration.

## CLI Command Reference

| Command | Purpose | Token Impact |
| :--- | :--- | :--- |
| ` + "`tzro probe \"<query>\"`" + ` | Fast local symbol and file discovery using ripgrep + Tree-sitter AST | **0 cloud tokens (<500 tokens output)** |
| ` + "`tzro context \"<task>\" --budget <n>`" + ` | Assembles ranked, token-budgeted context pack with AST & call graph | **Replaces 5–10 exploration turns** |
| ` + "`tzro impact [files...]`" + ` | Computes change-impact graph, callers, and test coverage before edits | **Prevents broken refactors and missing test runs** |
| ` + "`tzro skeleton <file>`" + ` | Skeletons a code file, eliding function bodies into SHA-256 hashes | **70%–90% token reduction** |
| ` + "`tzro expand <hash>`" + ` | Retrieves elided code body or stored artifact with optional ` + "`--lines`" + ` | Fetches only the required ~20 lines |
| ` + "`tzro compact [--run \"<cmd>\"]`" + ` | Compactor with evidence contract, exit code confidence, 10-line cap | **80% token reduction on test/build logs** |
| ` + "`tzro session save / load`" + ` | Portable, git-aware agent session manifest with freshness validation | **Eliminates full transcript/repo re-reads** |
| ` + "`tzro ingest <file>`" + ` | Import CSV/TSV/JSON into SQLite, returns envelope with table pointer | **97%+ token reduction on tabular data** |
| ` + "`tzro query <table> \"<sql>\"`" + ` | Execute read-only SQL against imported tabular data | Fetches only the query results |
| ` + "`tzro doctor`" + ` | Synthetic health check for proxy, routes, FTS5 engine, and agent hooks | Instant diagnostic verification |
| ` + "`tzro start --port 7878`" + ` | Launches the transparent loopback reverse proxy | **Locks KV-cache prefix (70–99% hit rate)** |
| ` + "`tzro status`" + ` | Displays real-time shielded tokens, memory usage, and proxy metrics | Diagnostic monitoring |

---

## Tabular Data Querying (` + "`tzro query`" + `)

When data is imported via ` + "`tzro ingest`" + `, all columns are stored as ` + "`TEXT`" + ` in local SQLite.
- Numeric comparisons: ` + "`SELECT col, CAST(col AS INTEGER) FROM table WHERE CAST(col AS REAL) > 10.0`" + `
- Aggregations: ` + "`SELECT category, COUNT(*), AVG(CAST(price AS REAL)) FROM table GROUP BY category`" + `
- Results are returned in clean Markdown table format.

---

## System 1 Graph Calls (` + "`tzro execute`" + `)

When ` + "`TZRO_EXPERIMENTAL_RUNTIMES=1`" + ` is configured, declarative graph DAGs can be executed locally without cloud API calls:

` + "```sh" + `
tzro execute graph.json
echo '{"version":"3.0","task_id":"t1","nodes":[...]}' | tzro execute -
` + "```" + `

### Graph Structure
- ` + "`version`" + `: "3.0"
- ` + "`task_id`" + `: Identifier string
- ` + "`nodes`" + `: Array of node objects, each containing ` + "`id`" + `, ` + "`type`" + `, and input/configuration:
  - **` + "`tool`" + `**: Executes a tzro CLI or system tool (` + "`probe`" + `, ` + "`skeleton`" + `, ` + "`search`" + `, ` + "`bash`" + `).
  - **` + "`decision`" + `**: Evaluates a question locally via ` + "`bin/jev-score`" + ` (` + "`choice`" + `, ` + "`score`" + `, ` + "`noul`" + `).
  - **` + "`extract`" + `**: Extracts typed spans (e.g. ` + "`file_path`" + `) from text via GLiNER worker.
  - **` + "`group`" + `**: Groups child nodes with fanout or conditional execution.
- References between nodes use JSON pointers: ` + "`{\"$ref\": \"/nodes/node-id/output/stdout\"}`" + `.
`

// WriteTzroSkill writes the tzro SKILL.md and REFERENCE.md to the given skills directory.
// It creates <skillsDir>/tzro/SKILL.md and <skillsDir>/tzro/REFERENCE.md following the
// Agent Skills specification (agentskills.io).
// If files already exist, they are overwritten (idempotent re-runs).
// Parent directories are created as needed.
func WriteTzroSkill(skillsDir string) error {
	targetDir := filepath.Join(skillsDir, "tzro")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(targetDir, "REFERENCE.md"), []byte(tzroReferenceMD), 0644); err != nil {
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
