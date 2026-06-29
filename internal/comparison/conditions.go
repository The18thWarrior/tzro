package comparison

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"tzro/internal/codegen"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/task"
	"tzro/internal/telemetry"
	"tzro/internal/tools"
)

// modelModeForCondition returns the config modelMode string for a DAG condition.
func modelModeForCondition(conditionID string) (string, error) {
	switch conditionID {
	case ConditionCloudDAGRaw:
		return "cloud", nil
	case ConditionCloudDAG:
		return "cloud", nil
	case ConditionLocalOnly:
		return "local", nil
	case ConditionCooperative:
		return "cooperative", nil
	default:
		return "", fmt.Errorf("unsupported DAG condition: %s", conditionID)
	}
}

// RunDAGCondition executes a task under one of the DAG-based conditions
// (cloud_dag_raw, cloud_dag, local_only, cooperative).
// It creates an isolated database, sets the appropriate model mode, runs the task through the
// standard task.Execute pipeline, and extracts the terminal synthesis output.
func RunDAGCondition(ctx context.Context, conditionID string, t ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	modelMode, err := modelModeForCondition(conditionID)
	if err != nil {
		return ComparisonResult{}, err
	}

	// Save and restore model mode via the global pointer
	originalModelMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.ModelMode = modelMode
	defer func() {
		config.GlobalConfig.ModelMode = originalModelMode
	}()

	// Isolated database per condition run
	dbFile := fmt.Sprintf("tzro_comparison_%s_%s.db", conditionID, t.ID)
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbFile)
	defer func() {
		memory.DB.Close()
		_ = os.Remove(dbFile)
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()

	if err := memory.DB.Init(); err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to init isolated database for %s: %w", conditionID, err)
	}

	// Initialize tool registry, then remove dashboard-specific tools that
	// would confuse the planner. The comparison tasks produce documentation,
	// not dashboard specs — but the planner sees "terminal_synthesis" in the
	// registry and plans a deterministic node calling it, colliding with the
	// compiler-injected synthesis node.
	_ = tools.Init("")
	tools.Unregister("terminal_synthesis")
	tools.Unregister("compose_layout")
	tools.Unregister("gather_metrics")
	tools.Unregister("gather_tasks")
	tools.Unregister("gather_config")
	tools.Unregister("gather_workflows")

	// Initialize inference backend for Probe Node execution.
	// Without this, probe nodes fail with "no active inference backend".
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = inference.NewLlamaServerBackend(inference.GlobalLocalModel, telemetry.Default)
	defer func() {
		inference.ActiveBackend = oldBackend
	}()

	// Auto-start sidecar if not already running, then wait for health
	status, activePort, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.GlobalLocalModel.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for %s: %v\n", conditionID, err)
		} else {
			// Wait for the sidecar to become healthy (64K context can take 10-20s to load)
			_, activePort, _, _, _ = inference.GlobalLocalModel.GetStatusInfo()
			fmt.Fprintf(os.Stderr, "[Comparison] Waiting for sidecar health on port %d...\n", activePort)
			for attempt := range 30 {
				healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
				resp, err := http.Get(healthURL)
				if err == nil && resp.StatusCode == http.StatusOK {
					resp.Body.Close()
					fmt.Fprintf(os.Stderr, "[Comparison] Sidecar healthy after %d attempts\n", attempt+1)
					break
				}
				if resp != nil {
					resp.Body.Close()
				}
				time.Sleep(1 * time.Second)
			}
		}
	}

	// Fresh token tracker
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	// Compaction bypass: cloud_dag_raw disables the 5-Layer Compaction Pipeline
	// so we can isolate DAG structural savings from pipeline savings.
	if conditionID == ConditionCloudDAGRaw {
		ctx = context.WithValue(ctx, "compaction_disabled", true)
	}

	taskID := fmt.Sprintf("comparison_%s_%s", conditionID, t.ID)

	startTime := time.Now()

	graph, _, err := task.Execute(ctx, t.Prompt, task.ExecuteOptions{
		TaskID:     taskID,
		IntentType: "workflow",
	})
	if err != nil {
		localUsage, cloudUsage := tracker.GetUsage()
		return ComparisonResult{
			TaskID:      t.ID,
			TaskTier:    t.Tier,
			Condition:   conditionID,
			CloudTokens: cloudUsage,
			LocalTokens: localUsage,
			WallClockMs: time.Since(startTime).Milliseconds(),
			EstCostUSD:  EstimateCost(cloudUsage, localUsage, pricing),
			Error:       fmt.Sprintf("DAG execution failed: %v", err),
		}, nil
	}

	// Extract terminal synthesis output
	outputText := extractTerminalSynthesis(graph, taskID)

	// Count tool calls from the graph
	toolCallCount := countToolCalls(graph, taskID)

	localUsage, cloudUsage := tracker.GetUsage()
	return ComparisonResult{
		TaskID:        t.ID,
		TaskTier:      t.Tier,
		Condition:     conditionID,
		CloudTokens:   cloudUsage,
		LocalTokens:   localUsage,
		WallClockMs:   time.Since(startTime).Milliseconds(),
		EstCostUSD:    EstimateCost(cloudUsage, localUsage, pricing),
		ToolCallCount: toolCallCount,
		OutputText:    outputText,
	}, nil
}

