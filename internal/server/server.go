package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"
	"tzro/internal/channel"
	"tzro/internal/classifier"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/mcp"
	"tzro/internal/memory"
	"tzro/internal/notification"
	"tzro/internal/packagemanager"
	"tzro/internal/proactivity"
	"tzro/internal/stream"
	"tzro/internal/task"
	"tzro/internal/tools"
	"tzro/internal/workflow"
)

// recoverTaskPanic recovers from panics in background task goroutines.
// Without this, an unrecovered panic in any ExecuteGraph or task.Execute
// goroutine crashes the entire daemon process with no stack trace.
func recoverTaskPanic(taskID string) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		fmt.Fprintf(os.Stderr, "[Server] PANIC in task %s: %v\n%s\n", taskID, r, stack)
		stream.GlobalBus.Publish(stream.StreamChunk{
			Source:  "system",
			TaskID:  taskID,
			Type:    "task_panic",
			Content: fmt.Sprintf("Panic recovered: %v", r),
		})
	}
}

type ChatRequest struct {
	Message string `json:"message"`
}

type ChatResponse struct {
	Intent     classifier.IntentResult  `json:"intent"`
	Complexity string                   `json:"complexity"`
	TaskID     string                   `json:"taskId"`
	StreamID   string                   `json:"streamId,omitempty"`
	Graph      *compiler.ExecutionGraph `json:"graph,omitempty"`
	Levels     [][]string               `json:"levels,omitempty"`
	Message    string                   `json:"message"`
}

var (
	activeGraphs    = make(map[string]*compiler.ExecutionGraph)
	activeCancels   = make(map[string]context.CancelFunc)
	graphsMutex     sync.RWMutex
	DaemonStartTime = time.Now()
)

