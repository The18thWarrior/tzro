package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	tzroctx "tzro/pkg/context"
	"tzro/pkg/dlp"
	"tzro/pkg/executor"
	"tzro/pkg/store"
	"tzro/pkg/verification"
)

// MCPServer implements a JSON-RPC 2.0 stdio MCP server exposing context, impact, graph, and verification tools.
type MCPServer struct {
	engine              *executor.Engine
	storeDB             *store.Store
	workspace           string
	protocolVersion     string
	reqID               atomic.Int64
	verificationService *verification.Service
	activeMu            sync.Mutex
	activeCalls         map[string]context.CancelFunc
}

// NewMCPServer creates a new MCP server wrapping the executor engine and store.
func NewMCPServer(engine *executor.Engine, s *store.Store, workspace string) *MCPServer {
	fa, _ := verification.NewWorkspaceFileAccess(workspace)
	runner := verification.NewExactCommandRunner(workspace)
	storeAdapter := verification.NewProductionEvidenceStore(s, workspace)
	vService := verification.NewService(workspace, verification.Dependencies{
		FileAccess:    fa,
		CommandRunner: runner,
		EvidenceStore: storeAdapter,
	})

	return &MCPServer{
		engine:              engine,
		storeDB:             s,
		workspace:           workspace,
		protocolVersion:     "2024-11-05",
		verificationService: vService,
		activeCalls:         make(map[string]context.CancelFunc),
	}
}

// JSON-RPC 2.0 types
type jsonRPCRequest struct {
	JSONRPC string                 `json:"jsonrpc"`
	ID      interface{}            `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcError   `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCNotification struct {
	JSONRPC string      `json:"jsonrpc"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

// MCP protocol types
type mcpInitializeResult struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ServerInfo      map[string]string      `json:"serverInfo"`
}

type mcpTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	InputSchema  map[string]interface{} `json:"inputSchema"`
	OutputSchema map[string]interface{} `json:"outputSchema,omitempty"`
}

type mcpToolsListResult struct {
	Tools []mcpTool `json:"tools"`
}

type mcpToolCallResult struct {
	Content           []mcpContent           `json:"content"`
	StructuredContent map[string]interface{} `json:"structuredContent,omitempty"`
	IsError           bool                   `json:"isError,omitempty"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func reqIDKey(id interface{}) string {
	switch v := id.(type) {
	case string:
		return "s:" + v
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("n:%d", int64(v))
		}
		return fmt.Sprintf("f:%g", v)
	case float32:
		if v == float32(int64(v)) {
			return fmt.Sprintf("n:%d", int64(v))
		}
		return fmt.Sprintf("f:%g", v)
	case int:
		return fmt.Sprintf("n:%d", v)
	case int64:
		return fmt.Sprintf("n:%d", v)
	case nil:
		return "nil"
	default:
		return fmt.Sprintf("v:%v", v)
	}
}

func (s *MCPServer) registerActiveRequest(id interface{}, parent context.Context) (context.Context, context.CancelFunc) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeCalls == nil {
		s.activeCalls = make(map[string]context.CancelFunc)
	}
	ctx, cancel := context.WithCancel(parent)
	key := reqIDKey(id)
	s.activeCalls[key] = cancel
	return ctx, cancel
}

func (s *MCPServer) unregisterActiveRequest(id interface{}) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeCalls != nil {
		delete(s.activeCalls, reqIDKey(id))
	}
}

func (s *MCPServer) cancelActiveRequest(id interface{}) {
	s.activeMu.Lock()
	defer s.activeMu.Unlock()
	if s.activeCalls != nil {
		if cancel, ok := s.activeCalls[reqIDKey(id)]; ok {
			cancel()
		}
	}
}

