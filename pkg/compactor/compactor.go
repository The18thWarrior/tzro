package compactor

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
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
)

// StackTraceElider trims boilerplate framework stack frames, preserving application code and error messages.
func StackTraceElider(input string) string {
	lines := strings.Split(input, "\n")
	var pruned []string
	elidedCount := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
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

	var artifactID string
	if s != nil {
		id, err := s.PutArtifact(&store.Artifact{
			Type:             "log",
			Workspace:        workspace,
			TransformVersion: "v2.0",
			Body:             input,
		})
		if err == nil {
			artifactID = id
		}
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
		compacted = StackTraceElider(input)
	}

	if artifactID != "" {
		header := fmt.Sprintf("// [Tzro Artifact: %s | Full original retained (run `tzro expand %s` to retrieve)]\n", artifactID, artifactID)
		return header + compacted
	}

	return compacted
}
