package executor

import (
	"strings"
	"testing"
)

func TestParseToolCallsFromContent_QwenXMLTags(t *testing.T) {
	content := `I'll search for the relevant information.

<tool_call>{"name": "web_search", "arguments": {"query": "golang security CVE 2024 2025"}}</tool_call>
`
	allowed := []ReActToolDef{
		{Type: "function", Function: ReActFunctionDef{Name: "web_search"}},
		{Type: "function", Function: ReActFunctionDef{Name: "web_browse"}},
	}
	result := parseToolCallsFromContent(content, allowed)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Name != "web_search" {
		t.Errorf("expected tool name 'web_search', got %q", result[0].Name)
	}
	if q, ok := result[0].Arguments["query"].(string); !ok || q != "golang security CVE 2024 2025" {
		t.Errorf("expected query argument, got %v", result[0].Arguments)
	}
}

func TestParseToolCallsFromContent_RawJSON(t *testing.T) {
	// Exact pattern from the benchmark output
	content := `go
{"name": "web_search", "arguments": {"query": "golang language security releases 2024 2025 CVE"}}
{
"name": "web_search",
"args": {
"query": "golang language security releases 2024 2025 CVE"
}
}

## References & Verified Sources
`
	allowed := []ReActToolDef{
		{Type: "function", Function: ReActFunctionDef{Name: "web_search"}},
	}
	result := parseToolCallsFromContent(content, allowed)
	if len(result) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(result))
	}
	for i, tc := range result {
		if tc.Name != "web_search" {
			t.Errorf("call %d: expected 'web_search', got %q", i, tc.Name)
		}
		if _, ok := tc.Arguments["query"]; !ok {
			t.Errorf("call %d: missing 'query' argument", i)
		}
	}
}

func TestParseToolCallsFromContent_CodeBlock(t *testing.T) {
	content := "Let me search for that.\n\n```json\n{\"name\": \"web_browse\", \"arguments\": {\"url\": \"https://example.com\"}}\n```\n\nI'll analyze the results."
	allowed := []ReActToolDef{
		{Type: "function", Function: ReActFunctionDef{Name: "web_browse"}},
	}
	result := parseToolCallsFromContent(content, allowed)
	if len(result) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(result))
	}
	if result[0].Name != "web_browse" {
		t.Errorf("expected 'web_browse', got %q", result[0].Name)
	}
}

func TestParseToolCallsFromContent_UnknownToolRejected(t *testing.T) {
	content := `{"name": "dangerous_tool", "arguments": {"path": "/etc/passwd"}}`
	allowed := []ReActToolDef{
		{Type: "function", Function: ReActFunctionDef{Name: "web_search"}},
	}
	result := parseToolCallsFromContent(content, allowed)
	if len(result) != 0 {
		t.Fatalf("expected 0 tool calls (rejected unknown tool), got %d", len(result))
	}
}

func TestParseToolCallsFromContent_NoToolCalls(t *testing.T) {
	content := "Here is a comprehensive summary of the local AI landscape..."
	allowed := []ReActToolDef{
		{Type: "function", Function: ReActFunctionDef{Name: "web_search"}},
	}
	result := parseToolCallsFromContent(content, allowed)
	if len(result) != 0 {
		t.Fatalf("expected 0 tool calls, got %d", len(result))
	}
}

func TestStripToolCallText(t *testing.T) {
	content := `I'll search now.

<tool_call>{"name": "web_search", "arguments": {"query": "test"}}</tool_call>

Let me analyze.`
	stripped := stripToolCallText(content)
	if stripped == "" {
		t.Fatal("stripped content should not be empty")
	}
	if strings.Contains(stripped, "<tool_call>") || strings.Contains(stripped, "</tool_call>") {
		t.Errorf("stripped content still contains tool_call tags: %q", stripped)
	}
}

