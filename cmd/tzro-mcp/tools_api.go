package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"tzro/internal/content"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/sentinel"
	"tzro/internal/tools"
)

// TzroApiArgs defines the inputs for the generic tzro_api tool.
type TzroApiArgs struct {
	// The API endpoint to call. Can be a daemon HTTP path (e.g. "/api/memories")
	// or a named function: completion, classification, compact, web_search,
	// sentinel_wake, sentinel_alerts, activity_report, rag_context,
	// observer_events, observer_memories, memory_query, memory_ingest,
	// kg_neighborhood, kg_add_entity, skills_list, skills_get, skills_relevant,
	// skills_add, configure_tools, schedule, apps_list, apps_install,
	// apps_uninstall, dashboard_regenerate, dashboard_spec.
	Endpoint string `json:"endpoint" jsonschema:"required,API endpoint path or named function to call"`

	// HTTP method for daemon endpoints. Ignored for named functions.
	Method string `json:"method,omitempty" jsonschema:"HTTP method: GET or POST. Default GET. Ignored for named functions."`

	// Parameters — passed as function arguments for named functions, or as
	// query params (GET) / JSON body (POST) for daemon HTTP endpoints.
	Params map[string]interface{} `json:"params,omitempty" jsonschema:"Request parameters"`
}

func handleTzroApi(ctx context.Context, req *mcp.CallToolRequest, args TzroApiArgs) (*mcp.CallToolResult, any, error) {
	endpoint := strings.TrimSpace(args.Endpoint)
	if endpoint == "" {
		return errResult(`{"error": "endpoint cannot be empty"}`)
	}

	// Named function dispatch — inline Go calls
	if !strings.HasPrefix(endpoint, "/") {
		return dispatchNamedFunction(ctx, endpoint, args.Params)
	}

	// HTTP path dispatch — proxy to daemon
	method := strings.ToUpper(strings.TrimSpace(args.Method))
	if method == "" {
		method = "GET"
	}

	var body interface{}
	if method == "POST" || method == "PUT" || method == "DELETE" {
		body = args.Params
	}

	respBytes, err := proxyToDaemon(endpoint, method, body)
	if err != nil {
		return errResult(fmt.Sprintf(`{"error": "daemon proxy failed: %v"}`, err))
	}
	return textResult(string(respBytes))
}

// dispatchNamedFunction routes named function calls to inline Go implementations.
func dispatchNamedFunction(ctx context.Context, name string, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	switch name {

	// --- Local Inference ---

	case "completion":
		return apiCompletion(ctx, params)

	case "classification":
		return apiClassification(ctx, params)

	case "compact":
		return apiCompact(ctx, params)

	// --- Search ---

	case "web_search":
		return apiWebSearch(ctx, params)

	// --- Memory ---

	case "memory_query":
		return apiMemoryQuery(ctx, params)

	case "memory_ingest":
		return apiMemoryIngest(ctx, params)

	// --- Knowledge Graph ---

	case "kg_neighborhood":
		return apiKgNeighborhood(ctx, params)

	case "kg_add_entity":
		return apiKgAddEntity(ctx, params)

	case "rag_context":
		return apiRagContext(ctx, params)

	// --- Skills ---

	case "skills_list":
		return apiSkillsList(ctx, params)

	case "skills_get":
		return apiSkillsGet(ctx, params)

	case "skills_relevant":
		return apiSkillsRelevant(ctx, params)

	case "skills_add":
		return apiSkillsAdd(ctx, params)

	// --- Observer ---

	case "observer_events":
		return apiObserverEvents(ctx, params)

	case "observer_memories":
		return apiObserverMemories(ctx, params)

	// --- Sentinel ---

	case "activity_report":
		return apiActivityReport(ctx, params)

	case "sentinel_alerts":
		return apiSentinelAlerts(ctx, params)

	case "sentinel_wake":
		return apiSentinelWake(ctx, params)

	// --- Configuration ---

	case "configure_tools":
		return apiConfigureTools(ctx, params)

	// --- Schedule (action-based) ---

	case "schedule":
		return apiSchedule(ctx, params)

	// --- Apps ---

	case "apps_list":
		return apiAppsList(ctx, params)

	case "apps_install":
		return apiAppsInstall(ctx, params)

	case "apps_uninstall":
		return apiAppsUninstall(ctx, params)

	// --- Dashboard sub-actions ---

	case "dashboard_regenerate":
		return apiDashboardRegenerate(ctx, params)

	case "dashboard_spec":
		return apiDashboardSpec(ctx, params)

	// --- Cache Management ---

	case "cache_clear":
		return apiCacheClear(ctx, params)

	default:
		return errResult(fmt.Sprintf(`{"error": "unknown endpoint '%s'. Use a named function or an HTTP path starting with /"}`, name))
	}
}