// Serve runs the MCP server over an input reader and output writer.
func (s *MCPServer) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	reader := bufio.NewReader(in)
	var writerMu sync.Mutex

	writeResponse := func(resp interface{}) error {
		writerMu.Lock()
		defer writerMu.Unlock()
		data, err := json.Marshal(resp)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(out, "%s\n", data)
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading input: %w", err)
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = writeResponse(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      nil,
				Error:   &rpcError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		// Handle cancellation notification
		if req.Method == "notifications/cancelled" {
			if req.Params != nil {
				if targetID, exists := req.Params["requestId"]; exists {
					s.cancelActiveRequest(targetID)
				}
			}
			continue
		}

		// Handle initialize and discovery synchronously
		if req.Method == "initialize" || req.Method == "notifications/initialized" || req.Method == "tools/list" {
			resp := s.handleRequest(ctx, &req, writeResponse)
			if resp != nil {
				_ = writeResponse(resp)
			}
			continue
		}

		// Execute tools/call asynchronously so the reader remains available for cancellation
		reqCtx, cancel := s.registerActiveRequest(req.ID, ctx)
		go func(r jsonRPCRequest, cCtx context.Context, cFunc context.CancelFunc) {
			defer func() {
				cFunc()
				s.unregisterActiveRequest(r.ID)
			}()

			resp := s.handleRequest(cCtx, &r, writeResponse)
			if resp != nil {
				_ = writeResponse(resp)
			}
		}(req, reqCtx, cancel)
	}
}

// ServeStdio runs the MCP server over stdin/stdout.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	return s.Serve(ctx, os.Stdin, os.Stdout)
}