// StartServer starts the HTTP router on the specified port.
// It serves API routes and binds static dashboard assets.
func StartServer(addr string) error {
	mux := http.NewServeMux()

	// CORS wrapper
	corsHandler := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			if r.Method == "OPTIONS" {
				w.WriteHeader(http.StatusOK)
				return
			}
			h(w, r)
		}
	}

	mux.HandleFunc("/health", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"healthy"}`))
	}))
	mux.HandleFunc("/api/chat", corsHandler(handleChat))
	mux.HandleFunc("/api/tasks", corsHandler(handleTasks))
	mux.HandleFunc("/api/memories", corsHandler(handleMemories))
	mux.HandleFunc("/api/memories/node", corsHandler(handleNodeAdd))
	mux.HandleFunc("/api/memories/edge", corsHandler(handleEdgeAdd))
	mux.HandleFunc("/api/skills", corsHandler(handleSkills))
	mux.HandleFunc("/api/mcp", corsHandler(handleMCP))
	mux.HandleFunc("/api/mcp/openapi", corsHandler(handleOpenAPIMCP))
	mux.HandleFunc("/api/entity-types", corsHandler(handleEntityTypes))
	mux.HandleFunc("/api/config", corsHandler(handleConfig))
	mux.HandleFunc("/api/sidecar", corsHandler(handleSidecar))
	mux.HandleFunc("/api/models", corsHandler(handleModels))
	mux.HandleFunc("/api/models/download", corsHandler(handleModelDownload))
	mux.HandleFunc("/api/events", corsHandler(handleEvents))
	mux.HandleFunc("/api/notifications", corsHandler(handleNotifications))
	mux.HandleFunc("/api/tasks/events", corsHandler(handleTaskSSE))
	mux.HandleFunc("/api/tasks/resume", corsHandler(handleTasksResume))
	mux.HandleFunc("/api/tasks/approve", corsHandler(handleTasksApprove))
	mux.HandleFunc("/api/tasks/cancel", corsHandler(handleTasksCancel))
	mux.HandleFunc("/api/tasks/run", corsHandler(handleTasksRun))
	mux.HandleFunc("/api/apps/install", corsHandler(handleAppsInstall))
	mux.HandleFunc("/api/apps/uninstall", corsHandler(handleAppsUninstall))
	mux.HandleFunc("/api/apps", corsHandler(handleAppsList))

	// Daemon lifecycle
	mux.HandleFunc("/api/restart", corsHandler(handleRestart))

	// Workflows routing
	mux.HandleFunc("/api/workflows", corsHandler(handleWorkflows))
	mux.HandleFunc("/api/workflows/toggle", corsHandler(handleWorkflowToggle))
	mux.HandleFunc("/api/workflows/trigger", corsHandler(handleWorkflowTrigger))
	mux.HandleFunc("/api/workflows/executions", corsHandler(handleWorkflowExecutions))
	mux.HandleFunc("/api/workflows/executions/detail", corsHandler(handleWorkflowExecutionDetail))

	// Dashboard routing
	mux.HandleFunc("/api/dashboard/spec", corsHandler(handleDashboardSpec))
	mux.HandleFunc("/api/dashboard/regenerate", corsHandler(handleDashboardRegenerate))

	// Serve Dashboard static files at root with SPA routing fallback
	staticDir := "./static"
	mux.HandleFunc("/", corsHandler(func(w http.ResponseWriter, r *http.Request) {
		p := path.Clean(r.URL.Path)
		if p == "/" || p == "." {
			p = "index.html"
		} else {
			p = strings.TrimPrefix(p, "/")
		}

		fullPath := filepath.Join(staticDir, p)
		info, err := os.Stat(fullPath)
		if err != nil || info.IsDir() {
			// SPA fallback
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
			return
		}
		http.ServeFile(w, r, fullPath)
	}))

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		if os.Getenv("PORT") == "" {
			fmt.Printf("[Server] Port %s already in use. Falling back to dynamic port allocation...\n", addr)
			listener, err = net.Listen("tcp", "127.0.0.1:0")
		}
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", addr, err)
		}
	}
	defer listener.Close()

	actualAddr := listener.Addr().(*net.TCPAddr)
	actualPort := actualAddr.Port

	_ = config.WriteDaemonPort(actualPort)
	defer config.RemoveDaemonPort()

	_ = config.WriteDashboardLock(os.Getpid(), actualPort)
	defer config.RemoveDashboardLock()

	fmt.Printf("[Server] Unified HTTP Router listening on http://127.0.0.1:%d...\n", actualPort)
	return http.Serve(listener, mux)
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// 1. Intent & Complexity classification
	intent := classifier.Classify(r.Context(), req.Message)

	// Collect configured tools names for complexity checks
	daemonsConfig := mcp.GlobalRegistry.GetList()
	var toolNames []string
	for k := range daemonsConfig {
		toolNames = append(toolNames, k)
	}
	complexity := classifier.ClassifyComplexity(r.Context(), req.Message, toolNames)

	resp := ChatResponse{
		Intent:     intent,
		Complexity: complexity,
		TaskID:     fmt.Sprintf("task_%d", time.Now().Unix()),
		Message:    intent.Summary,
	}

	// 1.5 Promotion Check: If type == "chat" AND complexity == "T1" or "T2", promote it!
	if intent.Type == "chat" && (complexity == "T1" || complexity == "T2") {
		// Publish promotion event
		stream.GlobalBus.Publish(stream.StreamChunk{
			Source:  "system",
			Type:    "promotion",
			TaskID:  resp.TaskID,
			Content: "Upgrading to planned workflow — this requires multiple tools.",
		})
		// Promote intent type to workflow to compile a DAG graph below
		intent.Type = "workflow"
		resp.Intent.Type = "workflow"
	}

	// 2. If intent is workflow or heartbeat, compile a DAG graph
	if intent.Type == "workflow" || intent.Type == "heartbeat" || intent.Type == "research" {
		graph, err := task.Plan(r.Context(), req.Message, task.ExecuteOptions{
			TaskID:     resp.TaskID,
			IntentType: intent.Type,
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("Graph planning error: %v", err), http.StatusInternalServerError)
			return
		}

		levels, err := compiler.CompileAndSort(graph)
		if err != nil {
			http.Error(w, fmt.Sprintf("Graph compilation error: %v", err), http.StatusInternalServerError)
			return
		}

		resp.Graph = graph
		resp.Levels = levels

		// Save graph in active mapping with cancellable context
		taskCtx, taskCancel := context.WithCancel(context.Background())
		graphsMutex.Lock()
		activeGraphs[resp.TaskID] = graph
		activeCancels[resp.TaskID] = taskCancel
		graphsMutex.Unlock()

		// Run graph in background asynchronously to prevent HTTP blocking
		go func() {
			defer recoverTaskPanic(graph.TaskID)
			defer func() {
				graphsMutex.Lock()
				delete(activeCancels, graph.TaskID)
				graphsMutex.Unlock()
			}()
			proactivity.RegisterActiveUserTask(graph.TaskID)
			defer proactivity.DeregisterActiveUserTask(graph.TaskID)

			err := executor.GlobalEngine.ExecuteGraph(taskCtx, graph, levels)
			if err != nil {
				fmt.Printf("[Server Executor Error] %v\n", err)
			}

			// Log dialogue turn to memory (Session History Compaction support)
			var executedTools []string
			for _, node := range graph.Nodes {
				state, ok := memory.DB.GetNodeState(graph.TaskID, node.ID)
				if ok && state.Status == "completed" {
					executedTools = append(executedTools, fmt.Sprintf("%s(instructions: %q, output: %q)", node.Action, node.Instructions, state.Output))
				} else {
					executedTools = append(executedTools, fmt.Sprintf("%s(instructions: %q, status: %q)", node.Action, node.Instructions, state.Status))
				}
			}
			sessionID := memory.GetSessionID(graph.TaskID)
			memory.DB.AddSessionTurn(sessionID, req.Message, executedTools)
		}()
	} else {
		// Conversational query of T0 complexity - streams LLM response in the background
		streamID := fmt.Sprintf("s_%d", time.Now().UnixNano())
		resp.StreamID = streamID
		resp.Message = ""

		go func(taskID, streamID, userMessage string) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()

			cfg := config.Get()
			sidecarStatus, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
			isSidecarActive := (sidecarStatus == "Active" || sidecarStatus == "Adopted")
			useLocal := isSidecarActive && cfg.ModelMode != "cloud"

			meta := inference.StreamMeta{
				StreamID: streamID,
				Source:   "chat",
				TaskID:   taskID,
			}

			systemPrompt := "You are a helpful conversational AI assistant. Keep your responses clear, helpful, and concise."
			ragCtx := memory.DB.GetGraphRAGContext(userMessage, config.GetMaxRAGContextChars())
			if ragCtx != "" {
				systemPrompt += "\n\n" + ragCtx
			}

			var finalReply string
			if useLocal {
				msgs := []inference.InferenceMessage{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userMessage},
				}
				res, err := inference.GlobalLocalModel.CallLocalModelStream(ctx, msgs, "", meta)
				if err == nil && res != nil {
					finalReply = res.Content
				}
			} else {
				msgs := []inference.InferenceMessage{
					{Role: "system", Content: systemPrompt},
					{Role: "user", Content: userMessage},
				}
				reply, err := inference.CallCloudModelStream(ctx, msgs, "", meta, nil)
				if err == nil {
					finalReply = reply
				}
			}

			if finalReply != "" {
				sessionID := memory.GetSessionID(taskID)
				memory.DB.AddSessionTurn(sessionID, userMessage, []string{fmt.Sprintf("reply(%s)", finalReply)})
			}
		}(resp.TaskID, streamID, req.Message)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	graphsMutex.RLock()
	defer graphsMutex.RUnlock()

	type TaskStateItem struct {
		TaskID     string                   `json:"taskId"`
		Graph      *compiler.ExecutionGraph `json:"graph,omitempty"`
		States     map[string]interface{}   `json:"states,omitempty"`
		CreatedAt  int64                    `json:"createdAt"`
		Prompt     string                   `json:"prompt"`
		IntentType string                   `json:"intentType"`
		Status     string                   `json:"status"`
		NodeCount  int                      `json:"nodeCount"`
	}

	seen := make(map[string]bool)
	var list []TaskStateItem

	// 1. Active in-memory graphs (currently running tasks)
	for taskID, g := range activeGraphs {
		statesMap := make(map[string]interface{})
		derivedStatus := "running"
		hasFailed := false
		allCompleted := true
		for _, node := range g.Nodes {
			state, ok := memory.DB.GetNodeState(taskID, node.ID)
			if ok {
				statesMap[node.ID] = state
				if state.Status == "failed" {
					hasFailed = true
				}
				if state.Status != "completed" {
					allCompleted = false
				}
			} else {
				statesMap[node.ID] = map[string]string{"status": "pending", "output": ""}
				allCompleted = false
			}
		}
		if hasFailed {
			derivedStatus = "failed"
		} else if allCompleted && len(g.Nodes) > 0 {
			derivedStatus = "completed"
		}

		list = append(list, TaskStateItem{
			TaskID:     taskID,
			Graph:      g,
			States:     statesMap,
			CreatedAt:  g.CreatedAt,
			Prompt:     g.GoalPrompt,
			IntentType: "task",
			Status:     derivedStatus,
			NodeCount:  len(g.Nodes),
		})
		seen[taskID] = true
	}

	// 2. Historical tasks from SQLite (recently completed/failed)
	historical, err := memory.DB.GetRecentTasks(20, "all")
	if err == nil {
		for _, ht := range historical {
			if seen[ht.TaskID] {
				continue // Active graph takes priority
			}

			// Reconstruct a minimal graph and states map from persisted node_states
			// so the DAG view can render node tiles even for completed/historical tasks.
			nodeStates := memory.DB.GetAllNodeStates(ht.TaskID)
			var reconstructedGraph *compiler.ExecutionGraph
			statesMap := make(map[string]interface{})

			if len(nodeStates) > 0 {
				var nodes []compiler.GraphNode
				for _, ns := range nodeStates {
					nodes = append(nodes, compiler.GraphNode{
						ID:     ns.NodeID,
						Type:   "action",
						Status: ns.Status,
						Output: ns.Output,
					})
					statesMap[ns.NodeID] = map[string]string{
						"status": ns.Status,
						"output": ns.Output,
					}
				}
				reconstructedGraph = &compiler.ExecutionGraph{
					TaskID:    ht.TaskID,
					Nodes:     nodes,
					Edges:     []compiler.GraphEdge{}, // Edges are not persisted
					CreatedAt: ht.CreatedAt,
				}
			}

			list = append(list, TaskStateItem{
				TaskID:     ht.TaskID,
				Graph:      reconstructedGraph,
				States:     statesMap,
				CreatedAt:  ht.CreatedAt,
				Prompt:     "", // Historical tasks don't retain the prompt in node_states
				IntentType: "task",
				Status:     ht.Status,
				NodeCount:  ht.NodeCount,
			})
			seen[ht.TaskID] = true
		}
	}

	// Sort descending by createdAt
	for i := 0; i < len(list); i++ {
		for j := i + 1; j < len(list); j++ {
			if list[j].CreatedAt > list[i].CreatedAt {
				list[i], list[j] = list[j], list[i]
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func handleMemories(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var req struct {
			UserID     string  `json:"userId"`
			Type       string  `json:"type"`
			Content    string  `json:"content"`
			Context    string  `json:"context"`
			Confidence float64 `json:"confidence"`
			Source     string  `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		m := memory.FactMemory{
			UserID:     req.UserID,
			Type:       req.Type,
			Content:    req.Content,
			Context:    req.Context,
			Confidence: req.Confidence,
			Source:     req.Source,
		}
		if m.UserID == "" {
			m.UserID = "default"
		}
		if m.Source == "" {
			m.Source = "manual_api"
		}
		if err := memory.DB.AddMemory(m); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"success"}`))
		return
	}

	// Support Graph-RAG neighborhood traversal
	nodeID := r.URL.Query().Get("neighborhood")
	if nodeID != "" {
		subgraph := memory.DB.GetEntityNeighborhood(nodeID, 2)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(subgraph)
		return
	}

	type MemoryPayload struct {
		Facts []memory.FactMemory `json:"facts"`
		Nodes []memory.KGNode     `json:"nodes"`
		Edges []memory.KGEdge     `json:"edges"`
	}

	facts := memory.DB.GetMemories()
	nodeMap := memory.DB.GetNodes()
	edgeMap := memory.DB.GetEdges()

	var nodes []memory.KGNode
	for _, n := range nodeMap {
		nodes = append(nodes, n)
	}

	var edges []memory.KGEdge
	for _, e := range edgeMap {
		edges = append(edges, e)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(MemoryPayload{
		Facts: facts,
		Nodes: nodes,
		Edges: edges,
	})
}

func handleNodeAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var n memory.KGNode
	if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	n.Weight = 1.0
	n.Source = "manual_gui"
	err := memory.DB.AddNode(n)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"status":"success"}`))
}

func handleEdgeAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var e memory.KGEdge
	if err := json.NewDecoder(r.Body).Decode(&e); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	e.Weight = 1.0
	e.ID = fmt.Sprintf("edge_%d", time.Now().UnixNano())
	err := memory.DB.AddEdge(e)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"status":"success"}`))
}

func handleSkills(w http.ResponseWriter, r *http.Request) {
	skillsList := memory.DB.GetSkills()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(skillsList)
}

func handleMCP(w http.ResponseWriter, r *http.Request) {
	daemonsList := mcp.GlobalRegistry.GetList()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(daemonsList)
}

func handleEntityTypes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		types := memory.DB.GetEntityTypes()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types)

	case "POST":
		var et memory.EntityType
		if err := json.NewDecoder(r.Body).Decode(&et); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if et.ID == "" || et.Label == "" {
			http.Error(w, "id and label are required", http.StatusBadRequest)
			return
		}
		if et.Color == "" {
			et.Color = "hsl(220, 70%, 55%)" // sensible default
		}
		if err := memory.DB.AddEntityType(et); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "created", "id": et.ID})

	case "DELETE":
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}
		if err := memory.DB.DeleteEntityType(id); err != nil {
			if strings.Contains(err.Error(), "built-in") {
				http.Error(w, err.Error(), http.StatusForbidden)
			} else {
				http.Error(w, err.Error(), http.StatusNotFound)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		cfg := config.Get()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cfg)
		return
	}

	if r.Method == "POST" {
		var cfg config.EngineConfig
		if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := config.Save(&cfg); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Sync sidecar startup setting change — manages both worker and router
		if cfg.SidecarEnabled {
			go func() {
				_ = inference.StartActive(context.Background())
			}()
		} else {
			go func() {
				_ = inference.StopActive()
			}()
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleSidecar(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		wStatus, wPort, wPid, wProgress, wModel := inference.GlobalWorkerModel.GetStatusInfo()
		rStatus, rPort, rPid, _, rModel := inference.GlobalRouterModel.GetStatusInfo()

		resp := map[string]interface{}{
			"activePort":       wPort,
			"activePid":        wPid,
			"status":           wStatus,
			"manifestProgress": wProgress,
			"ggufModelPath":    wModel,
		}

		// Include router info if configured
		if rModel != "" || rStatus != "Stopped" {
			resp["router"] = map[string]interface{}{
				"status":        rStatus,
				"activePort":    rPort,
				"activePid":     rPid,
				"ggufModelPath": rModel,
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == "POST" {
		var body struct {
			Action string `json:"action"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var err error
		switch body.Action {
		case "start":
			err = inference.StartActive(context.Background())
		case "stop":
			err = inference.StopActive()
		case "erase_cache":
			err = inference.GlobalLocalModel.TriggerGC(context.Background())
			cacheDir := filepath.Join(".tzro", "cache")
			_ = os.RemoveAll(cacheDir)
			_ = os.MkdirAll(cacheDir, 0755)
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
			return
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"success"}`))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type ModelListEntry struct {
		inference.ModelEntry
		Downloaded bool `json:"downloaded"`
	}

	catalog := inference.GetCatalog()
	modelsDir := config.GetModelsDir()

	result := make([]ModelListEntry, 0, len(catalog))
	for _, entry := range catalog {
		downloaded := false
		modelPath := filepath.Join(modelsDir, entry.Filename)
		if _, err := os.Stat(modelPath); err == nil {
			downloaded = true
		}
		result = append(result, ModelListEntry{
			ModelEntry: entry,
			Downloaded: downloaded,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func handleModelDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	type DownloadRequest struct {
		ModelID   string `json:"modelId"`
		CustomURL string `json:"customUrl"`
	}

	var req DownloadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var downloadURL string
	var filename string

	if req.ModelID != "" {
		entry := inference.FindModelByID(req.ModelID)
		if entry == nil {
			http.Error(w, fmt.Sprintf("model not found: %s", req.ModelID), http.StatusBadRequest)
			return
		}
		downloadURL = entry.DownloadURL
		filename = entry.Filename
	} else if req.CustomURL != "" {
		downloadURL = req.CustomURL
		parsed, err := url.Parse(req.CustomURL)
		if err != nil {
			http.Error(w, "invalid custom URL", http.StatusBadRequest)
			return
		}
		filename = path.Base(parsed.Path)
	}

	if downloadURL == "" || filename == "" {
		http.Error(w, "must provide modelId or customUrl", http.StatusBadRequest)
		return
	}

	modelsDir := config.GetModelsDir()
	finalPath := filepath.Join(modelsDir, filename)

	// If file already exists, return success immediately
	if _, err := os.Stat(finalPath); err == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "already_downloaded"})
		return
	}

	inference.GlobalLocalModel.ManifestProgress = 0

	go func() {
		tmpPath := filepath.Join(modelsDir, filename+".downloading")

		httpReq, err := http.NewRequest("GET", downloadURL, nil)
		if err != nil {
			fmt.Printf("[Model Download] Failed to create request: %v\n", err)
			_ = os.Remove(tmpPath)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}
		httpReq.Header.Set("User-Agent", "tzro-engine/1.0")

		client := &http.Client{}
		resp, err := client.Do(httpReq)
		if err != nil {
			fmt.Printf("[Model Download] HTTP request failed: %v\n", err)
			_ = os.Remove(tmpPath)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("[Model Download] Server returned status %s\n", resp.Status)
			_ = os.Remove(tmpPath)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		totalSize := resp.ContentLength

		tmpFile, err := os.Create(tmpPath)
		if err != nil {
			fmt.Printf("[Model Download] Failed to create temp file: %v\n", err)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		buf := make([]byte, 32*1024)
		var downloaded int64

		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				_, writeErr := tmpFile.Write(buf[:n])
				if writeErr != nil {
					fmt.Printf("[Model Download] Write error: %v\n", writeErr)
					tmpFile.Close()
					_ = os.Remove(tmpPath)
					inference.GlobalLocalModel.ManifestProgress = 0
					return
				}
				downloaded += int64(n)
				if totalSize > 0 {
					pct := int(downloaded * 99 / totalSize)
					if pct > 99 {
						pct = 99
					}
					inference.GlobalLocalModel.ManifestProgress = pct
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					break
				}
				fmt.Printf("[Model Download] Read error: %v\n", readErr)
				tmpFile.Close()
				_ = os.Remove(tmpPath)
				inference.GlobalLocalModel.ManifestProgress = 0
				return
			}
		}

		tmpFile.Close()

		if err := os.Rename(tmpPath, finalPath); err != nil {
			fmt.Printf("[Model Download] Failed to rename temp file: %v\n", err)
			_ = os.Remove(tmpPath)
			inference.GlobalLocalModel.ManifestProgress = 0
			return
		}

		inference.GlobalLocalModel.ManifestProgress = 100

		// Update config with new model path
		cfg := config.Get()
		cfg.GGUFModelPath = finalPath
		_ = config.Save(&cfg)

		fmt.Printf("[Model Download] Successfully downloaded %s to %s\n", filename, finalPath)
	}()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "downloading"})
}

func handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Allow client connections from web frontends
	w.Header().Set("Access-Control-Allow-Origin", "*")

	sub := stream.GlobalBus.Subscribe(nil)
	defer sub.Unsubscribe()

	// Flush the headers to establish connection
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case chunk, ok := <-sub.Ch:
			if !ok {
				return
			}
			data, err := json.Marshal(chunk)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", string(data))
			flusher.Flush()
		}
	}
}

func handleNotifications(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	statusFilter := r.URL.Query().Get("status")
	list, err := notification.List(r.Context(), statusFilter)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func handleNotificationsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID     string `json:"id"`
		Status string `json:"status"` // "read" | "dismissed"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" || (req.Status != "read" && req.Status != "dismissed" && req.Status != "unread") {
		http.Error(w, "id and a valid status are required", http.StatusBadRequest)
		return
	}

	err := notification.MarkRead(r.Context(), req.ID, req.Status)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"success"}`))
}