// ---------------------------------------------------------------------------
// Named function implementations — thin wrappers calling existing logic
// ---------------------------------------------------------------------------

func apiCompletion(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	systemPrompt, _ := params["systemPrompt"].(string)
	userPrompt, _ := params["userPrompt"].(string)
	jsonSchema, _ := params["jsonSchema"].(string)

	if strings.TrimSpace(userPrompt) == "" {
		return errResult(`{"error": "userPrompt cannot be empty"}`)
	}

	backend := inference.ActiveBackend
	if backend == nil {
		return errResult(`{"error": "no inference backend configured"}`)
	}
	if strings.ToLower(backend.Status()) == "stopped" {
		if err := backend.Start(ctx); err != nil {
			return errResult(fmt.Sprintf(`{"error": "local model failed to start: %s"}`, err.Error()))
		}
	}

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, jsonSchema)
	if err != nil {
		return errResult(fmt.Sprintf(`{"error": "local model inference failed: %s"}`, err.Error()))
	}

	return jsonResult(map[string]interface{}{
		"content":          result.Content,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	})
}

func apiClassification(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	input, _ := params["input"].(string)
	contextHint, _ := params["context"].(string)

	if strings.TrimSpace(input) == "" {
		return errResult(`{"error": "input cannot be empty"}`)
	}

	// Extract categories from params
	var categories []string
	if cats, ok := params["categories"].([]interface{}); ok {
		for _, c := range cats {
			if s, ok := c.(string); ok {
				categories = append(categories, s)
			}
		}
	}
	if len(categories) < 2 {
		return errResult(`{"error": "at least 2 categories are required"}`)
	}

	backend := inference.ActiveBackend
	if backend == nil {
		return errResult(`{"error": "no inference backend configured"}`)
	}
	if strings.ToLower(backend.Status()) == "stopped" {
		if err := backend.Start(ctx); err != nil {
			return errResult(fmt.Sprintf(`{"error": "local model failed to start: %s"}`, err.Error()))
		}
	}

	systemPrompt := "You are a classification agent. Classify the input into exactly one of the provided categories. Respond with ONLY valid JSON matching the schema."
	if contextHint != "" {
		systemPrompt += "\n\nAdditional context: " + contextHint
	}

	userPrompt := fmt.Sprintf("Classify this input:\n\n%s\n\nValid categories: %s", input, strings.Join(categories, ", "))

	categoriesJSON, _ := json.Marshal(categories)
	jsonSchema := fmt.Sprintf(`{
		"type": "object",
		"properties": {
			"category": { "type": "string", "enum": %s },
			"confidence": { "type": "number", "minimum": 0.0, "maximum": 1.0 },
			"reasoning": { "type": "string" }
		},
		"required": ["category", "confidence", "reasoning"]
	}`, string(categoriesJSON))

	result, err := backend.CallModel(ctx, []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, jsonSchema)
	if err != nil {
		return errResult(fmt.Sprintf(`{"error": "classification inference failed: %s"}`, err.Error()))
	}

	var classResult map[string]interface{}
	if json.Unmarshal([]byte(result.Content), &classResult) != nil {
		classResult = map[string]interface{}{"raw": result.Content}
	}

	return jsonResult(map[string]interface{}{
		"classification":   classResult,
		"promptTokens":     result.PromptTokens,
		"completionTokens": result.CompletionTokens,
		"durationSeconds":  result.DurationSeconds,
		"tokensPerSecond":  result.TokensPerSecond,
	})
}

