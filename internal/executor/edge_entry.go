package executor

// edge_entry.go — Incremental Edge Entry accumulation for Probe Thought Chains.
//
// ADR-0059: Each Thought Chain step accumulates an EdgeEntry instead of appending
// to a growing conversation. The full edge log is compiled for synthesis at loop
// termination. This eliminates rolling compaction and sliding window overhead,
// achieving near-zero KV cache prefill per step.

import (
	"fmt"
	"path/filepath"
	"strings"

	"tzro/internal/compactor"
)

// EdgeEntry is the accumulation unit for a single Thought Chain step.
// Captured after each successful tool call and compiled into the synthesis
// context at loop termination. Never injected into the per-step inference prompt.
type EdgeEntry struct {
	StepIndex     int
	ToolName      string
	ToolArgs      string
	ResultSnippet string // Code Skeleton for read_file, full for compact tools
	FullResult    string // stored for SQLite persistence, not used in prompt
}

// maxReadFileSnippet is the character budget for read_file Edge Entry snippets.
// Uses Code Skeleton extraction (signatures preserved, bodies stripped) via the
// Structured Compactor. Other tools keep full output (already compact).
const maxReadFileSnippet = 2000

// NewEdgeEntry creates an EdgeEntry with tool-type-aware truncation (ADR-0059).
//   - read_file: Code Skeleton via CompactContent (signatures preserved)
//   - list_dir, search_files: full output (already compact, typically <5K chars)
//   - introspect_cache, sql_cached_data: full output (evidence data, preserved)
//   - Other tools: capped at maxReadFileSnippet chars
func NewEdgeEntry(stepIndex int, toolName, toolArgs, toolOutput string) EdgeEntry {
	snippet := toolOutput

	switch toolName {
	case "read_file":
		// Code Skeleton: deterministic structural reduction preserving
		// function signatures, type declarations, package/import statements.
		if len(toolOutput) > maxReadFileSnippet {
			snippet = compactor.CompactContent(toolOutput, maxReadFileSnippet)
		}
	case "list_dir", "search_files":
		// Already compact — keep full output
	case "introspect_cache", "sql_cached_data":
		// Evidence data — preserve for synthesis fidelity
	default:
		// Unknown tool — apply generic truncation
		if len(snippet) > maxReadFileSnippet {
			snippet = snippet[:maxReadFileSnippet] + "\n[... truncated ...]"
		}
	}

	return EdgeEntry{
		StepIndex:     stepIndex,
		ToolName:      toolName,
		ToolArgs:      toolArgs,
		ResultSnippet: snippet,
		FullResult:    toolOutput,
	}
}

// BuildBreadcrumbs generates a deterministic, tool-type-aware exploration progress
// summary from the accumulated Edge Entries. Injected into each per-step prompt
// to provide routing memory without growing the conversation history.
//
// For Probe Nodes:
//
//	## Exploration Progress (5/30 steps, 3 successful)
//	Files read: probe.go, executor.go, ready_queue.go
//	Dirs listed: internal/executor/ (1 call)
//	Searches: "EdgeThought" (1 call)
//
// For Analyze Nodes:
//
//	## Analysis Progress (4/15 steps, 3 successful)
//	Schema inspected: cache_1784607195509971000
//	Queries: "SELECT COUNT(*)..." (2 calls)
func BuildBreadcrumbs(entries []EdgeEntry, stepBudget int, isAnalyze bool) string {
	if len(entries) == 0 {
		return ""
	}

	var filesRead []string
	var dirsListed int
	var searchCount int
	var introspected []string
	var queryCount int
	successCount := 0

	for _, e := range entries {
		successCount++
		switch e.ToolName {
		case "read_file":
			// Extract basename from args for compact display
			name := extractBasename(e.ToolArgs)
			if name != "" && !containsString(filesRead, name) {
				filesRead = append(filesRead, name)
			}
		case "list_dir":
			dirsListed++
		case "search_files":
			searchCount++
		case "introspect_cache":
			cacheId := extractCacheId(e.ToolArgs)
			if cacheId != "" && !containsString(introspected, cacheId) {
				introspected = append(introspected, cacheId)
			}
		case "sql_cached_data":
			queryCount++
		}
	}

	var sb strings.Builder

	if isAnalyze {
		sb.WriteString(fmt.Sprintf("## Analysis Progress (%d/%d steps, %d successful)\n",
			len(entries), stepBudget, successCount))
		if len(introspected) > 0 {
			fmt.Fprintf(&sb, "Schema inspected: %s\n", strings.Join(introspected, ", "))
		}
		if queryCount > 0 {
			sb.WriteString(fmt.Sprintf("Queries run: %d\n", queryCount))
		}
	} else {
		sb.WriteString(fmt.Sprintf("## Exploration Progress (%d/%d steps, %d successful)\n",
			len(entries), stepBudget, successCount))
		if len(filesRead) > 0 {
			// Cap file list to avoid bloating breadcrumbs
			display := filesRead
			if len(display) > 15 {
				display = display[:15]
				sb.WriteString(fmt.Sprintf("Files read: %s (+%d more)\n",
					strings.Join(display, ", "), len(filesRead)-15))
			} else {
				fmt.Fprintf(&sb, "Files read: %s\n", strings.Join(display, ", "))
			}
		}
		if dirsListed > 0 {
			sb.WriteString(fmt.Sprintf("Dirs listed: %d\n", dirsListed))
		}
		if searchCount > 0 {
			sb.WriteString(fmt.Sprintf("Searches run: %d\n", searchCount))
		}
	}

	return sb.String()
}