// Workflows handlers implementation

type WorkflowWithTasks struct {
	memory.WorkflowDefinition
	Tasks []memory.WorkflowTask `json:"tasks"`
}

type SaveWorkflowRequest struct {
	ID            string                `json:"id"`
	Name          string                `json:"name"`
	Description   string                `json:"description"`
	TriggerType   string                `json:"triggerType"`
	TriggerConfig string                `json:"triggerConfig"`
	Status        string                `json:"status"`
	Tasks         []memory.WorkflowTask `json:"tasks"`
}

func handleWorkflows(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		defs, err := memory.DB.GetWorkflows()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		var result []WorkflowWithTasks
		for _, d := range defs {
			tasks, err := memory.DB.GetWorkflowTasks(d.ID)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			result = append(result, WorkflowWithTasks{
				WorkflowDefinition: d,
				Tasks:              tasks,
			})
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)

	case "POST":
		var req SaveWorkflowRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Name == "" || req.TriggerType == "" {
			http.Error(w, "name and triggerType are required", http.StatusBadRequest)
			return
		}

		wfID := req.ID
		isNew := false
		if wfID == "" {
			wfID = fmt.Sprintf("wf_%d", time.Now().UnixNano())
			isNew = true
		}

		var existing *memory.WorkflowDefinition
		if !isNew {
			defs, _ := memory.DB.GetWorkflows()
			for i := range defs {
				if defs[i].ID == wfID {
					existing = &defs[i]
					break
				}
			}
		}

		now := time.Now().Unix()
		createdAt := now
		if existing != nil {
			createdAt = existing.CreatedAt
		}

		status := req.Status
		if status == "" {
			status = "active"
		}

		// Calculate initial next run time if active and cron
		var nextRun int64
		if status == "active" && req.TriggerType == "cron" && req.TriggerConfig != "" {
			next := workflow.ParseCronNext(req.TriggerConfig, time.Now())
			if !next.IsZero() {
				nextRun = next.Unix()
			}
		}

		wf := memory.WorkflowDefinition{
			ID:            wfID,
			Name:          req.Name,
			Description:   req.Description,
			TriggerType:   req.TriggerType,
			TriggerConfig: req.TriggerConfig,
			Status:        status,
			NextRunAt:     nextRun,
			CreatedAt:     createdAt,
			UpdatedAt:     now,
		}

		// Assign workflowId to tasks
		for i := range req.Tasks {
			req.Tasks[i].WorkflowID = wfID
		}

		if err := memory.DB.SaveWorkflow(wf, req.Tasks); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "saved", "id": wfID})

	case "DELETE":
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}

		if err := memory.DB.DeleteWorkflow(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleWorkflowToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID     string `json:"id"`
		Status string `json:"status"` // "active" | "paused"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" || (req.Status != "active" && req.Status != "paused") {
		http.Error(w, "id and active/paused status are required", http.StatusBadRequest)
		return
	}

	// Calculate and store next run time if we are toggling to active
	if req.Status == "active" {
		defs, _ := memory.DB.GetWorkflows()
		for _, d := range defs {
			if d.ID == req.ID {
				if d.TriggerType == "cron" && d.TriggerConfig != "" {
					next := workflow.ParseCronNext(d.TriggerConfig, time.Now())
					if !next.IsZero() {
						_ = memory.DB.UpdateWorkflowNextRun(req.ID, next.Unix())
					}
				}
				break
			}
		}
	} else {
		// Clear next run time if paused
		_ = memory.DB.UpdateWorkflowNextRun(req.ID, 0)
	}

	if err := memory.DB.ToggleWorkflow(req.ID, req.Status); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func handleWorkflowTrigger(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}

	go func(id string) {
		if err := workflow.ExecuteWorkflow(context.Background(), id); err != nil {
			fmt.Printf("[Server Workflow Trigger Error] Workflow %s failed: %v\n", id, err)
		}
	}(req.ID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "triggered"})
}