func apiCompact(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	// Re-route to existing handler via typed args
	var messages []CompactMessage
	if msgs, ok := params["messages"].([]interface{}); ok {
		for _, m := range msgs {
			if mm, ok := m.(map[string]interface{}); ok {
				messages = append(messages, CompactMessage{
					Role:    fmt.Sprintf("%v", mm["role"]),
					Content: fmt.Sprintf("%v", mm["content"]),
				})
			}
		}
	}
	if len(messages) == 0 {
		return errResult(`{"error": "messages array cannot be empty"}`)
	}

	focusHint, _ := params["focusHint"].(string)
	return handleTzroCompact(ctx, nil, TzroCompactArgs{
		Messages:  messages,
		FocusHint: focusHint,
	})
}

func apiWebSearch(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return errResult(`{"error": "query cannot be empty"}`)
	}

	limit := intParam(params, "maxResults", 5)

	results, source := tools.WebSearchMetasearch(ctx, query, limit)

	resultMaps := make([]map[string]string, 0, len(results))
	for _, r := range results {
		resultMaps = append(resultMaps, map[string]string{
			"title":   r.Title,
			"url":     r.URL,
			"snippet": r.Snippet,
		})
	}

	return jsonResult(map[string]interface{}{
		"query":   query,
		"source":  source,
		"results": resultMaps,
	})
}

func apiMemoryQuery(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	query, _ := params["query"].(string)
	if strings.TrimSpace(query) == "" {
		return errResult(`{"error": "query cannot be empty"}`)
	}

	limit := intParam(params, "limit", 10)
	mems, nodes, err := memory.DB.SearchMemoriesAndNodes(query, limit)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(map[string]interface{}{
		"memories": mems,
		"nodes":    nodes,
	})
}

func apiMemoryIngest(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroMemoryIngest(ctx, nil, TzroMemoryIngestArgs{
		Type:       strParam(params, "type"),
		Content:    strParam(params, "content"),
		UserID:     strParam(params, "userId"),
		Context:    strParam(params, "context"),
		Confidence: floatParam(params, "confidence"),
	})
}

func apiKgNeighborhood(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	entityID := strParam(params, "entityId")
	if entityID == "" {
		return errResult(`{"error": "entityId is required"}`)
	}

	maxHops := intParam(params, "maxHops", 2)
	var opts []memory.NeighborhoodOption
	if limit := intParam(params, "limit", 0); limit > 0 {
		opts = append(opts, memory.WithLimit(limit))
	}
	if dir := strParam(params, "direction"); dir != "" {
		opts = append(opts, memory.WithDirection(dir))
	}

	subgraph := memory.DB.GetEntityNeighborhood(entityID, maxHops, opts...)
	return jsonResult(subgraph)
}

func apiKgAddEntity(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	// Re-marshal params to JSON and unmarshal into typed args
	b, _ := json.Marshal(params)
	var args TzroKgAddEntityArgs
	_ = json.Unmarshal(b, &args)
	return handleTzroKgAddEntity(ctx, nil, args)
}

func apiRagContext(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	query := strParam(params, "query")
	if query == "" {
		return errResult(`{"error": "query is required"}`)
	}
	maxChars := intParam(params, "maxChars", 2000)
	ragStr := memory.DB.GetGraphRAGContext(query, maxChars)
	return jsonResult(map[string]interface{}{"context": ragStr})
}

func apiSkillsList(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	skills := memory.DB.GetSkills()
	limit := intParam(params, "limit", 0)
	if limit > 0 && len(skills) > limit {
		skills = skills[:limit]
	}
	return jsonResult(skills)
}

func apiSkillsGet(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	id := strParam(params, "id")
	if id == "" {
		return errResult(`{"error": "id is required"}`)
	}
	return handleTzroSkillsGet(ctx, nil, TzroSkillsGetArgs{ID: id})
}

func apiSkillsRelevant(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	prompt := strParam(params, "prompt")
	if prompt == "" {
		return errResult(`{"error": "prompt is required"}`)
	}
	return handleTzroSkillsRelevant(ctx, nil, TzroSkillsRelevantArgs{
		Prompt: prompt,
		Limit:  intParam(params, "limit", 5),
	})
}

func apiSkillsAdd(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroSkillsAdd(ctx, nil, TzroSkillsAddArgs{
		Name:               strParam(params, "name"),
		TriggerDescription: strParam(params, "triggerDescription"),
		SOPContent:         strParam(params, "sopContent"),
	})
}

func apiObserverEvents(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroObserverEvents(ctx, nil, TzroObserverEventsArgs{
		Limit: intParam(params, "limit", 10),
	})
}

