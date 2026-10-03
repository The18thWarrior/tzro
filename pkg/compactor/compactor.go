package compactor

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"tzro/pkg/store"
)

// SmartJSONCrusher compresses JSON arrays of uniform objects into compact tabular format.
func SmartJSONCrusher(input string) string {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "[") || !strings.HasSuffix(trimmed, "]") {
		return input
	}

	var arr []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
		return input
	}

	if len(arr) < 2 {
		return input
	}

	// Collect unique keys in deterministic order
	keySet := make(map[string]bool)
	for _, item := range arr {
		for k := range item {
			keySet[k] = true
		}
	}

	if len(keySet) == 0 {
		return input
	}

	var keys []string
	for k := range keySet {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Compressed JSON Table (%d rows)\n", len(arr)))
	sb.WriteString("| ")
	sb.WriteString(strings.Join(keys, " | "))
	sb.WriteString(" |\n")
	sb.WriteString("|")
	sb.WriteString(strings.Repeat(" --- |", len(keys)))
	sb.WriteString("\n")

	for _, item := range arr {
		var row []string
		for _, k := range keys {
			val, ok := item[k]
			if !ok {
				row = append(row, "-")
			} else {
				row = append(row, fmt.Sprintf("%v", val))
			}
		}
		sb.WriteString("| ")
		sb.WriteString(strings.Join(row, " | "))
		sb.WriteString(" |\n")
	}

	return sb.String()
}

var (
	// Regex patterns for runtime / framework internal frames
	goRuntimeFrameRe    = regexp.MustCompile(`(?m)^\s*(runtime/|testing\.go|net/http/server\.go).*$`)
	nodeInternalFrameRe = regexp.MustCompile(`(?m)^\s*at\s+.*\(node:internal/.*$`)
	pyFrameworkFrameRe  = regexp.MustCompile(`(?m)^\s*File ".*/lib/python.*/site-packages/.*", line \d+, in .*$`)
	goStdFunctionRe     = regexp.MustCompile(`^(?:runtime\.|internal/|testing\.|net\.|net/http\.)[^\n]*\(`)
	goStdLocationRe     = regexp.MustCompile(`(?:^|/)src/(?:runtime|internal|testing|net)/[^\n]+:\d+(?: |$)`)
)

// StackTraceElider trims boilerplate framework stack frames, preserving application code and error messages.
func StackTraceElider(input string) string {
	lines := strings.Split(input, "\n")
	var pruned []string
	elidedCount := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		// Native Go traces put the function and source location on separate lines.
		// Require both a standard-library function and location to avoid hiding
		// application packages that happen to use names such as runtime or net.
		if i+1 < len(lines) && goStdFunctionRe.MatchString(trimmed) && goStdLocationRe.MatchString(strings.TrimSpace(lines[i+1])) {
			elidedCount++
			i++
			continue
		}
		isBoilerplate := goRuntimeFrameRe.MatchString(trimmed) ||
			nodeInternalFrameRe.MatchString(trimmed) ||
			pyFrameworkFrameRe.MatchString(trimmed)

		if isBoilerplate {
			elidedCount++
			continue
		}

		if elidedCount > 0 {
			pruned = append(pruned, fmt.Sprintf("    ... [%d framework/runtime frames elided] ...", elidedCount))
			elidedCount = 0
		}
		pruned = append(pruned, line)
	}

	if elidedCount > 0 {
		pruned = append(pruned, fmt.Sprintf("    ... [%d framework/runtime frames elided] ...", elidedCount))
	}

	return strings.Join(pruned, "\n")
}

// IsSSEPayload returns true if the text begins with Server-Sent Events markers.
func IsSSEPayload(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "event:") || strings.HasPrefix(trimmed, "data:")
}

// MinArtifactThreshold is the minimum byte size of an input before an artifact
// is stored in SQLite and an expansion header is prepended to the compacted output.
// Inputs smaller than this threshold that are compacted still return the compacted text,
// but omit the artifact header to prevent token inflation on small outputs.
const MinArtifactThreshold = 200

// GetArtifactThreshold returns the configured minimum artifact threshold.
// Defaults to MinArtifactThreshold (200 bytes) if unset or unparseable.
func GetArtifactThreshold() int {
	if v := os.Getenv("TZRO_COMPACT_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return MinArtifactThreshold
}

// CompactLog applies log and test output pruning.
func CompactLog(input string) string {
	return CompactWithArtifact(input, "", nil)
}

// CompactWithArtifact saves full uncompressed original to Store and returns compacted view with artifact ID.
func CompactWithArtifact(input, workspace string, s *store.Store) string {
	// Guard: Executable protocol payloads (SSE stream chunks) must never be converted or mutated
	if IsSSEPayload(input) {
		return input
	}

	var compacted string
	// First check if it is raw JSON
	if strings.HasPrefix(strings.TrimSpace(input), "[") {
		crushed := SmartJSONCrusher(input)
		if len(crushed) < len(input) {
			compacted = crushed
		}
	}

	if compacted == "" {
		// Apply stack trace elision
		elided := StackTraceElider(input)
		if len(elided) < len(input) {
			compacted = elided
		}
	}

	// If no compaction occurred (output not reduced), return raw input untouched.
	// Never create an artifact or attach a retention header when nothing was elided.
	if compacted == "" || len(compacted) >= len(input) {
		return input
	}

	// If input was compacted, only store an artifact and attach the expansion header
	// if the original input meets the minimum threshold.
	threshold := GetArtifactThreshold()
	if s != nil && len(input) >= threshold {
		id, err := s.PutArtifact(&store.Artifact{
			Type:             "log",
			Workspace:        workspace,
			TransformVersion: "v2.0",
			Body:             input,
		})
		if err == nil {
			header := fmt.Sprintf("// [Tzro Artifact: %s | Full original retained (run `tzro expand %s` to retrieve)]\n", id, id)
			return header + compacted
		}
	}

	return compacted
}
