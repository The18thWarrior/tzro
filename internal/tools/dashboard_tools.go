package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"tzro/internal/cache"
	"tzro/internal/config"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// MetricItem matches the frontend MetricCard props
type MetricItem struct {
	Label      string `json:"label"`
	Value      string `json:"value"`
	Trend      string `json:"trend,omitempty"`      // "up" | "down" | "stable"
	TrendValue string `json:"trendValue,omitempty"` // e.g. "+12%"
}

// ==========================================
// 1. gather_metrics tool
// ==========================================

type GatherMetricsTool struct{}

func (g *GatherMetricsTool) Name() string { return "gather_metrics" }
func (g *GatherMetricsTool) GetSchema() (string, error) {
	return GetToolGBNFSchema(map[string]interface{}{}, []string{}), nil
}
func (g *GatherMetricsTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	db := memory.DB.RawDB()
	if db == nil {
		return "", fmt.Errorf("database not initialized")
	}

	now := time.Now().Unix()
	dayAgo := now - 24*3600
	twoDaysAgo := now - 48*3600

	// 1. Total tasks in last 24h
	var tasks24h int
	_ = db.QueryRow("SELECT COUNT(DISTINCT task_id) FROM node_states WHERE completed_at >= ?", dayAgo).Scan(&tasks24h)

	// 2. Total tasks in previous 24h (24h-48h ago)
	var tasksPrev int
	_ = db.QueryRow("SELECT COUNT(DISTINCT task_id) FROM node_states WHERE completed_at >= ? AND completed_at < ?", twoDaysAgo, dayAgo).Scan(&tasksPrev)

	// 3. Total successful tasks in last 24h (tasks with no failed nodes)
	var success24h int
	_ = db.QueryRow(`SELECT COUNT(DISTINCT task_id) FROM node_states 
		WHERE completed_at >= ? AND task_id NOT IN (
			SELECT DISTINCT task_id FROM node_states WHERE status = 'failed' AND completed_at >= ?
		)`, dayAgo, dayAgo).Scan(&success24h)

	// Calculate Success Rate
	successRate := 100.0
	if tasks24h > 0 {
		successRate = (float64(success24h) * 100.0) / float64(tasks24h)
	}

	// Calculate Task Count Trend
	trend := "stable"
	trendValue := "0%"
	if tasksPrev > 0 {
		diff := tasks24h - tasksPrev
		pct := (float64(diff) * 100.0) / float64(tasksPrev)
		if pct > 0 {
			trend = "up"
			trendValue = fmt.Sprintf("+%.0f%%", pct)
		} else if pct < 0 {
			trend = "down"
			trendValue = fmt.Sprintf("%.0f%%", pct)
		}
	} else if tasks24h > 0 {
		trend = "up"
		trendValue = fmt.Sprintf("+%d tasks", tasks24h)
	}

	// Average TPS and Cache Hit Rate
	avgTPS := inference.GetGlobalAverageTPS()
	cacheHitRate := cache.GetCacheHitRate()

	sidecarStatus, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()

	metrics := []MetricItem{
		{
			Label:      "Total Tasks (24h)",
			Value:      fmt.Sprintf("%d", tasks24h),
			Trend:      trend,
			TrendValue: trendValue,
		},
		{
			Label: "Success Rate",
			Value: fmt.Sprintf("%.0f%%", successRate),
			Trend: "stable",
		},
		{
			Label: "Avg Inference Speed",
			Value: fmt.Sprintf("%.1f t/s", avgTPS),
			Trend: "stable",
		},
		{
			Label: "Cache Hit Rate",
			Value: fmt.Sprintf("%.1f%%", cacheHitRate*100.0),
			Trend: "stable",
		},
		{
			Label: "Local Sidecar",
			Value: sidecarStatus,
			Trend: "stable",
		},
	}

	resBytes, _ := json.Marshal(metrics)
	return string(resBytes), nil
}

// ==========================================
// 2. gather_tasks tool
// ==========================================

type GatherTasksTool struct{}

