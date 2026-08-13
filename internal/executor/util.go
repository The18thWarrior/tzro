package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/templates"
	"tzro/internal/tools"
)

// GetNodeTypeReferenceCard returns a formatted reference card of all registered
// node types. Falls back to the static template if no registry is active.
func GetNodeTypeReferenceCard() string {
	if activeRegistry != nil {
		return activeRegistry.BuildReferenceCard()
	}
	return templates.NodeTypeReferenceCard
}

var comparisonRegex = regexp.MustCompile(`^\s*(.*?)\s*(==|!=|>=|<=|>|<)\s*(.*?)\s*$`)

func evaluateDeterministicCondition(cond string) (bool, bool) {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return false, false
	}

	matches := comparisonRegex.FindStringSubmatch(cond)
	if len(matches) < 4 {
		lowerCond := strings.ToLower(cond)
		if lowerCond == "true" {
			return true, true
		}
		if lowerCond == "false" {
			return false, true
		}
		return false, false
	}

	lhsRaw := strings.TrimSpace(matches[1])
	op := matches[2]
	rhsRaw := strings.TrimSpace(matches[3])

	// Strip quotes if present
	stripQuotes := func(s string) string {
		if len(s) >= 2 {
			if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
				return s[1 : len(s)-1]
			}
		}
		return s
	}

	lhsClean := stripQuotes(lhsRaw)
	rhsClean := stripQuotes(rhsRaw)

	// Check if they are booleans
	parseBool := func(s string) (bool, bool) {
		ls := strings.ToLower(s)
		if ls == "true" {
			return true, true
		}
		if ls == "false" {
			return false, true
		}
		return false, false
	}

	lhsBool, lhsIsBool := parseBool(lhsClean)
	rhsBool, rhsIsBool := parseBool(rhsClean)

	if lhsIsBool && rhsIsBool {
		switch op {
		case "==":
			return lhsBool == rhsBool, true
		case "!=":
			return lhsBool != rhsBool, true
		default:
			return false, false
		}
	}

	// Check if they are numbers
	lhsNum, errL := strconv.ParseFloat(lhsClean, 64)
	rhsNum, errR := strconv.ParseFloat(rhsClean, 64)

	if errL == nil && errR == nil {
		switch op {
		case "==":
			return lhsNum == rhsNum, true
		case "!=":
			return lhsNum != rhsNum, true
		case ">":
			return lhsNum > rhsNum, true
		case ">=":
			return lhsNum >= rhsNum, true
		case "<":
			return lhsNum < rhsNum, true
		case "<=":
			return lhsNum <= rhsNum, true
		}
	}

	// String comparison fallback
	switch op {
	case "==":
		return lhsClean == rhsClean, true
	case "!=":
		return lhsClean != rhsClean, true
	}

	return false, false
}


func isTestingOrBenchmark(ctx context.Context) bool {
	if ctx.Value("is_benchmark") != nil {
		return true
	}
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.") || strings.Contains(arg, "test") {
			return true
		}
	}
	if os.Getenv("TZRO_HEADLESS") == "true" {
		return true
	}
	return false
}

// isCompactionDisabled checks whether the 5-Layer Compaction Pipeline is disabled
// for this execution context. Used by the cloud_dag_raw benchmark condition to
// bypass cache.Process() and pass raw tool output through unmodified.
func isCompactionDisabled(ctx context.Context) bool {
	return ctx.Value("compaction_disabled") != nil
}


func getDelays(ctx context.Context) (time.Duration, time.Duration) {
	cfg := config.Get()
	nodeDelay := time.Duration(cfg.ExecutorNodeDelayMs) * time.Millisecond
	levelDelay := time.Duration(cfg.ExecutorLevelDelayMs) * time.Millisecond

	// Assign historical visual pacing defaults if unset (0)
	if cfg.ExecutorNodeDelayMs == 0 {
		nodeDelay = 800 * time.Millisecond
	}
	if cfg.ExecutorLevelDelayMs == 0 {
		levelDelay = 500 * time.Millisecond
	}

	// Bypass delays for testing and benchmarking
	if isTestingOrBenchmark(ctx) {
		return 0, 0
	}

	return nodeDelay, levelDelay
}

// classifyToolName uses GBNF-constrained local inference to map a hallucinated
// tool name to the closest registered tool. Returns empty string if classification fails.

func classifyToolName(ctx context.Context, hallucinated string, nodeInstructions string) string {
	registeredTools := tools.GetList()
	if len(registeredTools) == 0 {
		return ""
	}
	var toolNames []string
	for _, t := range registeredTools {
		toolNames = append(toolNames, t.Name())
	}

	// Build GBNF-constrained schema with enum of real tool names
	toolNamesJSON, _ := json.Marshal(toolNames)
	schema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"tool": {
				"type": "string",
				"enum": %s
			}
		},
		"required": ["tool"]
	}`, string(toolNamesJSON))

	systemPrompt := "You are a tool name classifier. Given a hallucinated tool name and the node's instructions, select the most appropriate real tool from the available options."
	userPrompt := fmt.Sprintf("Hallucinated tool: %s\nNode instructions: %s\nSelect the real tool that best matches the intent.",
		hallucinated, truncateString(nodeInstructions, 200))

	backend := inference.ActiveBackend
	if backend == nil {
		return ""
	}
	result, err := backend.CallModel(ctx, []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, schema)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Executor] Tool classification failed: %v\n", err)
		return ""
	}

	var parsed struct {
		Tool string `json:"tool"`
	}
	if json.Unmarshal([]byte(result.Content), &parsed) == nil {
		return parsed.Tool
	}
	return ""
}


func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// injectCacheIdIntoRawOutput annotates a raw tool output with a derived cacheId
// so downstream binding resolution can discover the filtered/derived cache.
//
// Red-team FM-3 fix: When filter_where (or any tool) produces output > 12KB,
// the compactor saves it to a new SQLite cache. Without this injection, downstream
// nodes resolve bindings against the original (unfiltered) cacheId because the
// derived cacheId only appears in log lines, not in the stored raw output.
//
// Strategy:
//   - If output is a JSON array: wrap in {"derivedCacheId": "...", "data": [...]}
//   - If output is a JSON object: add "derivedCacheId" field
//   - If output is plain text: prepend a metadata line

func injectCacheIdIntoRawOutput(output string, cacheID string) string {
	trimmed := strings.TrimSpace(output)

	// JSON array — wrap in an envelope
	if strings.HasPrefix(trimmed, "[") {
		return fmt.Sprintf(`{"derivedCacheId":"%s","data":%s}`, cacheID, output)
	}

	// JSON object — inject field at the start
	if strings.HasPrefix(trimmed, "{") {
		return fmt.Sprintf(`{"derivedCacheId":"%s",%s`, cacheID, trimmed[1:])
	}

	// Plain text — prepend metadata line
	return fmt.Sprintf("derivedCacheId: %s\n%s", cacheID, output)
}