// CompileEdgeLog concatenates Edge Entries into a synthesis-ready context string.
// Used by runSynthesisPass to assemble the exploration findings for the worker model.
//
// Returns the compiled string and a boolean indicating whether the probe-over-edges
// overflow handler should be activated (edge log exceeds maxEdgeLogChars).
const maxEdgeLogChars = 200_000 // ~50K tokens, fits worker model's 64K context

func CompileEdgeLog(entries []EdgeEntry) (string, bool) {
	var sb strings.Builder
	sb.WriteString("## Exploration Log\n\n")

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("### Step %d: %s\n", e.StepIndex, e.ToolName))
		if e.ToolArgs != "" {
			sb.WriteString(fmt.Sprintf("Args: %s\n", e.ToolArgs))
		}
		if e.ResultSnippet != "" {
			sb.WriteString(fmt.Sprintf("Result:\n%s\n", e.ResultSnippet))
		}
		sb.WriteString("\n")
	}

	result := sb.String()
	overflow := len(result) > maxEdgeLogChars

	if overflow {
		// Truncate from the beginning (oldest entries) to fit budget
		// Keep the most recent entries that fit within the budget
		result = truncateEdgeLogFromStart(entries, maxEdgeLogChars)
	}

	return result, overflow
}

// truncateEdgeLogFromStart builds the edge log from the most recent entries
// that fit within the budget, discarding oldest entries first.
func truncateEdgeLogFromStart(entries []EdgeEntry, budget int) string {
	// Build from the end backwards to find where we can start
	var chunks []string
	totalLen := 0
	headerLen := len("## Exploration Log\n\n")

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		chunk := fmt.Sprintf("### Step %d: %s\nArgs: %s\nResult:\n%s\n\n",
			e.StepIndex, e.ToolName, e.ToolArgs, e.ResultSnippet)
		if totalLen+len(chunk)+headerLen > budget {
			break
		}
		chunks = append([]string{chunk}, chunks...)
		totalLen += len(chunk)
	}

	droppedCount := len(entries) - len(chunks)
	var sb strings.Builder
	sb.WriteString("## Exploration Log\n\n")
	if droppedCount > 0 {
		sb.WriteString(fmt.Sprintf("[%d earlier steps omitted to fit context budget]\n\n", droppedCount))
	}
	for _, c := range chunks {
		sb.WriteString(c)
	}
	return sb.String()
}

// extractBasename pulls the filename from a tool args JSON string.
// Handles both {"path":"/full/path/to/file.go"} and bare paths.
func extractBasename(argsStr string) string {
	// Quick extraction: find "path":"..." pattern
	idx := strings.Index(argsStr, `"path"`)
	if idx < 0 {
		return ""
	}
	rest := argsStr[idx+6:]
	// Skip to the value
	valStart := strings.IndexByte(rest, '"')
	if valStart < 0 {
		return ""
	}
	rest = rest[valStart+1:]
	valEnd := strings.IndexByte(rest, '"')
	if valEnd < 0 {
		return ""
	}
	path := rest[:valEnd]
	return filepath.Base(path)
}

