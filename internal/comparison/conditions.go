package comparison

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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

	// For codegen tasks, scope file writes to a temporary directory so
	// benchmark runs never modify the actual source tree. Read tools still
	// have access to the source directory for Probe exploration.
	var tmpDir string
	var codegenTargetPath string
	if t.Category == CategoryCodegen && t.Filepath != "" {
		var tmpErr error
		tmpDir, tmpErr = os.MkdirTemp("", "tzro_benchmark_*")
		if tmpErr != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create temp dir for codegen benchmark: %w", tmpErr)
		}
		if evalDir, evalErr := filepath.EvalSymlinks(tmpDir); evalErr == nil {
			tmpDir = evalDir
		}
		defer os.RemoveAll(tmpDir)

		// Set up target path inside tmpDir
		codegenTargetPath = filepath.Join(tmpDir, t.Filepath)
		if mkdirErr := os.MkdirAll(filepath.Dir(codegenTargetPath), 0755); mkdirErr != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create target parent dir: %w", mkdirErr)
		}

		// Copy seed file if present
		if t.SeedFile != "" {
			seedData, seedErr := ReadSeedFile(t.SeedFile)
			if seedErr != nil {
				return ComparisonResult{}, fmt.Errorf("failed to read seed file: %w", seedErr)
			}
			if writeErr := os.WriteFile(codegenTargetPath, seedData, 0644); writeErr != nil {
				return ComparisonResult{}, fmt.Errorf("failed to write seed file: %w", writeErr)
			}
		}

		// Re-register write_file with a validator scoped to ONLY the tmpDir.
		// This ensures any write_file calls from the DAG planner/executor
		// go to the temp directory, not the actual source tree.
		writeValidator := tools.NewStaticPathValidator([]string{tmpDir})
		tools.Register(tools.NewWriteFileTool(writeValidator))
		fmt.Fprintf(os.Stderr, "[Comparison] Codegen task %s: write_file scoped to %s\n", t.ID, tmpDir)

		// Re-register read tools with a validator that allows both codebase and tmpDir
		readPaths := append(tools.GetAllowedPaths(), tmpDir)
		readValidator := tools.NewStaticPathValidator(readPaths)
		tools.Register(tools.NewReadFileTool(readValidator))
		tools.Register(tools.NewListDirTool(readValidator))
		tools.Register(tools.NewSearchFilesTool(readValidator))
		tools.Register(tools.NewPeekFileTool(readValidator))
	}

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

	// For codegen tasks, augment the prompt with the target path inside tmpDir
	// so the planner directs write_file to the correct location.
	taskPrompt := t.Prompt
	if codegenTargetPath != "" {
		taskPrompt = fmt.Sprintf("%s\n\nWrite the output file to: %s", taskPrompt, codegenTargetPath)
	}

	startTime := time.Now()

	graph, _, err := task.Execute(ctx, taskPrompt, task.ExecuteOptions{
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

	// Extract output: for codegen tasks, prefer reading the written file from tmpDir;
	// for docgen tasks, use the terminal synthesis node output.
	var outputText string
	if codegenTargetPath != "" {
		if data, readErr := os.ReadFile(codegenTargetPath); readErr == nil && len(data) > 0 {
			outputText = string(data)
		} else {
			// Fallback to terminal synthesis if no file was written
			outputText = extractTerminalSynthesis(graph, taskID)
		}
	} else {
		outputText = extractTerminalSynthesis(graph, taskID)
	}

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

// RunCodegenCondition executes a code generation task using the static DAG
// from the codegen package under either "cloud" or "cooperative" mode.
// Context is pre-computed via GatherContext (pure Go) and file writing is
// handled post-DAG via WriteCodeFile (pure Go).
func RunCodegenCondition(ctx context.Context, conditionID, modelMode string, t ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	// Set model mode for the codegen run
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

	// Initialize tools (needed for tool schema lookup during DAG execution)
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
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for %s: %v\n", conditionID, err)
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
	if evalDir, evalErr := filepath.EvalSymlinks(tmpDir); evalErr == nil {
		tmpDir = evalDir
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

	// Pre-compute context with a PathValidator that includes tmpDir.
	// Without this, the PathValidator rejects tmpDir paths (outside TZRO_DIR),
	// and the local model may extract relative paths that resolve against CWD.
	extendedPaths := append(tools.GetAllowedPaths(), tmpDir)
	validator := tools.NewStaticPathValidator(extendedPaths)
	codeCtx, ctxErr := codegen.GatherContext(targetPath, validator)
	if ctxErr != nil {
		fmt.Fprintf(os.Stderr, "[Comparison] GatherContext warning: %v\n", ctxErr)
		codeCtx = &codegen.CodeContext{
			Language: language,
			Siblings: make(map[string]string),
		}
	}

	// Fresh token tracker
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	taskID := fmt.Sprintf("comparison_%s_%s", conditionID, t.ID)
	startTime := time.Now()

	// Build the DAG with pre-computed context (single reason_code node)
	graph := codegen.BuildCodeDAG(taskID, spec, targetPath, language, 500, codeCtx)

	// Execute the DAG using the global engine
	err = executor.GlobalEngine.ExecuteGraphReactive(ctx, graph)

	wallClock := time.Since(startTime).Milliseconds()
	localUsage, cloudUsage := tracker.GetUsage()

	if err != nil {
		return ComparisonResult{
			TaskID:      t.ID,
			TaskTier:    t.Tier,
			Condition:   conditionID,
			CloudTokens: cloudUsage,
			LocalTokens: localUsage,
			WallClockMs: wallClock,
			EstCostUSD:  EstimateCost(cloudUsage, localUsage, pricing),
			Error:       fmt.Sprintf("%s execution failed: %v", conditionID, err),
		}, nil
	}

	// Post-process: extract reason_code output and write file (pure Go)
	var outputText string
	if state, ok := memory.DB.GetNodeState(taskID, "reason_code"); ok && state.Status == "completed" {
		rawCode := state.RawOutput
		if rawCode == "" {
			rawCode = state.Output
		}
		if rawCode != "" {
			_, _, writeErr := codegen.WriteCodeFile(targetPath, rawCode, 500)
			if writeErr != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] WriteCodeFile failed: %v\n", writeErr)
			}
		}
	}

	// Read the generated file as the output
	if data, readErr := os.ReadFile(targetPath); readErr == nil {
		outputText = string(data)
	} else {
		// Fallback to terminal synthesis
		outputText = extractTerminalSynthesis(graph, taskID)
	}

	toolCallCount := countToolCalls(graph, taskID)

	return ComparisonResult{
		TaskID:        t.ID,
		TaskTier:      t.Tier,
		Condition:     conditionID,
		CloudTokens:   cloudUsage,
		LocalTokens:   localUsage,
		WallClockMs:   wallClock,
		EstCostUSD:    EstimateCost(cloudUsage, localUsage, pricing),
		ToolCallCount: toolCallCount,
		OutputText:    outputText,
	}, nil
}

// RunCodegenExpandedCondition executes a codegen task using the pseudo-code
// expansion path. The task's Pseudocode field is populated from hand-authored
// fixture files in testdata/codegen_seeds/pseudocode/; the local model expands
// the pseudo-code into compilable source code.
func RunCodegenExpandedCondition(ctx context.Context, t ComparisonTask, pricing PricingTable) (ComparisonResult, error) {
	// Auto-load pseudo-code from fixture file if not populated on the task itself
	pseudocode := t.Pseudocode
	if strings.TrimSpace(pseudocode) == "" {
		pseudocode = ReadPseudocodeFixture(t.ID)
	}
	if strings.TrimSpace(pseudocode) == "" {
		return ComparisonResult{
			TaskID:    t.ID,
			TaskTier:  t.Tier,
			Condition: ConditionTzroCodeExpanded,
			Error:     "no pseudocode fixture found for task " + t.ID + " — skipping tzro_code_expanded condition",
		}, nil
	}

	// Set model mode to cooperative (local sidecar)
	originalModelMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.ModelMode = "cooperative"
	defer func() {
		config.GlobalConfig.ModelMode = originalModelMode
	}()

	// Isolated database per condition run
	dbFile := fmt.Sprintf("tzro_comparison_%s_%s.db", ConditionTzroCodeExpanded, t.ID)
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting(dbFile)
	defer func() {
		memory.DB.Close()
		_ = os.Remove(dbFile)
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()

	if err := memory.DB.Init(); err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to init isolated database for %s: %w", ConditionTzroCodeExpanded, err)
	}

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
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for %s: %v\n", ConditionTzroCodeExpanded, err)
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
	tmpDir, err := os.MkdirTemp("", "tzro_codegen_expanded_*")
	if err != nil {
		return ComparisonResult{}, fmt.Errorf("failed to create temp dir: %w", err)
	}
	if evalDir, evalErr := filepath.EvalSymlinks(tmpDir); evalErr == nil {
		tmpDir = evalDir
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

	spec := t.Spec
	if spec == "" {
		spec = t.Prompt
	}
	language := t.Language
	if language == "" {
		language = codegen.DetectLanguage(targetPath)
	}

	// Pre-compute context
	extendedPaths := append(tools.GetAllowedPaths(), tmpDir)
	validator := tools.NewStaticPathValidator(extendedPaths)
	codeCtx, ctxErr := codegen.GatherContext(targetPath, validator)
	if ctxErr != nil {
		fmt.Fprintf(os.Stderr, "[Comparison] GatherContext warning: %v\n", ctxErr)
		codeCtx = &codegen.CodeContext{
			Language: language,
			Siblings: make(map[string]string),
		}
	}

	// Fresh token tracker
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	taskID := fmt.Sprintf("comparison_%s_%s", ConditionTzroCodeExpanded, t.ID)
	startTime := time.Now()

	// Build the expansion DAG with pre-authored pseudo-code
	graph := codegen.BuildPseudocodeExpansionDAG(taskID, pseudocode, spec, targetPath, language, 500, codeCtx)

	// Execute the DAG
	err = executor.GlobalEngine.ExecuteGraphReactive(ctx, graph)

	wallClock := time.Since(startTime).Milliseconds()
	localUsage, cloudUsage := tracker.GetUsage()

	if err != nil {
		return ComparisonResult{
			TaskID:      t.ID,
			TaskTier:    t.Tier,
			Condition:   ConditionTzroCodeExpanded,
			CloudTokens: cloudUsage,
			LocalTokens: localUsage,
			WallClockMs: wallClock,
			EstCostUSD:  EstimateCost(cloudUsage, localUsage, pricing),
			Error:       fmt.Sprintf("%s execution failed: %v", ConditionTzroCodeExpanded, err),
		}, nil
	}

	// Post-process: extract reason_code output and write file
	if state, ok := memory.DB.GetNodeState(taskID, "reason_code"); ok && state.Status == "completed" {
		rawCode := state.RawOutput
		if rawCode == "" {
			rawCode = state.Output
		}
		if rawCode != "" {
			_, _, writeErr := codegen.WriteCodeFile(targetPath, rawCode, 500)
			if writeErr != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] WriteCodeFile failed: %v\n", writeErr)
			}
		}
	}

	// Read the generated file as the output
	var outputText string
	if data, readErr := os.ReadFile(targetPath); readErr == nil {
		outputText = string(data)
	} else {
		outputText = extractTerminalSynthesis(graph, taskID)
	}

	toolCallCount := countToolCalls(graph, taskID)

	return ComparisonResult{
		TaskID:        t.ID,
		TaskTier:      t.Tier,
		Condition:     ConditionTzroCodeExpanded,
		CloudTokens:   cloudUsage,
		LocalTokens:   localUsage,
		WallClockMs:   wallClock,
		EstCostUSD:    EstimateCost(cloudUsage, localUsage, pricing),
		ToolCallCount: toolCallCount,
		OutputText:    outputText,
	}, nil
}
