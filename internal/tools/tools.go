package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"tzro/internal/cache"
	"tzro/internal/config"
	"tzro/internal/mcp"
	"tzro/internal/wasm"
)

// Tool represents a single tool definition that can be dynamically resolved and executed.
type Tool interface {
	Name() string
	GetSchema() (string, error)
	Call(ctx context.Context, args map[string]interface{}) (string, error)
}

var (
	registry = make(map[string]Tool)
	mutex    sync.RWMutex
)

// Register registers a tool definition to the registry.
func Register(t Tool) {
	mutex.Lock()
	defer mutex.Unlock()
	registry[t.Name()] = t
}

// Unregister removes a registered tool from the registry.
func Unregister(name string) {
	mutex.Lock()
	defer mutex.Unlock()
	delete(registry, name)
}

// GetList returns a copy of all currently registered tools.
func GetList() []Tool {
	mutex.RLock()
	defer mutex.RUnlock()
	var list []Tool
	for _, t := range registry {
		list = append(list, t)
	}
	return list
}

// GetTool retrieves a registered tool from the registry.
func GetTool(name string) Tool {
	mutex.RLock()
	defer mutex.RUnlock()
	return registry[name]
}

// ClientToolAdapter represents a tool whose execution is delegated to the client.
type ClientToolAdapter struct {
	NameVal        string
	DescriptionVal string
	SchemaVal      string
}

func (c *ClientToolAdapter) Name() string               { return c.NameVal }
func (c *ClientToolAdapter) GetSchema() (string, error) { return c.SchemaVal, nil }
func (c *ClientToolAdapter) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	return "", fmt.Errorf("client-side tool '%s' must be executed by client", c.NameVal)
}

// GetSchema returns the GBNF-wrapped JSON schema for a tool by name.
func GetSchema(name string) (string, error) {
	mutex.RLock()
	t, exists := registry[name]
	mutex.RUnlock()

	if exists {
		return t.GetSchema()
	}

	// Dynamic fallback: Check MCP Registry daemons
	ctx := context.Background()
	daemon, found := mcp.GlobalRegistry.FindDaemonForTool(ctx, name)
	if found {
		// Discover tools from this daemon and register them
		toolsList, err := daemon.ListTools(ctx)
		if err == nil {
			for _, item := range toolsList {
				if item.Name == name {
					mcpTool := &MCPToolAdapter{
						name:        item.Name,
						description: item.Description,
						inputSchema: item.InputSchema,
						daemonName:  daemon.Name,
					}
					Register(mcpTool)
					return mcpTool.GetSchema()
				}
			}
		}
	}

	return "", fmt.Errorf("tool %q not found in registry or MCP daemons", name)
}

// Call executes a tool by name with the given arguments.
func Call(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	mutex.RLock()
	t, exists := registry[name]
	mutex.RUnlock()

	if exists {
		return t.Call(ctx, args)
	}

	// Dynamic lookup if not registered in tools.Init
	daemon, found := mcp.GlobalRegistry.FindDaemonForTool(ctx, name)
	if found {
		toolsList, err := daemon.ListTools(ctx)
		if err == nil {
			for _, item := range toolsList {
				if item.Name == name {
					mcpTool := &MCPToolAdapter{
						name:        item.Name,
						description: item.Description,
						inputSchema: item.InputSchema,
						daemonName:  daemon.Name,
					}
					Register(mcpTool)
					return mcpTool.Call(ctx, args)
				}
			}
		}
	}

	if ctx.Value("is_benchmark") != nil {
		return fmt.Sprintf(`{"error": "tool '%s' is not registered or discovered in the dynamic Tool Registry"}`, name), nil
	}

	return "", fmt.Errorf("tool '%s' is not registered or discovered in the dynamic Tool Registry", name)
}

// MCPToolAdapter wraps an MCP daemon tool call
type MCPToolAdapter struct {
	name        string
	description string
	inputSchema map[string]interface{}
	daemonName  string
}

func (m *MCPToolAdapter) Name() string {
	return m.name
}

func (m *MCPToolAdapter) GetSchema() (string, error) {
	return mcp.GetGBNFSchema(m.inputSchema)
}