func (s *MCPServer) handleRequest(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	switch req.Method {
	case "initialize":
		clientVer, _ := req.Params["protocolVersion"].(string)
		negotiated := "2024-11-05"
		if clientVer == "2025-06-18" {
			negotiated = "2025-06-18"
		}
		s.protocolVersion = negotiated

		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: mcpInitializeResult{
				ProtocolVersion: negotiated,
				Capabilities: map[string]interface{}{
					"tools": map[string]interface{}{},
				},
				ServerInfo: map[string]string{
					"name":    "tzro",
					"version": "3.0.0",
				},
			},
		}

	case "notifications/initialized":
		return nil

	case "tools/list":
		tools := []mcpTool{
			{
				Name:        "tzro_execute_graph",
				Description: executor.GraphToolDescription,
				InputSchema: executor.GraphToolParameters(),
			},
			{
				Name:        "tzro_get_context_pack",
				Description: "Assemble a ranked, token-budgeted context pack with AST declaration spans and call graphs.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"query":     map[string]interface{}{"type": "string", "description": "Freeform task description or keywords"},
						"symbol":    map[string]interface{}{"type": "string", "description": "Exact symbol name to anchor context around"},
						"file":      map[string]interface{}{"type": "string", "description": "File path to disambiguate symbol anchor (requires symbol)"},
						"budget":    map[string]interface{}{"type": "integer", "description": "Maximum token budget for context pack (default 4000)"},
						"tokenizer": map[string]interface{}{"type": "string", "description": "Tokenizer encoding: cl100k_base | o200k_base"},
						"format":    map[string]interface{}{"type": "string", "description": "Output format: markdown | json (default markdown)", "enum": []string{"markdown", "json"}},
					},
				},
			},
			{
				Name:        "tzro_get_impact_report",
				Description: "Analyze structural blast radius, dependent modules, callers, and test suites.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"symbol":    map[string]interface{}{"type": "string", "description": "Exact symbol name to analyze impact for"},
						"file":      map[string]interface{}{"type": "string", "description": "File path to disambiguate symbol"},
						"scope":     map[string]interface{}{"type": "string", "description": "Diff scope: staged | unstaged | all (default all)", "enum": []string{"staged", "unstaged", "all"}},
						"budget":    map[string]interface{}{"type": "integer", "description": "Maximum token budget (default 4000)"},
						"tokenizer": map[string]interface{}{"type": "string", "description": "Tokenizer encoding: cl100k_base | o200k_base"},
						"format":    map[string]interface{}{"type": "string", "description": "Output format: markdown | json | tree (default markdown)", "enum": []string{"markdown", "json", "tree"}},
					},
				},
			},
			{
				Name:        "tzro_edit_and_verify",
				Description: "Apply a batch of literal text edits and execute repository verification preset.",
				InputSchema: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"edits": map[string]interface{}{
							"type":        "array",
							"description": "List of literal replacements or creations to preflight and apply",
							"items": map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"kind":             map[string]interface{}{"type": "string", "enum": []string{"replace", "create"}},
									"path":             map[string]interface{}{"type": "string"},
									"old_text":         map[string]interface{}{"type": "string"},
									"new_text":         map[string]interface{}{"type": "string"},
									"expected_matches": map[string]interface{}{"type": "integer"},
									"content":          map[string]interface{}{"type": "string"},
								},
								"required": []string{"kind", "path"},
							},
						},
					},
					"required": []string{"edits"},
				},
			},
		}

		if s.protocolVersion == "2025-06-18" {
			tools[1].OutputSchema = map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query":       map[string]interface{}{"type": "string"},
					"budget":      map[string]interface{}{"type": "integer"},
					"used_tokens": map[string]interface{}{"type": "integer"},
					"items":       map[string]interface{}{"type": "array"},
					"coverage":    map[string]interface{}{"type": "object"},
					"tokenizer":   map[string]interface{}{"type": "object"},
				},
			}
			tools[2].OutputSchema = map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"workspace_root":       map[string]interface{}{"type": "string"},
					"changed_symbols":      map[string]interface{}{"type": "array"},
					"reference_edges":      map[string]interface{}{"type": "array"},
					"candidate_test_files": map[string]interface{}{"type": "array"},
					"coverage":             map[string]interface{}{"type": "object"},
				},
			}
			tools[3].OutputSchema = map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"schema":          map[string]interface{}{"type": "string"},
					"application":     map[string]interface{}{"type": "string"},
					"changed_files":   map[string]interface{}{"type": "array"},
					"uncertain_files": map[string]interface{}{"type": "array"},
					"verification":    map[string]interface{}{"type": "string"},
					"checks":          map[string]interface{}{"type": "array"},
					"termination":     map[string]interface{}{"type": "string"},
					"retention":       map[string]interface{}{"type": "string"},
				},
			}
		}

		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  mcpToolsListResult{Tools: tools},
		}

	case "tools/call":
		return s.handleToolCall(ctx, req, notify)

	default:
		return &jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("Method not found: %s", req.Method)},
		}
	}
}

func (s *MCPServer) handleToolCall(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	params := req.Params
	toolName, _ := params["name"].(string)

	switch toolName {
	case "tzro_execute_graph":
		return s.handleExecuteGraph(ctx, req, notify)
	case "tzro_get_context_pack":
		return s.handleGetContextPack(ctx, req, notify)
	case "tzro_get_impact_report":
		return s.handleGetImpactReport(ctx, req, notify)
	case "tzro_edit_and_verify":
		return s.handleEditAndVerify(ctx, req, notify)
	default:
		return errorResult(req.ID, fmt.Sprintf("Unknown tool: %s", toolName))
	}
}

