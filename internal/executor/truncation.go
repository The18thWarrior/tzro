package executor

// truncation.go — Intelligent content-aware truncation for Probe synthesis context.
//
// When a probe's synthesis pass includes raw tool outputs, the total context can
// exceed the local model's 64K context window. This module provides content-type-aware
// truncation that preserves the most valuable information:
//
//   - Code files: Truncate at the lowest bracket nesting level, keeping function
//     names and doc comments. 500-char floor per file, but function signatures
//     are always retained even if that exceeds the floor.
//   - Tabular data: Keep 3 sample rows plus summary statistics.
//   - Text/prose: Middle-out truncation (keep head + tail, elide middle).
//
// Truncation starts from the oldest tool results and stops once total context
// is within budget, preserving the most recent results intact.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// maxSynthesisContextChars is the character budget for probe synthesis context.
// Set to ~40K tokens ≈ 160K chars (at ~4 chars/token), leaving ample context
// for the model's output generation.
const maxSynthesisContextChars = 160000

// ContentType classifies tool output for type-appropriate truncation.
type ContentType int

const (
	ContentCode    ContentType = iota // Source code (Go, Python, JS, etc.)
	ContentTabular                    // CSV, TSV, JSON arrays, SQL results
	ContentText                       // Prose, markdown, logs, mixed content
)

// codeFloorChars is the minimum chars to retain per code file during truncation.
// Function signatures may exceed this floor and are always retained.
const codeFloorChars = 500

// tabularSampleRows is the number of sample rows to keep for tabular data.
const tabularSampleRows = 3

// middleOutKeepLines is the number of lines to keep from head and tail of text content.
const truncMiddleOutKeepLines = 30

// classifyContent heuristically determines the content type of tool output.
func classifyContent(content string) ContentType {
	// Check for tabular patterns first (most specific)
	if looksTabular(content) {
		return ContentTabular
	}
	// Check for code patterns
	if looksLikeCode(content) {
		return ContentCode
	}
	return ContentText
}

// looksTabular checks for CSV/TSV/JSON-array/table patterns.
func looksTabular(content string) bool {
	lines := strings.SplitN(content, "\n", 5)
	if len(lines) < 2 {
		return false
	}

	// CSV/TSV: consistent delimiter counts across lines
	for _, delim := range []string{",", "\t", "|"} {
		firstCount := strings.Count(lines[0], delim)
		if firstCount >= 2 {
			matches := 0
			for _, line := range lines[1:] {
				if strings.Count(line, delim) == firstCount {
					matches++
				}
			}
			if matches >= len(lines[1:])-1 {
				return true
			}
		}
	}

	// JSON array
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "[{") || strings.HasPrefix(trimmed, "[\n") {
		return true
	}

	return false
}

// looksLikeCode checks for source code patterns.
func looksLikeCode(content string) bool {
	codeIndicators := 0
	lines := strings.SplitN(content, "\n", 30)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Function declarations
		if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "interface ") {
			codeIndicators += 2
		}
		// Braces and brackets
		if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "}") {
			codeIndicators++
		}
		// Import/package statements
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "package ") ||
			strings.HasPrefix(trimmed, "from ") || strings.HasPrefix(trimmed, "#include") {
			codeIndicators += 2
		}
		// Comments
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "\"\"\"") {
			codeIndicators++
		}
	}

	return codeIndicators >= 4
}

// TruncateToolOutput applies content-aware truncation to a single tool output.
// Returns the truncated content. If content is already within targetChars,
// it is returned unchanged.
func TruncateToolOutput(content string, targetChars int) string {
	if utf8.RuneCountInString(content) <= targetChars {
		return content
	}

	contentType := classifyContent(content)
	switch contentType {
	case ContentCode:
		return truncateCode(content, targetChars)
	case ContentTabular:
		return truncateTabular(content, targetChars)
	default:
		return truncateTextMiddleOut(content, targetChars)
	}
}