func (m *MCPToolAdapter) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	daemon, exists := mcp.GlobalRegistry.GetDaemon(m.daemonName)
	if !exists {
		return "", fmt.Errorf("MCP daemon '%s' for tool '%s' not found", m.daemonName, m.name)
	}

	if !daemon.IsActive() {
		if err := daemon.Start(ctx); err != nil {
			return "", fmt.Errorf("failed to start MCP daemon '%s': %w", m.daemonName, err)
		}
	}

	params := map[string]interface{}{
		"name":      m.name,
		"arguments": args,
	}

	response, err := daemon.Call("tools/call", params)
	if err != nil {
		return "", fmt.Errorf("MCP tool call failed for daemon '%s': %w", m.daemonName, err)
	}

	var result map[string]interface{}
	var output string
	if json.Unmarshal([]byte(response), &result) == nil {
		if resVal, ok := result["result"].(map[string]interface{}); ok {
			if contentList, ok := resVal["content"].([]interface{}); ok && len(contentList) > 0 {
				if firstContent, ok := contentList[0].(map[string]interface{}); ok {
					output = fmt.Sprintf("%v", firstContent["text"])
				}
			}
		}
	}
	if output == "" {
		output = response
	}

	return output, nil
}

// FunctionTool allows registering static Go functions directly as tools
type FunctionTool struct {
	NameVal   string
	SchemaVal string
	Fn        func(ctx context.Context, args map[string]interface{}) (string, error)
}

func (f *FunctionTool) Name() string {
	return f.NameVal
}

func (f *FunctionTool) GetSchema() (string, error) {
	return f.SchemaVal, nil
}

func (f *FunctionTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	if f.Fn == nil {
		return "", fmt.Errorf("tool '%s' has no execution function", f.NameVal)
	}
	return f.Fn(ctx, args)
}

type StaticToolConfig struct {
	Schema map[string]interface{} `json:"schema"`
	Daemon string                 `json:"daemon,omitempty"`
}

// Init initializes the Tool Registry, loading static schemas and registering internal tools.
func Init(configPath string) error {
	mutex.Lock()
	registry = make(map[string]Tool)
	mutex.Unlock()

	Register(&ListToolsTool{})
	Register(NewWebSearchTool())
	Register(NewSearchKBTool())
	Register(NewQueryKGTool())
	Register(NewIngestKGTool())
	Register(NewExploreEntityTool())
	Register(NewSaveMemoryTool())
	Register(NewRecallMemoryTool())
	Register(NewForgetMemoryTool())
	Register(NewCreateTaskTool())
	Register(NewCreateDatabaseTool())
	Register(NewCreateTableTool())
	Register(NewInsertTool())
	Register(NewUpdateTool())
	Register(NewDeleteTool())
	Register(NewQueryTool())
	Register(&GatherMetricsTool{})
	Register(&GatherTasksTool{})
	Register(&GatherConfigTool{})
	Register(&GatherWorkflowsTool{})
	Register(&ComposeLayoutTool{})
	Register(&TerminalSynthesisTool{})

	// Register filesystem tools with path validation security boundary
	fsValidator := NewPathValidator(GetAllowedPaths())
	Register(NewReadFileTool(fsValidator))
	Register(NewListDirTool(fsValidator))
	Register(NewSearchFilesTool(fsValidator))
	Register(NewPeekFileTool(fsValidator))
	Register(NewWriteFileTool(fsValidator))

	// 1. Register cache tools statically
	Register(&FunctionTool{
		NameVal: "introspect_cache",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string" }
					},
					"required": ["cacheId"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			return cache.DefaultStore.Introspect(ctx, cacheID), nil
		},
	})

	Register(&FunctionTool{
		NameVal: "read_cached_data",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string" },
						"limit": { "type": "integer" },
						"offset": { "type": "integer" }
					},
					"required": ["cacheId"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			var limit, offset int
			if l, ok := args["limit"].(float64); ok {
				limit = int(l)
			} else if l, ok := args["limit"].(int); ok {
				limit = l
			} else {
				limit = 10
			}
			if o, ok := args["offset"].(float64); ok {
				offset = int(o)
			} else if o, ok := args["offset"].(int); ok {
				offset = o
			} else {
				offset = 0
			}
			return cache.DefaultStore.Read(ctx, cacheID, limit, offset), nil
		},
	})

	Register(&FunctionTool{
		NameVal: "jq_cached_data",
		SchemaVal: `{
			"type": "object",
			"properties": {
				"tool_arguments": {
					"type": "object",
					"properties": {
						"cacheId": { "type": "string" },
						"filter": { "type": "string" }
					},
					"required": ["cacheId", "filter"]
				}
			},
			"required": ["tool_arguments"]
		}`,
		Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
			cacheID, _ := args["cacheId"].(string)
			filter, _ := args["filter"].(string)
			return cache.DefaultStore.Query(ctx, cacheID, filter), nil
		},
	})

	// 2. Load static schemas from config file (fallback or preloaded tools)
	if configPath != "" {
		file, err := os.Open(configPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return err
			}
		} else {
			defer file.Close()
			var cfg map[string]StaticToolConfig
			if err := json.NewDecoder(file).Decode(&cfg); err != nil {
				return fmt.Errorf("failed to decode static tool schemas: %w", err)
			}

			for name, item := range cfg {
				if item.Daemon != "" {
					Register(&MCPToolAdapter{
						name:        name,
						description: "Statically configured MCP tool",
						inputSchema: item.Schema,
						daemonName:  item.Daemon,
					})
				} else {
					gbnfStr, _ := mcp.GetGBNFSchema(item.Schema)
					Register(&FunctionTool{
						NameVal:   name,
						SchemaVal: gbnfStr,
						Fn: func(ctx context.Context, args map[string]interface{}) (string, error) {
							return "", fmt.Errorf("static tool '%s' is registered as a schema placeholder and has no execution function", name)
						},
					})
				}
			}
		}
	}

	// 3. Load dynamic OpenAPI-based integrations from SQLite database
	if err := LoadOpenAPITools(); err != nil {
		fmt.Printf("[Tools Init Warning] Failed to load dynamic OpenAPI tools: %v\n", err)
	}

	// 4. Load dynamic Sandboxed Micro-Skills (WASM-embedded tools) from .tzro/wasm/
	if err := LoadWasmTools(); err != nil {
		fmt.Printf("[Tools Init Warning] Failed to load dynamic WASM tools: %v\n", err)
	}

	// 5. Load Agent App tools from .tzro/apps/ (installed via Package Manager)
	if err := LoadAppTools(); err != nil {
		fmt.Printf("[Tools Init Warning] Failed to load Agent App tools: %v\n", err)
	}

	return nil
}

