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

For a known sequence of steps, use the native ` + "`tzro_execute_graph`" + ` tool or ` + "`tzro execute graph.json --result selected`" + `.
One call runs the workflow locally, passes intermediate results between steps, and returns selected evidence.
Configured System 1 workers can make bounded decisions and extract values without cloud turns. Tool-only graphs need no models.
Use individual commands for single steps. Keep planning and code generation in the host model.

## Best Practices & Efficient Pipelining

To minimize turns and save tokens, pipeline tzro commands within single tool invocations:

- **Tabular Data (CSV, TSV, JSON)**: Name the table during ingest to query in a single turn:
  ` + "`tzro ingest <file> --name <tbl> && tzro query <tbl> \"<sql>\"`" + `
  All columns are TEXT; use ` + "`CAST(col AS INTEGER)`" + ` or ` + "`CAST(col AS REAL)`" + ` for math/ordering.
- **Large Files (>200 lines)**: Run ` + "`tzro skeleton <file>`" + ` to view method signatures and structural declarations without dumping the entire file. Use ` + "`tzro expand <hash>`" + ` only for specific elided method bodies.
- **Verbose Builds & Tests**: Run ` + "`tzro compact --run \"<cmd>\"`" + ` (e.g. ` + "`tzro compact --run \"go test ./...\"`" + `) to capture failure diagnostics within an inline 10-line cap.
- **Codebase Exploration**: Run ` + "`tzro probe \"<symbol>\"`" + ` for sub-millisecond AST symbol discovery (<5ms, 0 cloud tokens) before opening files.
- **Blast Radius**: Run ` + "`tzro impact [files...]`" + ` before modifying shared code to discover callers, dependents, and tests to run.

For full CLI reference, options, and System 1 graph calls (including ` + "`tzro expand`" + ` and ` + "`tzro context`" + `), see ` + "`REFERENCE.md`" + ` in this skill directory.
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
- **Single-turn pipeline**: ` + "`tzro ingest data.csv --name my_data && tzro query my_data \"SELECT ...\"`" + `
- Numeric comparisons: ` + "`SELECT col, CAST(col AS INTEGER) FROM table WHERE CAST(col AS REAL) > 10.0`" + `
- Aggregations: ` + "`SELECT category, COUNT(*), AVG(CAST(price AS REAL)) FROM table GROUP BY category`" + `
- Results are returned in clean Markdown table format.

---

## System 1 Graph Calls (` + "`tzro execute`" + `)

Submit a known local workflow once instead of returning to the cloud model after each step.
The Go executor schedules the graph. Configured System 1 workers supply bounded decisions and extracted values for later steps.
The result contains requested evidence or a yield when a decision misses its confidence threshold.
Set ` + "`accept.min_confidence`" + ` on decision nodes and validate the resulting work. Execution success does not establish answer correctness.
Tool-only graphs work in Standard without models. Decision and extraction nodes require configured workers and ` + "`TZRO_EXPERIMENTAL_RUNTIMES=1`" + `.

` + "```sh" + `
tzro execute graph.json --result selected
cat graph.json | tzro execute - --result selected
` + "```" + `

Native Pi and MCP expose ` + "`tzro_execute_graph({graph: ...})`" + ` directly, without a separate graph-file write.
For exact UTF-8 file text, use a tool node with ` + "`tool: \"read\"`" + ` and ` + "`args: {file, offset?, limit?}`" + `.
Offset and limit are positive line numbers/counts; offset starts at one. Relative paths use the workspace root.
Read returns body, file, start_line, and end_line. Each result and line is limited to 1 MiB; request smaller slices for larger files.
Blocked paths, binary files, and policies requiring redaction fail explicitly. Exact reads do not substitute skeletons.
Select ` + "`/nodes/id/output/body`" + ` for text or ` + "`/nodes/id/output`" + ` for the whole node result.
Query also accepts an optional file to import. Its table name is the basename without extension, with spaces and hyphens replaced by underscores.
For example, import a file and compute a result in one call:

` + "```json" + `
{
  "version": "3.0",
  "task_id": "summarize-data",
  "nodes": [
    {"id": "load", "type": "tool", "tool": "ingest", "args": {"file": "data.csv", "table": "measurements"}},
    {"id": "summary", "type": "tool", "tool": "query", "depends_on": ["load"], "args": {"sql": "SELECT COUNT(*) AS rows FROM measurements"}}
  ],
  "returns": ["/nodes/summary/output/rows"]
}
` + "```" + `

Use ` + "`depends_on`" + ` for every required ordering dependency. Wire argument or input values with ` + "`{\"$ref\":\"/nodes/id/output/field\"}`" + `.
Select only useful ` + "`returns`" + `. Intermediate output is retained locally with an artifact ID for ` + "`tzro expand`" + `.
Without explicit returns, selected mode returns terminal outputs and failures. Large results return an expansion pointer.
Local storage failure preserves the full response. Graph commands run with the caller's existing permissions.

### Graph Structure
- ` + "`version`" + `: "3.0"
- ` + "`task_id`" + `: Identifier string
- ` + "`nodes`" + `: Array of node objects, each containing ` + "`id`" + `, ` + "`type`" + `, and input/configuration:
  - **` + "`tool`" + `**: Executes ` + "`probe`" + `, ` + "`skeleton`" + `, ` + "`expand`" + `, ` + "`search`" + `, ` + "`ingest`" + `, ` + "`query`" + `, or ` + "`bash`" + `. Expand accepts an id and returns body. Shell nodes can run other CLI commands.
  - **` + "`decision`" + `**: Evaluates a question locally via ` + "`bin/jev-score`" + ` (` + "`choice`" + `, ` + "`score`" + `, ` + "`noul`" + `).
  - **` + "`extract`" + `**: Extracts typed spans (e.g. ` + "`file_path`" + `) from text via GLiNER worker.
  - **` + "`group`" + `**: Evaluates a template for each item with bounded concurrency. Each child's input receives item and index.
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