func handleWorkflowExecutions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	wfID := r.URL.Query().Get("workflowId")
	list, err := memory.DB.GetWorkflowExecutions(wfID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

func handleWorkflowExecutionDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	execID := r.URL.Query().Get("executionId")
	if execID == "" {
		http.Error(w, "executionId is required", http.StatusBadRequest)
		return
	}

	exec, taskRuns, err := memory.DB.GetWorkflowExecutionDetails(execID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	type ExecutionDetailsResponse struct {
		Execution *memory.WorkflowExecution      `json:"execution"`
		Tasks     []memory.WorkflowTaskExecution `json:"tasks"`
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ExecutionDetailsResponse{
		Execution: exec,
		Tasks:     taskRuns,
	})
}

func handleOpenAPIMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		integrations, err := memory.DB.GetOpenAPIIntegrations()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		type SanitizedIntegration struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			OpenAPISpec string `json:"openapiSpec"`
			AuthType    string `json:"authType"`
			AuthKey     string `json:"authKey,omitempty"`
			CreatedAt   int64  `json:"createdAt"`
		}
		var list []SanitizedIntegration
		for _, oi := range integrations {
			list = append(list, SanitizedIntegration{
				ID:          oi.ID,
				Name:        oi.Name,
				OpenAPISpec: oi.OpenAPISpec,
				AuthType:    oi.AuthType,
				AuthKey:     oi.AuthKey,
				CreatedAt:   oi.CreatedAt,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)

	case "POST":
		var req memory.OpenAPIIntegration
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.ID == "" || req.Name == "" || req.OpenAPISpec == "" || req.AuthType == "" {
			http.Error(w, "id, name, openapiSpec, and authType are required fields", http.StatusBadRequest)
			return
		}

		req.CreatedAt = time.Now().Unix()

		var temp map[string]interface{}
		if err := json.Unmarshal([]byte(req.OpenAPISpec), &temp); err != nil {
			http.Error(w, fmt.Sprintf("invalid OpenAPI specification JSON: %v", err), http.StatusBadRequest)
			return
		}

		if err := memory.DB.SaveOpenAPIIntegration(req); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tools.UnregisterOpenAPITools(req.ID)
		if err := tools.RegisterOpenAPISpec(req); err != nil {
			http.Error(w, fmt.Sprintf("saved to db but failed to parse and register tools: %v", err), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "id": req.ID})

	case "DELETE":
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "id query parameter is required", http.StatusBadRequest)
			return
		}

		if err := memory.DB.DeleteOpenAPIIntegration(id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		tools.UnregisterOpenAPITools(id)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted"})

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func handleDashboardSpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	spec, err := memory.DB.GetLatestDashboardSpec()
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to query dashboard spec: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if spec == nil {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":"error","message":"No dashboard spec found. Please trigger regeneration."}`))
		return
	}

	type dashboardSpecAPIResponse struct {
		ID              string          `json:"id"`
		Spec            json.RawMessage `json:"spec"`
		GeneratedAt     int64           `json:"generatedAt"`
		GeneratorTaskID string          `json:"generatorTaskId"`
		TTLSeconds      int64           `json:"ttlSeconds"`
	}
	resp := dashboardSpecAPIResponse{
		ID:              spec.ID,
		Spec:            json.RawMessage(spec.Spec),
		GeneratedAt:     spec.GeneratedAt,
		GeneratorTaskID: spec.GeneratorTaskID,
		TTLSeconds:      spec.TTLSeconds,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func handleDashboardRegenerate(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	waitParam := r.URL.Query().Get("wait")
	shouldWait := waitParam == "true" || waitParam == "1"

	taskID := fmt.Sprintf("task_dashboard_gen_%d", time.Now().UnixNano())
	prompt := "Generate system dashboard spec"

	ctx := context.Background()

	if shouldWait {
		timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		graph, _, err := task.Execute(timeoutCtx, prompt, task.ExecuteOptions{
			TaskID:       taskID,
			IntentType:   "workflow",
			IsForeground: true,
		})
		if err != nil {
			if timeoutCtx.Err() == context.DeadlineExceeded {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"generating","taskId":"%s"}`, taskID)))
				return
			}
			http.Error(w, fmt.Sprintf("Generation failed: %v", err), http.StatusInternalServerError)
			return
		}

		nodeSucceeded := false
		// Find the terminal node (no outgoing edges) and check its status
		hasOutgoing := make(map[string]bool)
		for _, edge := range graph.Edges {
			hasOutgoing[edge.SourceID] = true
		}
		for _, node := range graph.Nodes {
			if !hasOutgoing[node.ID] {
				state, ok := memory.DB.GetNodeState(taskID, node.ID)
				if ok && state.Status == "completed" {
					nodeSucceeded = true
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if nodeSucceeded {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"completed","taskId":"%s"}`, taskID)))
		} else {
			_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"failed","taskId":"%s"}`, taskID)))
		}
		return
	} else {
		go func() {
			_, _, _ = task.Execute(context.Background(), prompt, task.ExecuteOptions{
				TaskID:       taskID,
				IntentType:   "workflow",
				IsForeground: false,
			})
		}()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"generating","taskId":"%s"}`, taskID)))
	}
}