func apiObserverMemories(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroObserverMemories(ctx, nil, TzroObserverMemoriesArgs{
		Limit: intParam(params, "limit", 10),
	})
}

func apiActivityReport(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	activity := strParam(params, "activity")
	if activity == "" {
		return errResult(`{"error": "activity cannot be empty"}`)
	}

	var filesTouched, toolsUsed []string
	if ft, ok := params["filesTouched"].([]interface{}); ok {
		for _, f := range ft {
			if s, ok := f.(string); ok {
				filesTouched = append(filesTouched, s)
			}
		}
	}
	if tu, ok := params["toolsUsed"].([]interface{}); ok {
		for _, t := range tu {
			if s, ok := t.(string); ok {
				toolsUsed = append(toolsUsed, s)
			}
		}
	}

	report := sentinel.ActivityReport{
		Activity:     activity,
		FilesTouched: filesTouched,
		ToolsUsed:    toolsUsed,
		Timestamp:    time.Now().Unix(),
	}
	sentinel.DefaultAgent.IngestActivityReport(report)

	return jsonResult(map[string]string{"status": "acknowledged"})
}

func apiSentinelAlerts(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroSentinelAlerts(ctx, nil, TzroSentinelAlertsArgs{
		Status: strParam(params, "status"),
		Limit:  intParam(params, "limit", 10),
	})
}

func apiSentinelWake(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroSentinelWake(ctx, nil, TzroSentinelWakeArgs{
		ContextHint: strParam(params, "contextHint"),
	})
}

func apiConfigureTools(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	// Re-marshal and pass to existing handler
	b, _ := json.Marshal(params)
	var args TzroConfigureToolsArgs
	_ = json.Unmarshal(b, &args)
	return handleTzroConfigureTools(ctx, nil, args)
}

func apiSchedule(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	b, _ := json.Marshal(params)
	var args TzroScheduleArgs
	_ = json.Unmarshal(b, &args)
	return handleTzroSchedule(ctx, nil, args)
}

func apiAppsList(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroAppsList(ctx, nil, TzroAppsListArgs{})
}

func apiAppsInstall(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroAppsInstall(ctx, nil, TzroAppsInstallArgs{
		ArchivePath: strParam(params, "archivePath"),
	})
}

func apiAppsUninstall(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroAppsUninstall(ctx, nil, TzroAppsUninstallArgs{
		AppID: strParam(params, "appId"),
		Purge: boolParam(params, "purge"),
	})
}

func apiDashboardRegenerate(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroDashboardRegenerate(ctx, nil, TzroDashboardRegenerateArgs{
		Wait: boolParam(params, "wait"),
	})
}

func apiDashboardSpec(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	return handleTzroDashboardSpec(ctx, nil, TzroDashboardSpecArgs{})
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func errResult(msg string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
		IsError: true,
	}, nil, nil
}

func textResult(text string) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}, nil, nil
}

func jsonResult(v interface{}) (*mcp.CallToolResult, any, error) {
	b, _ := json.MarshalIndent(v, "", "  ")
	return textResult(string(b))
}

func strParam(params map[string]interface{}, key string) string {
	if params == nil {
		return ""
	}
	v, _ := params[key].(string)
	return v
}

func intParam(params map[string]interface{}, key string, defaultVal int) int {
	if params == nil {
		return defaultVal
	}
	if v, ok := params[key].(float64); ok {
		return int(v)
	}
	if v, ok := params[key].(int); ok {
		return v
	}
	return defaultVal
}

func floatParam(params map[string]interface{}, key string) float64 {
	if params == nil {
		return 0
	}
	if v, ok := params[key].(float64); ok {
		return v
	}
	return 0
}

func boolParam(params map[string]interface{}, key string) bool {
	if params == nil {
		return false
	}
	v, _ := params[key].(bool)
	return v
}

// apiCacheClear clears the image cache and returns the number of files removed.
func apiCacheClear(ctx context.Context, params map[string]interface{}) (*mcp.CallToolResult, any, error) {
	removed, err := content.ClearImageCache()
	if err != nil {
		return errResult(fmt.Sprintf(`{"error": "failed to clear cache: %v"}`, err))
	}

	result := fmt.Sprintf(`{"success": true, "filesRemoved": %d}`, removed)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result}},
	}, nil, nil
}