// extractCacheId pulls a cache_NNNN identifier from tool args.
func extractCacheId(argsStr string) string {
	idx := strings.Index(argsStr, "cache_")
	if idx < 0 {
		return ""
	}
	end := idx
	for end < len(argsStr) && argsStr[end] != '"' && argsStr[end] != ',' && argsStr[end] != '}' && argsStr[end] != ' ' {
		end++
	}
	return argsStr[idx:end]
}

// containsString checks if a string slice contains a value.
func containsString(slice []string, val string) bool {
	for _, s := range slice {
		if s == val {
			return true
		}
	}
	return false
}

// PruneEdgeContext builds a budget-aware enriched context from edge entry
// FullResults for cloud retry escalation. Unlike CompileEdgeLog (which uses
// pre-truncated ResultSnippets), this uses the full uncompacted tool outputs
// so the cloud model gets actual factual data (URLs, CVE details, search results)
// instead of the lossy snippets that cause hallucination.
//
// Strategy: most-recent-first with proportional per-entry budgets.
//   - Total budget is divided equally among entries (minimum 500 chars each).
//   - Each entry's FullResult is compacted to its per-entry budget.
//   - Entries are assembled newest-first; oldest are dropped if budget is exhausted.
//   - A header notes how many entries were included vs. dropped.
//
// Typical usage: inject the returned string as supplementary context in the
// cloud retry messages to prevent hallucination from thin context.
func PruneEdgeContext(entries []EdgeEntry, budgetChars int) string {
	if len(entries) == 0 || budgetChars <= 0 {
		return ""
	}

	// Reserve space for headers and framing
	const headerReserve = 200
	availableBudget := budgetChars - headerReserve
	if availableBudget < 500 {
		availableBudget = 500
	}

	// Calculate per-entry budget. Minimum 500 chars to keep entries useful.
	perEntryBudget := availableBudget / len(entries)
	if perEntryBudget < 500 {
		perEntryBudget = 500
	}

	// Build from most recent first — newest findings are highest value
	type prunedEntry struct {
		stepIndex int
		toolName  string
		toolArgs  string
		content   string
	}

	var pruned []prunedEntry
	totalChars := 0

	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]

		// Use FullResult when available, fall back to ResultSnippet
		source := e.FullResult
		if source == "" {
			source = e.ResultSnippet
		}
		if source == "" {
			continue
		}

		// Compact to per-entry budget
		content := source
		if len(content) > perEntryBudget {
			content = compactor.CompactContent(content, perEntryBudget)
		}

		// Check total budget
		entryOverhead := len(fmt.Sprintf("### Step %d: %s\nArgs: %s\n", e.StepIndex, e.ToolName, e.ToolArgs))
		if totalChars+len(content)+entryOverhead > availableBudget {
			break
		}

		pruned = append(pruned, prunedEntry{
			stepIndex: e.StepIndex,
			toolName:  e.ToolName,
			toolArgs:  e.ToolArgs,
			content:   content,
		})
		totalChars += len(content) + entryOverhead
	}

	if len(pruned) == 0 {
		return ""
	}

	// Reverse to restore chronological order
	for i, j := 0, len(pruned)-1; i < j; i, j = i+1, j-1 {
		pruned[i], pruned[j] = pruned[j], pruned[i]
	}

	// Build output
	var sb strings.Builder
	dropped := len(entries) - len(pruned)
	if dropped > 0 {
		sb.WriteString(fmt.Sprintf("## Exploration Evidence (%d/%d steps, %d earliest omitted)\n\n", len(pruned), len(entries), dropped))
	} else {
		sb.WriteString(fmt.Sprintf("## Exploration Evidence (%d steps)\n\n", len(pruned)))
	}

	for _, p := range pruned {
		sb.WriteString(fmt.Sprintf("### Step %d: %s\n", p.stepIndex, p.toolName))
		if p.toolArgs != "" {
			sb.WriteString(fmt.Sprintf("Args: %s\n", p.toolArgs))
		}
		sb.WriteString(p.content)
		sb.WriteString("\n\n")
	}

	return sb.String()
}