// handleTaskSSE streams execution events for a specific task via Server-Sent Events.
// Accessible at: GET /api/tasks/events?taskId=X
func handleTaskSSE(w http.ResponseWriter, r *http.Request) {
	taskID := r.URL.Query().Get("taskId")
	if taskID == "" {
		http.Error(w, "taskId query parameter required", http.StatusBadRequest)
		return
	}

	ch, err := channel.NewSSESubagentChannel(w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer ch.Close()

	// Bridge blocks until bus closes or client disconnects
	channel.BridgeWithOptions(ch, taskID, channel.BridgeOptions{
		StopOnError: true, // Client disconnect → stop
	})
}

var (
	pkgManager      *packagemanager.Manager
	pkgManagerMutex sync.Mutex
)

func getPkgManager() (*packagemanager.Manager, error) {
	pkgManagerMutex.Lock()
	defer pkgManagerMutex.Unlock()

	if pkgManager != nil {
		return pkgManager, nil
	}
	db := memory.DB.RawDB()
	if db == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	appsDir := config.ResolvePath("apps")
	pkgManager = packagemanager.NewManager(db, mcp.GlobalRegistry, appsDir)
	if err := pkgManager.InitSchema(); err != nil {
		pkgManager = nil
		return nil, err
	}
	return pkgManager, nil
}

func handleTasksResume(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"taskId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.TaskID = r.URL.Query().Get("taskId")
	}

	if req.TaskID == "" {
		http.Error(w, "taskId is required", http.StatusBadRequest)
		return
	}

	go func() {
		defer recoverTaskPanic(req.TaskID)
		db := memory.DB.RawDB()
		if db == nil {
			fmt.Fprintf(os.Stderr, "[Server Resume Error] Database not initialized\n")
			return
		}
		var graphBytes string
		err := db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", "graph_"+req.TaskID).Scan(&graphBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Server Resume Error] Failed to load cached graph: %v\n", err)
			return
		}
		var graph compiler.ExecutionGraph
		if err := json.Unmarshal([]byte(graphBytes), &graph); err != nil {
			fmt.Fprintf(os.Stderr, "[Server Resume Error] Failed to unmarshal cached graph: %v\n", err)
			return
		}
		levels, err := compiler.CompileAndSort(&graph)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Server Resume Error] Failed to compile graph: %v\n", err)
			return
		}
		proactivity.RegisterActiveUserTask(graph.TaskID)
		defer proactivity.DeregisterActiveUserTask(graph.TaskID)
		_ = executor.GlobalEngine.ExecuteGraph(context.Background(), &graph, levels)
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"success","message":"Task resume triggered"}`))
}

func handleTasksApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"taskId"`
		NodeID string `json:"nodeId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.TaskID = r.URL.Query().Get("taskId")
		req.NodeID = r.URL.Query().Get("nodeId")
	}

	if req.TaskID == "" || req.NodeID == "" {
		http.Error(w, "taskId and nodeId are required", http.StatusBadRequest)
		return
	}

	notifs, err := memory.DB.GetNotifications("unread")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var target *memory.DurableNotification
	for _, n := range notifs {
		if n.TaskID == req.TaskID && n.TargetID == req.NodeID && n.Source == "human_approval" && n.Type == "approval_request" {
			target = &n
			break
		}
	}

	if target == nil {
		http.Error(w, "unread approval request not found", http.StatusNotFound)
		return
	}

	if err := memory.DB.UpdateNotificationStatus(target.ID, "approved"); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	go func() {
		defer recoverTaskPanic(req.TaskID)
		db := memory.DB.RawDB()
		if db == nil {
			return
		}
		var graphBytes string
		_ = db.QueryRow("SELECT raw_payload FROM disk_cache WHERE cache_id = ?", "graph_"+req.TaskID).Scan(&graphBytes)
		var graph compiler.ExecutionGraph
		if json.Unmarshal([]byte(graphBytes), &graph) == nil {
			levels, _ := compiler.CompileAndSort(&graph)
			proactivity.RegisterActiveUserTask(graph.TaskID)
			defer proactivity.DeregisterActiveUserTask(graph.TaskID)
			_ = executor.GlobalEngine.ExecuteGraph(context.Background(), &graph, levels)
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"success","message":"Task approved and resume triggered"}`))
}

func handleAppsInstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		ArchivePath string `json:"archivePath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.ArchivePath = r.URL.Query().Get("archivePath")
	}

	if req.ArchivePath == "" {
		http.Error(w, "archivePath is required", http.StatusBadRequest)
		return
	}

	mgr, err := getPkgManager()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	app, err := mgr.Install(req.ArchivePath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(app)
}

func handleAppsUninstall(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		AppID string `json:"appId"`
		Purge bool   `json:"purge"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.AppID = r.URL.Query().Get("appId")
		req.Purge = r.URL.Query().Get("purge") == "true"
	}

	if req.AppID == "" {
		http.Error(w, "appId is required", http.StatusBadRequest)
		return
	}

	mgr, err := getPkgManager()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if req.Purge {
		err = mgr.Purge(req.AppID)
	} else {
		err = mgr.Uninstall(req.AppID)
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"success"}`))
}

func handleAppsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	mgr, err := getPkgManager()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	apps, err := mgr.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(apps)
}

// handleTasksCancel handles POST /api/tasks/cancel — cancels a running task.
// If the task has an active goroutine, its context is cancelled. All pending/running
// nodes are marked as 'cancelled' in SQLite. Falls back to SQLite-only update for
// zombie tasks that are no longer in memory.
func handleTasksCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"taskId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid request body"}`, http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		http.Error(w, `{"error":"taskId is required"}`, http.StatusBadRequest)
		return
	}

	// 1. Cancel the active context (if task goroutine is still running)
	graphsMutex.Lock()
	cancelFn, hasCancel := activeCancels[req.TaskID]
	graph := activeGraphs[req.TaskID]
	if hasCancel {
		cancelFn()
		delete(activeCancels, req.TaskID)
	}
	if graph != nil {
		delete(activeGraphs, req.TaskID)
	}
	graphsMutex.Unlock()

	// 2. Mark all pending/running nodes as cancelled in SQLite
	cancelledNodes := 0
	nodeStates := memory.DB.GetAllNodeStates(req.TaskID)
	for _, state := range nodeStates {
		if state.Status == "pending" || state.Status == "running" {
			_ = memory.DB.SetNodeState(req.TaskID, state.NodeID, "cancelled", "Task cancelled by user")
			cancelledNodes++
		}
	}

	// 3. Update task-level status
	_ = memory.DB.UpdateTaskStatus(req.TaskID, "cancelled", "cancelled by user")

	// 4. Emit telemetry event
	stream.GlobalBus.Publish(stream.StreamChunk{
		TaskID:  req.TaskID,
		Source:  "system",
		Type:    "task_cancelled",
		Content: fmt.Sprintf("Task cancelled. %d nodes marked as cancelled.", cancelledNodes),
	})

	fmt.Fprintf(os.Stderr, "[Server] Task %s cancelled (%d nodes cancelled, had active context: %v)\n",
		req.TaskID, cancelledNodes, hasCancel)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"cancelled","taskId":"%s","cancelledNodes":%d,"hadActiveContext":%v}`,
		req.TaskID, cancelledNodes, hasCancel)))
}

func handleTasksRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Prompt        string `json:"prompt"`
		TaskID        string `json:"taskId"`
		SelfContained bool   `json:"selfContained"`  // ADR-0054
		IsForeground  *bool  `json:"isForeground"`   // ADR-0063: defaults to true for CLI-initiated tasks
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if req.Prompt == "" {
		http.Error(w, "prompt is required", http.StatusBadRequest)
		return
	}
	if req.TaskID == "" {
		req.TaskID = fmt.Sprintf("task_%d", time.Now().Unix())
	}

	// ADR-0063: CLI/REST-submitted tasks are foreground by default (user-initiated).
	// Only mark as background if explicitly requested via isForeground: false.
	isForeground := true
	if req.IsForeground != nil {
		isForeground = *req.IsForeground
	}

	execOpts := task.ExecuteOptions{
		TaskID:        req.TaskID,
		IntentType:    "workflow",
		IsForeground:  isForeground,
		SelfContained: req.SelfContained,
	}

	taskCtx, taskCancel := context.WithCancel(context.Background())
	graphsMutex.Lock()
	activeCancels[req.TaskID] = taskCancel
	graphsMutex.Unlock()

	go func() {
		defer recoverTaskPanic(req.TaskID)
		defer func() {
			graphsMutex.Lock()
			delete(activeCancels, req.TaskID)
			graphsMutex.Unlock()
		}()
		_, _, err := task.Execute(taskCtx, req.Prompt, execOpts)
		if err != nil {
			// ADR-0054: Emit task_failed so SSE listeners can detect planning failures
			stream.GlobalBus.Publish(stream.StreamChunk{
				TaskID:  req.TaskID,
				Source:  "system",
				Type:    "task_failed",
				Content: err.Error(),
			})
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"running","taskId":"%s"}`, req.TaskID)))
}