func (s *MCPServer) handleExecuteGraph(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	arguments, _ := req.Params["arguments"].(map[string]interface{})
	graphData, _ := arguments["graph"]

	graphJSON, err := json.Marshal(graphData)
	if err != nil {
		return errorResult(req.ID, fmt.Sprintf("Invalid graph: %v", err))
	}

	var g executor.Graph
	if err := json.Unmarshal(graphJSON, &g); err != nil {
		return errorResult(req.ID, fmt.Sprintf("Graph parse error: %v", err))
	}

	var progressToken interface{}
	if meta, ok := arguments["_meta"].(map[string]interface{}); ok {
		progressToken = meta["progressToken"]
	}
	if progressToken == nil {
		progressToken = req.ID
	}

	// Emit progress notification: starting
	_ = notify(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params: map[string]interface{}{
			"progressToken": progressToken,
			"progress":      0,
			"total":         len(g.Nodes),
			"message":       fmt.Sprintf("Starting graph execution: %s (%d nodes)", g.TaskID, len(g.Nodes)),
		},
	})

	// Execute the graph
	result, err := s.engine.Execute(ctx, &g)
	if result != nil {
		recordActivity("graph", result.Status, 0)
	}
	if err != nil {
		return errorResult(req.ID, fmt.Sprintf("Execution error: %v", err))
	}

	// Emit progress notification: completed
	_ = notify(jsonRPCNotification{
		JSONRPC: "2.0",
		Method:  "notifications/progress",
		Params: map[string]interface{}{
			"progressToken": progressToken,
			"progress":      len(g.Nodes),
			"total":         len(g.Nodes),
			"message":       fmt.Sprintf("Completed graph execution: %s", g.TaskID),
		},
	})

	resultBytes, _ := json.Marshal(selectedGraphResult(&g, result, s.storeDB, s.workspace))
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpToolCallResult{
			Content: []mcpContent{{Type: "text", Text: string(resultBytes)}},
			IsError: result.Status == "failed",
		},
	}
}

func (s *MCPServer) handleGetContextPack(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	arguments, _ := req.Params["arguments"].(map[string]interface{})
	if arguments == nil {
		arguments = make(map[string]interface{})
	}

	query, _ := arguments["query"].(string)
	symbol, _ := arguments["symbol"].(string)
	file, _ := arguments["file"].(string)
	format, _ := arguments["format"].(string)

	// Validate budget
	budget := 0
	if bVal, exists := arguments["budget"]; exists {
		fVal, ok := bVal.(float64)
		if !ok || fVal != float64(int(fVal)) {
			return errorResult(req.ID, "fractional budget not allowed")
		}
		if int(fVal) <= 0 {
			return errorResult(req.ID, "budget must be greater than zero")
		}
		budget = int(fVal)
	}

	// Validate file path
	if file != "" {
		if symbol == "" {
			return errorResult(req.ID, "--file requires --symbol")
		}
		cleanFile := filepath.Clean(file)
		if filepath.IsAbs(cleanFile) {
			rel, err := filepath.Rel(s.workspace, cleanFile)
			if err != nil || strings.HasPrefix(rel, "..") {
				return errorResult(req.ID, fmt.Sprintf("file %q is outside bound workspace", file))
			}
			file = rel
		} else if strings.HasPrefix(cleanFile, "..") {
			return errorResult(req.ID, fmt.Sprintf("file %q is outside bound workspace", file))
		}
	}

	// Progress notification
	var progressToken interface{}
	if meta, ok := arguments["_meta"].(map[string]interface{}); ok {
		progressToken = meta["progressToken"]
	}
	if progressToken != nil {
		_ = notify(jsonRPCNotification{
			JSONRPC: "2.0",
			Method:  "notifications/progress",
			Params: map[string]interface{}{
				"progressToken": progressToken,
				"progress":      1,
				"total":         2,
				"message":       "Assembling context pack",
			},
		})
	}

	wp, _ := dlp.LoadWorkspacePolicy(s.workspace)
	var policyEngine *dlp.PolicyEngine
	if wp != nil {
		policyEngine = dlp.NewPolicyEngine(wp)
	}

	cReq := tzroctx.ContextRequest{
		WorkspaceRoot: s.workspace,
		Query:         query,
		Symbol:        symbol,
		File:          file,
		Budget:        budget,
		Format:        format,
	}

	service := tzroctx.NewContextService(s.storeDB, policyEngine)
	res, err := service.Execute(ctx, cReq)
	if err != nil {
		return errorResult(req.ID, err.Error())
	}

	var structured map[string]interface{}
	if s.protocolVersion == "2025-06-18" && res.Pack != nil {
		data, _ := json.Marshal(res.Pack)
		_ = json.Unmarshal(data, &structured)
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpToolCallResult{
			Content:           []mcpContent{{Type: "text", Text: res.Formatted}},
			StructuredContent: structured,
		},
	}
}