// LoadWasmTools scans .tzro/wasm/ for compiled .wasm binaries alongside their json schemas,
// registering each pair dynamically with the global registry.
func LoadWasmTools() error {
	wasmDir := config.ResolvePath("wasm")

	// If wasm directory does not exist, create it or return nil (skip gracefully)
	if _, err := os.Stat(wasmDir); os.IsNotExist(err) {
		_ = os.MkdirAll(wasmDir, 0755)
		return nil
	}

	files, err := os.ReadDir(wasmDir)
	if err != nil {
		return fmt.Errorf("failed to read WASM directory: %w", err)
	}

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		name := file.Name()
		if !strings.HasSuffix(name, ".wasm") {
			continue
		}

		skillID := strings.TrimSuffix(name, ".wasm")
		wasmPath := filepath.Join(wasmDir, name)
		schemaPath := filepath.Join(wasmDir, skillID+".json")

		// Check if schema file exists
		if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
			fmt.Printf("[WASM Loader Warning] Skipping micro-skill '%s': missing schema file '%s'\n", skillID, schemaPath)
			continue
		}

		// Instantiate and register
		wasmTool := wasm.NewWasmToolAdapter(skillID, wasmPath, schemaPath)
		Register(wasmTool)
		fmt.Printf("[WASM Loader] Dynamically registered Sandboxed Micro-Skill: '%s' -> %s\n", skillID, wasmPath)
	}

	return nil
}

// LoadAppTools scans .tzro/apps/ for installed Agent App manifests and registers
// their WASM tools with app-scoped namespacing ({appId}_{toolName}).
// This is called on daemon startup to restore tools from previously installed apps.
func LoadAppTools() error {
	appsDir := config.ResolvePath("apps")

	if _, err := os.Stat(appsDir); os.IsNotExist(err) {
		return nil // No apps installed
	}

	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return fmt.Errorf("failed to read apps directory: %w", err)
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		appID := entry.Name()
		manifestPath := filepath.Join(appsDir, appID, "tzro.manifest.json")

		manifestFile, err := os.Open(manifestPath)
		if err != nil {
			continue // App directory without manifest, skip
		}

		var manifest struct {
			ID    string `json:"id"`
			Tools []struct {
				Name string `json:"name"`
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"tools"`
		}

		if err := json.NewDecoder(manifestFile).Decode(&manifest); err != nil {
			manifestFile.Close()
			continue
		}
		manifestFile.Close()

		for _, td := range manifest.Tools {
			if td.Type != "wasm" {
				continue
			}

			namespacedName := appID + "_" + td.Name
			wasmPath := filepath.Join(appsDir, appID, td.Path)
			schemaPath := strings.TrimSuffix(wasmPath, filepath.Ext(wasmPath)) + ".json"

			if _, err := os.Stat(schemaPath); os.IsNotExist(err) {
				continue
			}

			wasmTool := wasm.NewWasmToolAdapter(namespacedName, wasmPath, schemaPath)
			Register(wasmTool)
			fmt.Printf("[App Loader] Registered Agent App tool: '%s' -> %s\n", namespacedName, wasmPath)
		}
	}

	return nil
}