// handleRestart handles POST /api/restart — triggers an immediate in-place
// daemon re-exec via syscall.Exec (ADR-0033). Responds before the re-exec
// so the caller receives a clean 200. The inference sidecar survives via
// process adoption on the new boot cycle.
func handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var body struct {
		Reason string `json:"reason"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if body.Reason == "" {
		body.Reason = "user-requested"
	}

	uptime := time.Since(DaemonStartTime).Truncate(time.Second).String()

	// Emit audit event to StreamBus so Observer/Sentinel/dashboard see it
	stream.GlobalBus.Publish(stream.StreamChunk{
		Source:  "system",
		Type:    "daemon_restart",
		Content: fmt.Sprintf("Daemon restart initiated (reason: %s, uptime: %s)", body.Reason, uptime),
	})

	// Respond before the re-exec so the caller gets a clean 200
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(fmt.Sprintf(`{"status":"restarting","reason":%q,"uptime":%q}`, body.Reason, uptime)))

	// Flush the response to the wire before scheduling the re-exec
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Schedule re-exec after a short delay to ensure the HTTP response is fully sent
	time.AfterFunc(100*time.Millisecond, execSelfRestart)
}

// execSelfRestart replaces the current process with a fresh copy of the same
// binary using syscall.Exec. The PID is preserved, so the pidlock in
// daemon.lock stays valid and the inference sidecar is adopted on reboot.
func execSelfRestart() {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Restart] Failed to resolve executable path: %v\n", err)
		return
	}
	// Resolve symlinks so we exec the real binary (handles install-in-place upgrades)
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[Restart] Failed to resolve symlinks: %v\n", err)
		return
	}

	fmt.Fprintf(os.Stderr, "[Restart] Re-executing %s ...\n", executable)
	if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "[Restart] syscall.Exec failed: %v\n", err)
	}
}