func (g *GatherTasksTool) Name() string { return "gather_tasks" }
func (g *GatherTasksTool) GetSchema() (string, error) {
	return GetToolGBNFSchema(map[string]interface{}{}, []string{}), nil
}
func (g *GatherTasksTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	recent, err := memory.DB.GetRecentTasks(10, "all")
	if err != nil {
		recent = []memory.TaskSummary{}
	}

	// Find spotlight candidates (recent failed tasks or active ones)
	var spotlightCandidates []string
	db := memory.DB.RawDB()
	if db != nil {
		rows, err := db.Query(`SELECT DISTINCT task_id FROM node_states 
			WHERE status = 'failed' ORDER BY completed_at DESC LIMIT 3`)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var tid string
				if err := rows.Scan(&tid); err == nil {
					spotlightCandidates = append(spotlightCandidates, tid)
				}
			}
		}
	}

	// If no failed tasks, pick the most recent completed task
	if len(spotlightCandidates) == 0 && len(recent) > 0 {
		spotlightCandidates = append(spotlightCandidates, recent[0].TaskID)
	}

	result := map[string]interface{}{
		"recentTasks":         recent,
		"spotlightCandidates": spotlightCandidates,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

// ==========================================
// 3. gather_config tool
// ==========================================

type GatherConfigTool struct{}

func (g *GatherConfigTool) Name() string { return "gather_config" }
func (g *GatherConfigTool) GetSchema() (string, error) {
	return GetToolGBNFSchema(map[string]interface{}{}, []string{}), nil
}
func (g *GatherConfigTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	cfg := config.Get()
	status, port, pid, progress, modelPath := inference.GlobalLocalModel.GetStatusInfo()

	// Get download models info
	modelsCatalog := inference.GetCatalog()
	modelsDir := config.GetModelsDir()
	var downloadedModels []string
	for _, entry := range modelsCatalog {
		p := filepath.Join(modelsDir, entry.Filename)
		if _, err := os.Stat(p); err == nil {
			downloadedModels = append(downloadedModels, entry.ID)
		}
	}

	result := map[string]interface{}{
		"config": map[string]interface{}{
			"modelMode":      cfg.ModelMode,
			"sidecarEnabled": cfg.SidecarEnabled,
			"activeModel":    cfg.GGUFModelPath,
			"privacyLevel":   cfg.PrivacyLevel,
		},
		"sidecar": map[string]interface{}{
			"status":           status,
			"activePort":       port,
			"activePid":        pid,
			"manifestProgress": progress,
			"ggufModelPath":    modelPath,
		},
		"downloadedModels": downloadedModels,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

// ==========================================
// 4. gather_workflows tool
// ==========================================

type GatherWorkflowsTool struct{}

func (g *GatherWorkflowsTool) Name() string { return "gather_workflows" }
func (g *GatherWorkflowsTool) GetSchema() (string, error) {
	return GetToolGBNFSchema(map[string]interface{}{}, []string{}), nil
}
func (g *GatherWorkflowsTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	workflows, err := memory.DB.GetWorkflows()
	if err != nil {
		workflows = []memory.WorkflowDefinition{}
	}

	var workflowWithTasks []map[string]interface{}
	for _, wf := range workflows {
		tasks, _ := memory.DB.GetWorkflowTasks(wf.ID)
		workflowWithTasks = append(workflowWithTasks, map[string]interface{}{
			"id":            wf.ID,
			"name":          wf.Name,
			"description":   wf.Description,
			"triggerType":   wf.TriggerType,
			"triggerConfig": wf.TriggerConfig,
			"status":        wf.Status,
			"nextRunAt":     wf.NextRunAt,
			"tasks":         tasks,
		})
	}

	executions, err := memory.DB.GetWorkflowExecutions("")
	if err != nil {
		executions = []memory.WorkflowExecution{}
	}

	// Select spotlight candidate: most recent failed workflow execution
	var spotlightCandidates []string
	for _, ex := range executions {
		if ex.Status == "failed" {
			spotlightCandidates = append(spotlightCandidates, ex.WorkflowID)
			break
		}
	}
	if len(spotlightCandidates) == 0 && len(workflows) > 0 {
		spotlightCandidates = append(spotlightCandidates, workflows[0].ID)
	}

	result := map[string]interface{}{
		"workflows":           workflowWithTasks,
		"executions":          executions,
		"spotlightCandidates": spotlightCandidates,
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

// ==========================================
// 5. compose_layout tool
// ==========================================

type ComposeLayoutTool struct{}

func (g *ComposeLayoutTool) Name() string { return "compose_layout" }
func (g *ComposeLayoutTool) GetSchema() (string, error) {
	// Enforce non-recursive, flat depth-4 layout specification
	level4Schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"type":  map[string]interface{}{"type": "string", "enum": []string{"MetricCard", "TaskTable", "EventFeed", "ConfigPanel", "SidecarStatus", "NotificationList", "WorkflowMonitor", "TaskSpotlight", "WorkflowSpotlight", "Annotation"}},
			"props": map[string]interface{}{"type": "object"},
		},
		"required": []string{"type"},
	}

	level3Schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"type":    map[string]interface{}{"type": "string", "enum": []string{"MetricCard", "TaskTable", "EventFeed", "ConfigPanel", "SidecarStatus", "NotificationList", "WorkflowMonitor", "TaskSpotlight", "WorkflowSpotlight", "Annotation", "Stack", "Grid", "Section"}},
			"columns": map[string]interface{}{"type": "integer"},
			"props":   map[string]interface{}{"type": "object"},
			"children": map[string]interface{}{
				"type":  "array",
				"items": level4Schema,
			},
		},
		"required": []string{"type"},
	}

	level2Schema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"type":    map[string]interface{}{"type": "string", "enum": []string{"MetricCard", "TaskTable", "EventFeed", "ConfigPanel", "SidecarStatus", "NotificationList", "WorkflowMonitor", "TaskSpotlight", "WorkflowSpotlight", "Annotation", "Stack", "Grid", "Section"}},
			"columns": map[string]interface{}{"type": "integer"},
			"props":   map[string]interface{}{"type": "object"},
			"children": map[string]interface{}{
				"type":  "array",
				"items": level3Schema,
			},
		},
		"required": []string{"type"},
	}

	componentSchema := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"type":    map[string]interface{}{"type": "string", "enum": []string{"MetricCard", "TaskTable", "EventFeed", "ConfigPanel", "SidecarStatus", "NotificationList", "WorkflowMonitor", "TaskSpotlight", "WorkflowSpotlight", "Annotation", "Stack", "Grid", "Section"}},
			"columns": map[string]interface{}{"type": "integer"},
			"props":   map[string]interface{}{"type": "object"},
			"children": map[string]interface{}{
				"type":  "array",
				"items": level2Schema,
			},
		},
		"required": []string{"type"},
	}

	dashboardSpecSchema := map[string]interface{}{
		"version":     map[string]interface{}{"type": "integer"},
		"generatedAt": map[string]interface{}{"type": "integer"},
		"ttlSeconds":  map[string]interface{}{"type": "integer"},
		"layout":      componentSchema,
	}

	requiredKeys := []string{"version", "generatedAt", "ttlSeconds", "layout"}
	return GetToolGBNFSchema(dashboardSpecSchema, requiredKeys), nil
}
func (g *ComposeLayoutTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	// The LLM writes output into the tool arguments. We simply marshal and return it.
	resBytes, err := json.Marshal(args)
	if err != nil {
		return "", err
	}
	return string(resBytes), nil
}