func (s *MCPServer) handleGetImpactReport(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	arguments, _ := req.Params["arguments"].(map[string]interface{})
	if arguments == nil {
		arguments = make(map[string]interface{})
	}

	symbol, _ := arguments["symbol"].(string)
	file, _ := arguments["file"].(string)
	scope, _ := arguments["scope"].(string)
	format, _ := arguments["format"].(string)

	if format == "" {
		format = "markdown"
	}
	if format != "markdown" && format != "json" && format != "tree" {
		return errorResult(req.ID, fmt.Sprintf("invalid format %q: must be markdown, json, or tree", format))
	}

	// Validate conflicting anchors
	if symbol != "" && scope != "" && scope != "all" {
		return errorResult(req.ID, "cannot specify both symbol and scope")
	}

	if scope == "" {
		scope = "all"
	} else if scope != "staged" && scope != "unstaged" && scope != "all" {
		return errorResult(req.ID, fmt.Sprintf("invalid scope %q: must be staged, unstaged, or all", scope))
	}

	// Validate budget
	budget := 0
	if bVal, exists := arguments["budget"]; exists {
		fVal, ok := bVal.(float64)
		if !ok || fVal != float64(int(fVal)) {
			return errorResult(req.ID, "fractional budget not allowed")
		}
		if int(fVal) <= 0 {
			return errorResult(req.ID, "budget must be greater than zero")
		}
		budget = int(fVal)
	}
	if budget == 0 {
		cfg, err := tzroctx.LoadConfig(s.workspace)
		if err == nil && cfg != nil {
			budget = cfg.DefaultBudget
		} else {
			budget = 4000
		}
	}

	// Validate file path
	if file != "" {
		cleanFile := filepath.Clean(file)
		if filepath.IsAbs(cleanFile) {
			rel, err := filepath.Rel(s.workspace, cleanFile)
			if err != nil || strings.HasPrefix(rel, "..") {
				return errorResult(req.ID, fmt.Sprintf("file %q is outside bound workspace", file))
			}
			file = rel
		} else if strings.HasPrefix(cleanFile, "..") {
			return errorResult(req.ID, fmt.Sprintf("file %q is outside bound workspace", file))
		}
	}

	// Progress notification
	var progressToken interface{}
	if meta, ok := arguments["_meta"].(map[string]interface{}); ok {
		progressToken = meta["progressToken"]
	}
	if progressToken != nil {
		_ = notify(jsonRPCNotification{
			JSONRPC: "2.0",
			Method:  "notifications/progress",
			Params: map[string]interface{}{
				"progressToken": progressToken,
				"progress":      1,
				"total":         2,
				"message":       "Analyzing impact report",
			},
		})
	}

	wp, _ := dlp.LoadWorkspacePolicy(s.workspace)
	var policyEngine *dlp.PolicyEngine
	if wp != nil {
		policyEngine = dlp.NewPolicyEngine(wp)
	}

	analyzer := tzroctx.NewImpactAnalyzer(s.storeDB, policyEngine)
	var pack *tzroctx.ContextPack
	var err error

	if symbol != "" {
		pack, err = analyzer.AnalyzeSymbol(s.workspace, symbol, budget, false)
	} else {
		pack, err = analyzer.AnalyzeDiff(s.workspace, scope, budget, false)
	}

	if err != nil {
		return errorResult(req.ID, err.Error())
	}

	// Format text output
	var textOutput string
	switch format {
	case "tree":
		if pack.Impact != nil {
			textOutput = tzroctx.RenderImpactTree(pack.Impact, 80, true)
		} else {
			textOutput = pack.FormatMarkdown()
		}
	case "json":
		data, _ := json.MarshalIndent(pack.Impact, "", "  ")
		textOutput = string(data) + "\n"
	default:
		textOutput = pack.FormatMarkdown()
	}

	var structured map[string]interface{}
	if s.protocolVersion == "2025-06-18" && pack.Impact != nil {
		data, _ := json.Marshal(pack.Impact)
		_ = json.Unmarshal(data, &structured)
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpToolCallResult{
			Content:           []mcpContent{{Type: "text", Text: textOutput}},
			StructuredContent: structured,
		},
	}
}

