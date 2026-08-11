package tools

import (
	"testing"
)

// TestToolDescription_BaseAgentTool verifies that BaseAgentTool exposes
// the description field via the Description() method.
func TestToolDescription_BaseAgentTool(t *testing.T) {
	tool := &BaseAgentTool{
		name:        "test_tool",
		description: "A test tool for verifying descriptions.",
		schema:      "{}",
	}

	if tool.Description() != "A test tool for verifying descriptions." {
		t.Errorf("expected exact description match, got %q", tool.Description())
	}
}

// TestToolDescription_FunctionTool verifies that FunctionTool returns an
// empty description (it has no description field by design).
func TestToolDescription_FunctionTool(t *testing.T) {
	tool := &FunctionTool{
		NameVal:   "func_tool",
		SchemaVal: "{}",
	}

	// FunctionTool doesn't carry a description — should return ""
	if tool.Description() != "" {
		t.Errorf("expected empty description for FunctionTool, got %q", tool.Description())
	}
}

// TestToolDescription_ClientToolAdapter verifies that ClientToolAdapter
// exposes its DescriptionVal via Description().
func TestToolDescription_ClientToolAdapter(t *testing.T) {
	tool := &ClientToolAdapter{
		NameVal:        "client_tool",
		DescriptionVal: "Delegated to the client.",
		SchemaVal:      "{}",
	}

	if tool.Description() != "Delegated to the client." {
		t.Errorf("expected description match, got %q", tool.Description())
	}
}

// TestToolDescription_MCPToolAdapter verifies that MCPToolAdapter
// exposes its description via Description().
func TestToolDescription_MCPToolAdapter(t *testing.T) {
	tool := &MCPToolAdapter{
		name:        "mcp_tool",
		description: "MCP-provided tool.",
	}

	if tool.Description() != "MCP-provided tool." {
		t.Errorf("expected description match, got %q", tool.Description())
	}
}

// TestToolDescription_ListToolsTool verifies ListToolsTool has a non-empty description.
func TestToolDescription_ListToolsTool(t *testing.T) {
	tool := &ListToolsTool{}
	if tool.Description() == "" {
		t.Error("expected non-empty description for ListToolsTool")
	}
}

// TestToolDescription_RegistryLookup verifies that tools retrieved from the
// registry via GetTool expose descriptions correctly through the Tool interface.
func TestToolDescription_RegistryLookup(t *testing.T) {
	// Register a tool with a known description
	mutex.Lock()
	oldRegistry := registry
	registry = make(map[string]Tool)
	mutex.Unlock()
	defer func() {
		mutex.Lock()
		registry = oldRegistry
		mutex.Unlock()
	}()

	Register(&BaseAgentTool{
		name:        "web_search",
		description: "Search the web for information using multiple search engines.",
		schema:      "{}",
	})

	tool := GetTool("web_search")
	if tool == nil {
		t.Fatal("expected web_search to be registered")
	}

	desc := tool.Description()
	if desc == "" {
		t.Error("web_search should have a non-empty description via the Tool interface")
	}
	if desc != "Search the web for information using multiple search engines." {
		t.Errorf("unexpected description: %q", desc)
	}
}

// TestToolDescription_KeyToolsHaveDescriptions verifies that the constructors
// for key probe tools produce non-empty descriptions.
func TestToolDescription_KeyToolsHaveDescriptions(t *testing.T) {
	tools := []struct {
		name string
		tool Tool
	}{
		{"web_search", NewWebSearchTool()},
		{"web_browse", NewWebBrowseTool()},
		{"save_memory", NewSaveMemoryTool()},
		{"recall_memory", NewRecallMemoryTool()},
		{"search_kb", NewSearchKBTool()},
		{"create_task", NewCreateTaskTool()},
	}

	for _, tt := range tools {
		t.Run(tt.name, func(t *testing.T) {
			if tt.tool.Description() == "" {
				t.Errorf("tool %q has empty Description(), expected non-empty", tt.name)
			}
		})
	}
}
