package executor

import (
	"encoding/json"
	"testing"
)

func TestExtractToolArguments_NestedToolArgsMergesOuterKeys(t *testing.T) {
	// Reproduces the cache_function_index benchmark bug:
	// ADR-0030 splice puts "content" in both the outer map and tool_arguments,
	// but "path" only at the outer level. The unwrap loop must merge outer keys
	// into the inner map so "path" is preserved.
	input := map[string]interface{}{
		"content": "# Function Index\n\n## Types\n\n```go\ntype Foo struct {\n\tBar string\n}\n```",
		"path":    ".scratch/benchmark/test/function_index.md",
		"tool_arguments": map[string]interface{}{
			"content": "# Function Index\n\n## Types\n\n```go\ntype Foo struct {\n\tBar string\n}\n```",
		},
	}

	rawJSON, _ := json.Marshal(input)
	raw := "Execute tool 'write_file' using the structured arguments [Local Tactician] " + string(rawJSON)

	args := extractToolArguments(raw)

	// Must have both content and path after unwrap
	if _, ok := args["content"]; !ok {
		t.Error("expected 'content' key to survive unwrap")
	}
	if _, ok := args["path"]; !ok {
		t.Error("expected 'path' key to survive unwrap — outer-level key was lost during tool_arguments descent")
	}

	pathVal, ok := args["path"].(string)
	if !ok || pathVal != ".scratch/benchmark/test/function_index.md" {
		t.Errorf("path = %q, want %q", pathVal, ".scratch/benchmark/test/function_index.md")
	}

	// tool_arguments key itself should not survive
	if _, ok := args["tool_arguments"]; ok {
		t.Error("tool_arguments key should have been unwrapped away")
	}
}

func TestExtractToolArguments_InnerValuesTakePrecedence(t *testing.T) {
	// When the same key exists at both levels, inner (tool_arguments) wins
	input := map[string]interface{}{
		"content": "outer content (stale)",
		"path":    "/outer/path",
		"tool_arguments": map[string]interface{}{
			"content": "inner content (fresh)",
			"path":    "/inner/path",
		},
	}

	rawJSON, _ := json.Marshal(input)
	raw := "prefix " + string(rawJSON)

	args := extractToolArguments(raw)

	contentVal, _ := args["content"].(string)
	if contentVal != "inner content (fresh)" {
		t.Errorf("content = %q, want inner value to take precedence", contentVal)
	}

	pathVal, _ := args["path"].(string)
	if pathVal != "/inner/path" {
		t.Errorf("path = %q, want inner value to take precedence", pathVal)
	}
}

func TestExtractToolArguments_TripleNestedUnwrap(t *testing.T) {
	// Triple nesting: outer keys at all levels should merge inward
	input := map[string]interface{}{
		"outerOnly": "from-outer",
		"tool_arguments": map[string]interface{}{
			"midOnly": "from-mid",
			"tool_arguments": map[string]interface{}{
				"innerOnly": "from-inner",
			},
		},
	}

	rawJSON, _ := json.Marshal(input)
	args := extractToolArguments(string(rawJSON))

	if v, _ := args["outerOnly"].(string); v != "from-outer" {
		t.Errorf("outerOnly = %q, want 'from-outer'", v)
	}
	if v, _ := args["midOnly"].(string); v != "from-mid" {
		t.Errorf("midOnly = %q, want 'from-mid'", v)
	}
	if v, _ := args["innerOnly"].(string); v != "from-inner" {
		t.Errorf("innerOnly = %q, want 'from-inner'", v)
	}
}

func TestExtractToolArguments_LargeMarkdownContent(t *testing.T) {
	// Simulates the exact benchmark scenario: large markdown content with
	// Go code blocks containing { and } characters, properly JSON-escaped
	// by json.MarshalIndent in the ADR-0030 splice.
	largeContent := "# Cache Package Function Index\n\n" +
		"## Exported Types\n\n" +
		"**`type CacheEnvelope struct`**\n" +
		"```go\ntype CacheEnvelope struct {\n" +
		"\tCacheID      string                 `json:\"cacheId\"`\n" +
		"\tDataType     string                 `json:\"dataType\"`\n" +
		"}\n```\n\n" +
		"**`type CacheStore interface`**\n" +
		"```go\ntype CacheStore interface {\n" +
		"\tStore(ctx context.Context, rawPayload string) (string, string, error)\n" +
		"}\n```\n"

	input := map[string]interface{}{
		"content": largeContent,
		"path":    ".scratch/benchmark/results/function_index.md",
		"tool_arguments": map[string]interface{}{
			"content": largeContent,
		},
	}

	rawJSON, _ := json.MarshalIndent(input, "", "  ")
	raw := "Execute tool 'write_file' using the structured arguments extracted by the validator node [Local Tactician] " + string(rawJSON)

	args := extractToolArguments(raw)

	if _, ok := args["path"]; !ok {
		t.Fatal("expected 'path' key — large markdown content should not break JSON parsing or key merging")
	}

	pathVal, _ := args["path"].(string)
	if pathVal != ".scratch/benchmark/results/function_index.md" {
		t.Errorf("path = %q, want '.scratch/benchmark/results/function_index.md'", pathVal)
	}

	contentVal, _ := args["content"].(string)
	if contentVal != largeContent {
		t.Errorf("content length = %d, want %d", len(contentVal), len(largeContent))
	}
}
