package tools

import (
	"testing"
)

func TestProactivityLevel_BuiltInToolLevels(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	tests := []struct {
		name     string
		expected int
	}{
		{"read_file", PLevelObserve},
		{"list_dir", PLevelObserve},
		{"search_files", PLevelObserve},
		{"save_memory", PLevelPrepare},
		{"recall_memory", PLevelPrepare},
		{"web_search", PLevelExternalSideEffect},
		{"create_task", PLevelReversibleAction},
		{"query", PLevelReversibleAction},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GetProactivityLevel(tc.name)
			if got != tc.expected {
				t.Errorf("GetProactivityLevel(%q) = %d, want %d", tc.name, got, tc.expected)
			}
		})
	}
}

func TestProactivityLevel_MCPToolAdapterDefaultsToL3(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	mcpTool := &MCPToolAdapter{
		name:        "mcp_custom_tool",
		description: "test MCP tool",
		daemonName:  "test_daemon",
	}
	Register(mcpTool)
	defer Unregister("mcp_custom_tool")

	level := GetProactivityLevel("mcp_custom_tool")
	if level != PLevelReversibleAction {
		t.Errorf("MCP tool default level: expected L3 (%d), got %d", PLevelReversibleAction, level)
	}
}

func TestProactivityLevel_ClientToolAdapterDefaultsToL1(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	clientTool := &ClientToolAdapter{
		NameVal:        "client_custom_tool",
		DescriptionVal: "test client tool",
		SchemaVal:      `{"type": "object"}`,
	}
	Register(clientTool)
	defer Unregister("client_custom_tool")

	level := GetProactivityLevel("client_custom_tool")
	if level != PLevelPrepare {
		t.Errorf("Client tool default level: expected L1 (%d), got %d", PLevelPrepare, level)
	}
}

func TestProactivityLevel_ConfigOverride(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	// web_search is L4 by default
	before := GetProactivityLevel("web_search")
	if before != PLevelExternalSideEffect {
		t.Fatalf("web_search default: expected L4, got %d", before)
	}

	// Override to L1
	SetProactivityLevelOverride("web_search", PLevelPrepare)
	after := GetProactivityLevel("web_search")
	if after != PLevelPrepare {
		t.Errorf("web_search after override: expected L1 (%d), got %d", PLevelPrepare, after)
	}

	// Clear override, should revert to default
	ClearProactivityLevelOverride("web_search")
	reverted := GetProactivityLevel("web_search")
	if reverted != PLevelExternalSideEffect {
		t.Errorf("web_search after clearing override: expected L4, got %d", reverted)
	}
}

func TestProactivityLevel_FunctionToolDefaultsToL1(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	fnTool := &FunctionTool{
		name:   "unknown_custom_fn",
		schema: `{"type": "object"}`,
		fn:     nil,
	}
	Register(fnTool)
	defer Unregister("unknown_custom_fn")

	level := GetProactivityLevel("unknown_custom_fn")
	if level != PLevelPrepare {
		t.Errorf("FunctionTool default level: expected L1 (%d), got %d", PLevelPrepare, level)
	}
}

func TestProactivityLevel_OverrideTakesPrecedenceOverBuiltIn(t *testing.T) {
	ClearAllProactivityLevelOverrides()
	defer ClearAllProactivityLevelOverrides()

	// read_file is L0 by default
	if GetProactivityLevel("read_file") != PLevelObserve {
		t.Fatal("read_file should default to L0")
	}

	// Override to L4
	SetProactivityLevelOverride("read_file", PLevelExternalSideEffect)
	if GetProactivityLevel("read_file") != PLevelExternalSideEffect {
		t.Error("Override should take precedence over built-in default")
	}
}