// extractTerminalSynthesis reads the terminal synthesis node output from the executed graph.
func extractTerminalSynthesis(graph *compiler.ExecutionGraph, taskID string) string {
	if graph == nil {
		return ""
	}

	for _, node := range graph.Nodes {
		if node.Type == "synthesis" || node.ID == "terminal_synthesis" {
			if state, ok := memory.DB.GetNodeState(taskID, node.ID); ok {
				if state.RawOutput != "" {
					return state.RawOutput
				}
				return state.Output
			}
		}
	}
	return ""
}

// countToolCalls counts action/deterministic nodes in the execution graph
// plus actual tool calls made inside probe nodes (stored in the thought_chain table).
func countToolCalls(graph *compiler.ExecutionGraph, taskID string) int {
	if graph == nil {
		return 0
	}
	count := 0
	for _, node := range graph.Nodes {
		if node.Type == "action" || node.Type == "deterministic" {
			count++
		}
	}

	// Add tool calls from probe nodes (persisted in thought_chain)
	probeCount, err := memory.DB.CountToolCallsByTaskID(taskID)
	if err == nil {
		count += probeCount
	}

	return count
}

// RunCodegenCondition executes a code generation task using the static 3-node DAG
// from the codegen package (check_context → reason_code → write_code).
// This benchmarks the tzro_code tool's architecture directly, bypassing the planner.
func RunCodegenCondition(ctx context.Context, t ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	// Codegen always runs in cooperative mode (local model generates code)
	originalModelMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.ModelMode = "cooperative"
	defer func() {
		config.GlobalConfig.ModelMode = originalModelMode
	}()

	// Isolated database
	dbFile := fmt.Sprintf("tzro_comparison_%s_%s.db", ConditionTzroCode, t.ID)
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbFile)
	defer func() {
		memory.DB.Close()
		_ = os.Remove(dbFile)
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()

	if err := memory.DB.Init(); err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to init isolated database for tzro_code: %w", err)
	}

	// Initialize tools (needed for write_file in the static DAG)
	_ = tools.Init("")

	// Initialize inference backend
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = inference.NewLlamaServerBackend(inference.GlobalLocalModel, telemetry.Default)
	defer func() {
		inference.ActiveBackend = oldBackend
	}()

	// Auto-start sidecar if needed
	status, activePort, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.GlobalLocalModel.Start(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for tzro_code: %v\n", err)
		} else {
			_, activePort, _, _, _ = inference.GlobalLocalModel.GetStatusInfo()
			for attempt := range 30 {
				healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
				resp, err := http.Get(healthURL)
				if err == nil && resp.StatusCode == http.StatusOK {
					resp.Body.Close()
					fmt.Fprintf(os.Stderr, "[Comparison] Sidecar healthy after %d attempts\n", attempt+1)
					break
				}
				if resp != nil {
					resp.Body.Close()
				}
				time.Sleep(1 * time.Second)
			}
		}
	}

	// Create a temp directory for the codegen output
	tmpDir, err := os.MkdirTemp("", "tzro_codegen_*")
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// If the task has a seed file, copy it to the temp dir
	targetPath := filepath.Join(tmpDir, t.Filepath)
	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to create target parent dir: %w", err)
	}

	if t.SeedFile != "" {
		seedData, err := ReadSeedFile(t.SeedFile)
		if err != nil {
			return ComparisonResult{}, fmt.Errorf("failed to read seed file: %w", err)
		}
		if err := os.WriteFile(targetPath, seedData, 0644); err != nil {
			return ComparisonResult{}, fmt.Errorf("failed to write seed file: %w", err)
		}
	}

	// Determine spec and language
	spec := t.Spec
	if spec == "" {
		spec = t.Prompt
	}
	language := t.Language
	if language == "" {
		language = codegen.DetectLanguage(targetPath)
	}

	// Fresh token tracker
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	taskID := fmt.Sprintf("comparison_%s_%s", ConditionTzroCode, t.ID)
	startTime := time.Now()

	// Build the static 3-node DAG
	graph := codegen.BuildCodeDAG(taskID, spec, targetPath, language, 500)

	// Execute the DAG using the global engine
	err = executor.GlobalEngine.ExecuteGraphReactive(ctx, graph)

	wallClock := time.Since(startTime).Milliseconds()
	localUsage, cloudUsage := tracker.GetUsage()

	if err != nil {
		return ComparisonResult{
			TaskID:      t.ID,
			TaskTier:    t.Tier,
			Condition:   ConditionTzroCode,
			CloudTokens: cloudUsage,
			LocalTokens: localUsage,
			WallClockMs: wallClock,
			EstCostUSD:  EstimateCost(cloudUsage, localUsage, pricing),
			Error:       fmt.Sprintf("tzro_code execution failed: %v", err),
		}, nil
	}

	// Read the generated file as the output
	var outputText string
	if data, readErr := os.ReadFile(targetPath); readErr == nil {
		outputText = string(data)
	} else {
		// Try reading from the write_code node state
		outputText = extractTerminalSynthesis(graph, taskID)
	}

	toolCallCount := countToolCalls(graph, taskID)

	return ComparisonResult{
		TaskID:        t.ID,
		TaskTier:      t.Tier,
		Condition:     ConditionTzroCode,
		CloudTokens:   cloudUsage,
		LocalTokens:   localUsage,
		WallClockMs:   wallClock,
		EstCostUSD:    EstimateCost(cloudUsage, localUsage, pricing),
		ToolCallCount: toolCallCount,
		OutputText:    outputText,
	}, nil
}