func errorResult(id interface{}, msg string) *jsonRPCResponse {
	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: mcpToolCallResult{
			Content: []mcpContent{{Type: "text", Text: msg}},
			IsError: true,
		},
	}
}

func (s *MCPServer) handleEditAndVerify(ctx context.Context, req *jsonRPCRequest, notify func(interface{}) error) *jsonRPCResponse {
	arguments, _ := req.Params["arguments"].(map[string]interface{})
	data, err := json.Marshal(arguments)
	if err != nil {
		return errorResult(req.ID, fmt.Sprintf("invalid arguments: %v", err))
	}

	var vReq verification.Request
	if err := json.Unmarshal(data, &vReq); err != nil {
		return errorResult(req.ID, fmt.Sprintf("failed to parse verification request: %v", err))
	}

	if s.verificationService == nil {
		fa, _ := verification.NewWorkspaceFileAccess(s.workspace)
		runner := verification.NewExactCommandRunner(s.workspace)
		storeAdapter := verification.NewProductionEvidenceStore(s.storeDB, s.workspace)
		s.verificationService = verification.NewService(s.workspace, verification.Dependencies{
			FileAccess:    fa,
			CommandRunner: runner,
			EvidenceStore: storeAdapter,
		})
	}

	summary := s.verificationService.ApplyAndVerify(ctx, vReq, verification.Options{})

	var structured map[string]interface{}
	if s.protocolVersion == "2025-06-18" {
		structured = summary.ToMap()
	}

	return &jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: mcpToolCallResult{
			Content:           []mcpContent{{Type: "text", Text: summary.FormatText()}},
			StructuredContent: structured,
		},
	}
}

// 22. MCP COMMAND
func newMCPCmd() *cobra.Command {
	var mcpWorkspace string
	var mcpDeadline string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run the tzro Model Context Protocol (MCP) server over stdio",
		RunE: func(cmd *cobra.Command, args []string) error {
			if mcpWorkspace == "" {
				mcpWorkspace, _ = os.Getwd()
			}

			s, _ := store.OpenStore(getDBPath())
			if s != nil {
				defer s.Close()
			}

			engine, closeWorkers, err := configuredEngine(mcpWorkspace, s, 4)
			if err != nil {
				return err
			}
			defer closeWorkers()
			server := NewMCPServer(engine, s, mcpWorkspace)

			runCtx := cmd.Context()
			if mcpDeadline != "" {
				if dTime, err := time.Parse(time.RFC3339Nano, mcpDeadline); err == nil {
					var cancel context.CancelFunc
					runCtx, cancel = context.WithDeadline(runCtx, dTime)
					defer cancel()
				} else if dTime, err := time.Parse(time.RFC3339, mcpDeadline); err == nil {
					var cancel context.CancelFunc
					runCtx, cancel = context.WithDeadline(runCtx, dTime)
					defer cancel()
				}
			}

			return server.ServeStdio(runCtx)
		},
	}

	cmd.Flags().StringVar(&mcpWorkspace, "workspace", "", "Bound workspace directory (defaults to current working directory)")
	cmd.Flags().StringVar(&mcpDeadline, "deadline", "", "RFC3339Nano external session deadline")
	return cmd
}