// truncateCode truncates source code by removing bodies of deeply nested blocks
// while preserving function signatures, doc comments, and top-level structure.
// Starts removing from the deepest nesting level and works upward until within budget.
func truncateCode(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}

	// Calculate nesting depth for each line
	type lineInfo struct {
		text  string
		depth int
		keep  bool // true = always keep (signature, comment, package/import)
	}

	infos := make([]lineInfo, len(lines))
	currentDepth := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track depth changes
		opens := strings.Count(line, "{") - strings.Count(line, "}")
		if opens < 0 {
			currentDepth += opens
			if currentDepth < 0 {
				currentDepth = 0
			}
		}

		infos[i] = lineInfo{
			text:  line,
			depth: currentDepth,
			keep:  isCodeSignatureLine(trimmed),
		}

		if opens > 0 {
			currentDepth += opens
		}
	}

	// Find the maximum depth
	maxDepth := 0
	for _, info := range infos {
		if info.depth > maxDepth {
			maxDepth = info.depth
		}
	}

	// Progressively remove lines from deepest nesting level upward
	for depth := maxDepth; depth >= 1; depth-- {
		var result []string
		elided := false
		for _, info := range infos {
			if info.depth >= depth && !info.keep {
				if !elided {
					result = append(result, fmt.Sprintf("    // ... [%d lines at depth %d elided] ...", 1, depth))
					elided = true
				}
				continue
			}
			elided = false
			result = append(result, info.text)
		}

		joined := strings.Join(result, "\n")
		if utf8.RuneCountInString(joined) <= targetChars {
			return joined
		}
	}

	// If still too large after removing all nested content, fall back to
	// keeping the floor amount from the top (package + imports + signatures)
	// plus a tail summary
	if utf8.RuneCountInString(content) > codeFloorChars {
		// Collect all signature lines regardless of budget
		var sigLines []string
		for _, info := range infos {
			if info.keep {
				sigLines = append(sigLines, info.text)
			}
		}

		sigText := strings.Join(sigLines, "\n")
		if utf8.RuneCountInString(sigText) >= targetChars {
			// Even signatures alone exceed budget — just return what fits
			runes := []rune(sigText)
			return string(runes[:targetChars-50]) + "\n// ... [truncated] ..."
		}

		// Add as many top lines as possible after signatures
		remaining := targetChars - utf8.RuneCountInString(sigText) - 100
		if remaining > codeFloorChars {
			remaining = codeFloorChars
		}
		topLines := strings.SplitN(content, "\n", remaining/40+1)
		top := strings.Join(topLines, "\n")
		if utf8.RuneCountInString(top) > remaining {
			runes := []rune(top)
			top = string(runes[:remaining])
		}

		totalLines := len(lines)
		return top + fmt.Sprintf("\n// ... [%d lines elided] ...\n\n// === Function Signatures ===\n", totalLines-len(topLines)) + sigText
	}

	return content
}

// isCodeSignatureLine returns true if the line is a function signature,
// type declaration, doc comment, or package/import statement that should
// always be preserved during code truncation.
func isCodeSignatureLine(trimmed string) bool {
	// Function/method declarations
	if strings.HasPrefix(trimmed, "func ") {
		return true
	}
	// Type/interface/struct declarations
	if strings.HasPrefix(trimmed, "type ") {
		return true
	}
	// Package and import
	if strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "import ") {
		return true
	}
	// Doc comments (Go-style)
	if strings.HasPrefix(trimmed, "// ") && len(trimmed) > 3 {
		// Heuristic: doc comments tend to start with uppercase or describe a symbol
		rest := trimmed[3:]
		if len(rest) > 0 && rest[0] >= 'A' && rest[0] <= 'Z' {
			return true
		}
	}
	// Python/JS function declarations
	if strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
		strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "export ") {
		return true
	}
	return false
}

// truncateTabular keeps the header row, first N sample rows, and a summary line.
func truncateTabular(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= tabularSampleRows+2 {
		return content
	}

	// Keep header + sample rows
	var result []string
	headerEnd := 1
	// Check for separator line (e.g., "---" or "===")
	if len(lines) > 1 && (strings.Contains(lines[1], "---") || strings.Contains(lines[1], "===")) {
		headerEnd = 2
	}

	result = append(result, lines[:headerEnd]...)
	endSample := headerEnd + tabularSampleRows
	if endSample > len(lines) {
		endSample = len(lines)
	}
	result = append(result, lines[headerEnd:endSample]...)

	totalRows := len(lines) - headerEnd
	omitted := totalRows - tabularSampleRows
	if omitted > 0 {
		result = append(result, fmt.Sprintf("[... %d more rows omitted (total: %d rows) ...]", omitted, totalRows))
	}

	// Add last row as additional sample if available
	if len(lines) > endSample+1 {
		result = append(result, lines[len(lines)-2])
	}

	joined := strings.Join(result, "\n")
	if utf8.RuneCountInString(joined) > targetChars {
		// Still too large — fall back to middle-out
		return truncateTextMiddleOut(content, targetChars)
	}
	return joined
}