// ==========================================
// 6. terminal_synthesis tool
// ==========================================

type TerminalSynthesisTool struct{}

func (g *TerminalSynthesisTool) Name() string { return "terminal_synthesis" }
func (g *TerminalSynthesisTool) GetSchema() (string, error) {
	return GetToolGBNFSchema(map[string]interface{}{
		"spec": map[string]interface{}{"type": "object", "description": "The dashboard spec JSON to validate and persist"},
	}, []string{"spec"}), nil
}
func (g *TerminalSynthesisTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	specArg, ok := args["spec"]
	if !ok {
		// Try parsing from raw instruction input or variables fallback
		return "", fmt.Errorf("missing 'spec' argument")
	}

	specBytes, err := json.Marshal(specArg)
	if err != nil {
		return "", fmt.Errorf("failed to serialize spec: %w", err)
	}

	// Basic validation: ensure layout, version, generatedAt exist
	var parsed map[string]interface{}
	if err := json.Unmarshal(specBytes, &parsed); err != nil {
		return "", fmt.Errorf("invalid spec JSON: %w", err)
	}

	if _, ok := parsed["version"]; !ok {
		return "", fmt.Errorf("missing version key in spec")
	}
	if _, ok := parsed["generatedAt"]; !ok {
		return "", fmt.Errorf("missing generatedAt key in spec")
	}
	if _, ok := parsed["layout"]; !ok {
		return "", fmt.Errorf("missing layout tree in spec")
	}

	// Extract generator task ID from context if possible, or fallback
	generatorTaskID := "task_system_gen"
	if ctxNodeID := ctx.Value("nodeID"); ctxNodeID != nil {
		generatorTaskID = fmt.Sprintf("%v", ctxNodeID)
	}

	// Generate a unique spec ID
	specID := fmt.Sprintf("spec_%d", time.Now().UnixNano())
	generatedAt := time.Now().Unix()
	ttlSeconds := int64(14400) // default 4h

	if ttlVal, ok := parsed["ttlSeconds"].(float64); ok {
		ttlSeconds = int64(ttlVal)
	}

	// Save to SQLite DB
	err = memory.DB.SaveDashboardSpec(specID, string(specBytes), generatedAt, generatorTaskID, ttlSeconds)
	if err != nil {
		return "", fmt.Errorf("failed to save dashboard spec to database: %w", err)
	}

	// Count number of components used (primitives used count)
	primitivesCount := countPrimitives(parsed["layout"])

	result := map[string]interface{}{
		"specId":         specID,
		"primitivesUsed": primitivesCount,
		"generatedAt":    generatedAt,
		"status":         "completed",
	}

	resBytes, _ := json.Marshal(result)
	return string(resBytes), nil
}

// Recursive helper to count primitives in layout
func countPrimitives(layoutObj interface{}) int {
	if layoutObj == nil {
		return 0
	}
	m, ok := layoutObj.(map[string]interface{})
	if !ok {
		return 0
	}

	count := 1 // Count itself
	children, exists := m["children"]
	if !exists {
		return count
	}

	arr, ok := children.([]interface{})
	if !ok {
		return count
	}

	for _, child := range arr {
		count += countPrimitives(child)
	}
	return count
}