// truncateTextMiddleOut keeps the first and last N lines of text content,
// replacing the middle with a summary marker.
func truncateTextMiddleOut(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	keepLines := truncMiddleOutKeepLines

	if len(lines) <= keepLines*2 {
		// Not enough lines to middle-out — just truncate from end
		runes := []rune(content)
		if len(runes) <= targetChars {
			return content
		}
		return string(runes[:targetChars-50]) + "\n[... truncated ...]"
	}

	head := strings.Join(lines[:keepLines], "\n")
	tail := strings.Join(lines[len(lines)-keepLines:], "\n")
	omitted := len(lines) - keepLines*2

	result := head + fmt.Sprintf("\n[... %d lines omitted ...]\n", omitted) + tail

	if utf8.RuneCountInString(result) > targetChars {
		// Even head+tail exceeds budget — reduce keepLines
		runes := []rune(result)
		return string(runes[:targetChars-50]) + "\n[... truncated ...]"
	}
	return result
}

// TruncateSynthesisContext takes the full list of thought steps with tool outputs
// and returns a truncated version that fits within maxSynthesisContextChars.
// Truncation starts from the oldest tool results and stops once total size is within
// budget, preserving the most recent results intact.
func TruncateSynthesisContext(steps []SynthesisStep) string {
	// First pass: compute total size
	totalChars := 0
	for _, s := range steps {
		totalChars += utf8.RuneCountInString(s.Thought) + utf8.RuneCountInString(s.ToolOutput) + 50 // overhead
	}

	if totalChars <= maxSynthesisContextChars {
		// Everything fits — return all content
		return formatSynthesisSteps(steps, -1)
	}

	// Need to truncate. Work backwards from budget.
	// Start by calculating how much we need to cut.
	overBudget := totalChars - maxSynthesisContextChars

	// Truncate oldest steps first
	truncatedSteps := make([]SynthesisStep, len(steps))
	copy(truncatedSteps, steps)

	for i := 0; i < len(truncatedSteps) && overBudget > 0; i++ {
		outputLen := utf8.RuneCountInString(truncatedSteps[i].ToolOutput)
		if outputLen == 0 {
			continue
		}

		// Calculate how much to keep for this step's tool output
		targetLen := outputLen - overBudget
		if targetLen < codeFloorChars {
			targetLen = codeFloorChars
		}

		if targetLen < outputLen {
			truncatedSteps[i].ToolOutput = TruncateToolOutput(truncatedSteps[i].ToolOutput, targetLen)
			saved := outputLen - utf8.RuneCountInString(truncatedSteps[i].ToolOutput)
			overBudget -= saved
		}
	}

	return formatSynthesisSteps(truncatedSteps, -1)
}

// SynthesisStep holds the data for one probe exploration step,
// used by TruncateSynthesisContext.
type SynthesisStep struct {
	StepIndex  int
	Thought    string
	ToolOutput string
}

// formatSynthesisSteps formats steps into the context string for synthesis.
// If maxSteps >= 0, only the last maxSteps steps are included.
func formatSynthesisSteps(steps []SynthesisStep, maxSteps int) string {
	start := 0
	if maxSteps >= 0 && len(steps) > maxSteps {
		start = len(steps) - maxSteps
	}

	var sb strings.Builder
	for _, s := range steps[start:] {
		sb.WriteString(fmt.Sprintf("Step %d: %s\n", s.StepIndex, s.Thought))
		if s.ToolOutput != "" {
			sb.WriteString(fmt.Sprintf("  Tool Output:\n%s\n", s.ToolOutput))
		}
	}
	return sb.String()
}
