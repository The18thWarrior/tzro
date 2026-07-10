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
func RunDAGCondition(ctx context.Context, conditionID string, t ComparisonTask, pricing PricingTable, outputDir string) (ComparisonResult, error) {
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



	// Isolated database per condition run. Append timestamp to avoid SQLite
	// locking issues (disk I/O error 522) when runs happen in rapid succession.
	dbFile := fmt.Sprintf("tzro_comparison_%s_%s_%d.db", conditionID, t.ID, time.Now().UnixNano())
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

	// For both codegen and docgen tasks, scope file writes to a temporary directory
	// (inside OutputDir if provided) so benchmark runs never modify the actual
	// source tree. Read tools still have access to the source directory for Probe exploration.
	var testOutputDir string
	var cleanup func()
	if outputDir != "" {
		absOut, _ := filepath.Abs(outputDir)
		outputDir = absOut
		testOutputDir = filepath.Join(outputDir, "test_outputs", conditionID, t.ID)
		if err := os.MkdirAll(testOutputDir, 0755); err != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create test output dir: %w", err)
		}
		if evalDir, evalErr := filepath.EvalSymlinks(testOutputDir); evalErr == nil {
			testOutputDir = evalDir
		}
		cleanup = func() {} // Keep for inspection
	} else {
		var tmpErr error
		testOutputDir, tmpErr = os.MkdirTemp("", "tzro_benchmark_*")
		if tmpErr != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create temp dir for benchmark: %w", tmpErr)
		}
		if evalDir, evalErr := filepath.EvalSymlinks(testOutputDir); evalErr == nil {
			testOutputDir = evalDir
		}
		cleanup = func() { os.RemoveAll(testOutputDir) }
	}
	defer cleanup()

	var codegenTargetPath string
	if t.Category == CategoryCodegen && t.Filepath != "" {
		// Set up target path inside testOutputDir
		codegenTargetPath = filepath.Join(testOutputDir, t.Filepath)
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
	} else if t.Category == CategoryDocgen {
		// For docgen tasks, copy the target files to testOutputDir to ensure
		// a consistent environment where reading and writing happen in the same structure.
		projectRoot := tools.GetAllowedPaths()[0]
		for _, p := range t.TargetPaths {
			src := filepath.Join(projectRoot, p)
			dst := filepath.Join(testOutputDir, p)

			// Check if source exists
			info, err := os.Stat(src)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Warning: docgen target path %s does not exist: %v\n", src, err)
				continue
			}

			// Skip previous benchmark results to avoid recursive nesting
			if info.IsDir() && strings.HasPrefix(info.Name(), "benchmark_results_") {
				continue
			}

			// Create parent directory in destination
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return ComparisonResult{}, fmt.Errorf("failed to create docgen target parent: %w", err)
			}

			// Copy file or directory
			if info.IsDir() {
				if err := copyDir(src, dst); err != nil {
					fmt.Fprintf(os.Stderr, "[Comparison] Warning: failed to copy dir %s to %s: %v\n", src, dst, err)
				}
			} else {
				if err := copyFile(src, dst); err != nil {
					fmt.Fprintf(os.Stderr, "[Comparison] Warning: failed to copy file %s to %s: %v\n", src, dst, err)
				}
			}
		}
	}

	// Re-register write_file with a validator scoped to ONLY the testOutputDir.
	// This ensures any write_file calls from the DAG planner/executor
	// go to the test directory, not the actual source tree.
	writeValidator := tools.NewStaticPathValidator([]string{testOutputDir})
	tools.Register(tools.NewWriteFileTool(writeValidator))

	// Re-register read tools with a validator that allows both codebase and testOutputDir
	readPaths := append(tools.GetAllowedPaths(), testOutputDir)
	readValidator := tools.NewStaticPathValidator(readPaths)
	tools.Register(tools.NewReadFileTool(readValidator))
	tools.Register(tools.NewListDirTool(readValidator))
	tools.Register(tools.NewSearchFilesTool(readValidator))
	tools.Register(tools.NewPeekFileTool(readValidator))

	if t.Category == CategoryCodegen {
		fmt.Fprintf(os.Stderr, "[Comparison] Codegen task %s: write_file scoped to %s\n", t.ID, testOutputDir)
	} else if t.Category == CategoryDocgen {
		fmt.Fprintf(os.Stderr, "[Comparison] Docgen task %s: write_file scoped to %s\n", t.ID, testOutputDir)
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

	// For codegen tasks, augment the prompt with the target path inside testOutputDir
	// so the planner directs write_file to the correct location.
	taskPrompt := t.Prompt
	projectRoot := tools.GetAllowedPaths()[0]
	if codegenTargetPath != "" {
		relCodegenPath, _ := filepath.Rel(projectRoot, codegenTargetPath)
		taskPrompt = fmt.Sprintf("%s\n\nWrite the output file to: %s", taskPrompt, relCodegenPath)
	} else if t.Category == CategoryDocgen {
		// For docgen tasks, the agent should READ source code from the project root
		// (where the actual codebase lives) but WRITE output files to the isolated
		// sandbox directory. Previously the prompt told the agent to read AND write
		// from the sandbox, which was empty for most files — causing all docgen
		// benchmarks to produce empty or error output.
		relOutputDir, err := filepath.Rel(projectRoot, testOutputDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison Warning] failed to get relative path for %s: %v. Using absolute path.\n", testOutputDir, err)
			relOutputDir = testOutputDir
		}
		taskPrompt = fmt.Sprintf("%s\n\nIMPORTANT: Read and explore source code from the project root directory (not from the output directory). Write all output files to this isolated output directory: %s", taskPrompt, relOutputDir)
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

	// Extract output: for codegen tasks, prefer reading the written file from testOutputDir;
	// for docgen tasks, use the terminal synthesis node output, falling back to the last write_file content.
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
		if outputText == "" && t.Category == CategoryDocgen {
			// Fallback: if docgen didn't have a synthesis node, it might have written to a file
			outputText = extractLastWriteContent(taskID, graph, testOutputDir)
		}
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

func extractTerminalSynthesis(graph *compiler.ExecutionGraph, taskID string) string {
	if graph == nil {
		return ""
	}
	var lastOutput string
	for i := len(graph.Nodes) - 1; i >= 0; i-- {
		node := graph.Nodes[i]
		if node.Type == "synthesis" || node.Type == "recall" || node.Type == "probe" || node.ID == "terminal_synthesis" {
			if state, ok := memory.DB.GetNodeState(taskID, node.ID); ok {
				if state.RawOutput != "" {
					lastOutput = state.RawOutput
					break
				}
				if state.Output != "" {
					lastOutput = state.Output
					break
				}
			}
		}
	}
	return lastOutput
}

// extractLastWriteContent attempts to recover the generated documentation from the execution graph
// or the filesystem if a terminal synthesis node is missing. This is common when the
// agent's plan ends with a write_file action instead of a synthesis step.
func extractLastWriteContent(taskID string, graph *compiler.ExecutionGraph, testOutputDir string) string {
	if graph == nil || testOutputDir == "" {
		return ""
	}

	// Scan testOutputDir for any created Markdown files.
	var content string
	_ = filepath.Walk(testOutputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Prefer .md files for docgen tasks
		if strings.HasSuffix(strings.ToLower(path), ".md") {
			data, readErr := os.ReadFile(path)
			if readErr == nil && len(data) > 0 {
				content = string(data)
				return filepath.SkipAll // found it
			}
		}
		return nil
	})

	return content
}

// extractLastSourceCodeOutput finds the last completed source_code node's
// output from the DAG. This handles the case where spawned repair nodes
// produce updated code — we want the latest version, not just reason_code.
// The output is stripped of compilation evidence (## Compilation Result ...)
// that the CompilationGateHook appends during execution.
func extractLastSourceCodeOutput(taskID string, graph *compiler.ExecutionGraph) string {
	if graph == nil {
		return ""
	}

	// Walk nodes in reverse order — spawned nodes are appended at the end,
	// so the last completed source_code node has the most recent code.
	var lastOutput string
	for i := len(graph.Nodes) - 1; i >= 0; i-- {
		node := graph.Nodes[i]
		if node.OutputFormat != "source_code" {
			continue
		}
		state, ok := memory.DB.GetNodeState(taskID, node.ID)
		if !ok || state.Status != "completed" {
			continue
		}
		raw := state.RawOutput
		if raw == "" {
			raw = state.Output
		}
		if raw == "" {
			continue
		}

		// Strip compilation evidence appended by CompilationGateHook
		if idx := strings.Index(raw, "\n\n## Compilation Result\n"); idx >= 0 {
			raw = raw[:idx]
		}

		lastOutput = raw
		break
	}

	return lastOutput
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
// from the codegen package. When modelMode is "cooperative", the complexity
// gate routes between two execution strategies:
//
//   - simple: Direct local codegen with CompilationGateHook (self-repair spawns)
//   - complex: Draft mode — single-pass local draft (no compilation gate),
//     then cloud fix if compilation fails
//
// When modelMode is "cloud", complexity classification is skipped and direct
// generation always runs (the cloud model doesn't need the draft pipeline).
// Context is pre-computed via GatherContext (pure Go) and file writing is
// handled post-DAG via WriteCodeFile (pure Go).
func RunCodegenCondition(ctx context.Context, conditionID, modelMode string, t ComparisonTask, pricing PricingTable, outputDir string) (ComparisonResult, error) {
	// Set model mode for the codegen run
	originalModelMode := config.GlobalConfig.ModelMode
	config.GlobalConfig.ModelMode = modelMode
	defer func() {
		config.GlobalConfig.ModelMode = originalModelMode
	}()

	// Isolated database per condition run. Append timestamp to avoid SQLite
	// locking issues (disk I/O error 522) when runs happen in rapid succession.
	dbFile := fmt.Sprintf("tzro_comparison_%s_%s_%d.db", conditionID, t.ID, time.Now().UnixNano())
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



	// Create a directory for the codegen output (inside OutputDir if provided)
	var testOutputDir string
	var cleanup func()
	if outputDir != "" {
		testOutputDir = filepath.Join(outputDir, "test_outputs", conditionID, t.ID)
		if err := os.MkdirAll(testOutputDir, 0755); err != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create test output dir: %w", err)
		}
		if evalDir, evalErr := filepath.EvalSymlinks(testOutputDir); evalErr == nil {
			testOutputDir = evalDir
		}
		cleanup = func() {}
	} else {
		var tmpErr error
		testOutputDir, tmpErr = os.MkdirTemp("", "tzro_codegen_*")
		if tmpErr != nil {
			return ComparisonResult{}, fmt.Errorf("failed to create temp dir: %w", tmpErr)
		}
		if evalDir, evalErr := filepath.EvalSymlinks(testOutputDir); evalErr == nil {
			testOutputDir = evalDir
		}
		cleanup = func() { os.RemoveAll(testOutputDir) }
	}
	defer cleanup()

	// If the task has a seed file, copy it to the temp dir
	targetPath := filepath.Join(testOutputDir, t.Filepath)
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

	// Pre-compute context with a PathValidator that includes testOutputDir.
	extendedPaths := append(tools.GetAllowedPaths(), testOutputDir)
	validator := tools.NewStaticPathValidator(extendedPaths)
	codeCtx, ctxErr := codegen.GatherContext(targetPath, validator)
	if ctxErr != nil {
		fmt.Fprintf(os.Stderr, "[Comparison] GatherContext warning: %v\n", ctxErr)
		codeCtx = &codegen.CodeContext{
			Language: language,
			Siblings: make(map[string]string),
		}
	}

	// For Go tasks, create a go.mod so the compilation gate can run go build
	if language == "go" {
		goModPath := filepath.Join(testOutputDir, "go.mod")
		if _, err := os.Stat(goModPath); os.IsNotExist(err) {
			_ = os.WriteFile(goModPath, []byte("module benchmod\n\ngo 1.21\n"), 0644)
		}
	}

	// For TypeScript tasks, scaffold tsconfig + ambient type shims
	if language == "typescript" {
		scaffoldTypeScriptEnv(testOutputDir)
	}

	// Route: cloud mode always uses direct generation.
	// Cooperative mode classifies complexity to choose between direct and draft.
	useDraftMode := false
	if modelMode == "cooperative" {
		tier := codegen.ClassifyCodeComplexity(spec, codeCtx)
		useDraftMode = (tier == "complex")
		fmt.Fprintf(os.Stderr, "[Comparison] Complexity tier=%s for %s/%s → %s\n",
			tier, conditionID, t.ID, map[bool]string{true: "draft+fix", false: "direct"}[useDraftMode])
	}

	if useDraftMode {
		return runDraftFixMode(ctx, conditionID, spec, language, targetPath, testOutputDir, t, codeCtx, pricing)
	}
	return runDirectMode(ctx, conditionID, spec, language, targetPath, t, codeCtx, pricing)
}

// runDirectMode executes direct codegen with CompilationGateHook and self-repair.
// Used for cloud_code and simple cooperative tasks.
func runDirectMode(ctx context.Context, conditionID, spec, language, targetPath string, t ComparisonTask, codeCtx *codegen.CodeContext, pricing PricingTable) (ComparisonResult, error) {
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)

	taskID := fmt.Sprintf("comparison_%s_%s", conditionID, t.ID)
	startTime := time.Now()

	// Build the DAG with pre-computed context (single reason_code node)
	graph := codegen.BuildCodeDAG(taskID, spec, targetPath, language, 500, codeCtx)

	// Register the compilation gate hook so Edge Thought sees compilation
	// evidence and can trigger repair spawns (ADR-0036).
	compilationHook := &codegen.CompilationGateHook{
		FilePath: targetPath,
		Language: language,
		Spec:     spec,
	}
	executor.GlobalEngine.RegisterHook(compilationHook)
	defer executor.GlobalEngine.UnregisterHook(compilationHook)

	// Execute the DAG using the global engine
	err := executor.GlobalEngine.ExecuteGraphReactive(ctx, graph)

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

	// Post-process: extract the last source_code node's output and write file.
	rawCode := extractLastSourceCodeOutput(taskID, graph)
	if rawCode != "" {
		_, _, writeErr := codegen.WriteCodeFile(targetPath, rawCode, 500)
		if writeErr != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] WriteCodeFile failed: %v\n", writeErr)
		}
	}

	// Read the generated file as the output
	var outputText string
	if data, readErr := os.ReadFile(targetPath); readErr == nil {
		outputText = string(data)
	} else {
		outputText = extractTerminalSynthesis(graph, taskID)
	}

	// Run compilation gate (informational — logged for benchmark reporting)
	compResult := codegen.RunCompilationGate(language, targetPath)
	if !compResult.Pass {
		fmt.Fprintf(os.Stderr, "[Comparison] Compilation gate FAILED for %s/%s: %s\n", conditionID, t.ID, compResult.Reason)
	} else {
		fmt.Fprintf(os.Stderr, "[Comparison] Compilation gate PASSED for %s/%s\n", conditionID, t.ID)
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

// runDraftFixMode executes draft+fix codegen: a single-pass local draft
// (no CompilationGateHook, no self-repair), then a cloud fix if compilation
// fails. This was formerly the separate RunCodegenDraftCondition.
func runDraftFixMode(ctx context.Context, conditionID, spec, language, targetPath, tmpDir string, t ComparisonTask, codeCtx *codegen.CodeContext, pricing PricingTable) (ComparisonResult, error) {
	// ── Phase 1: Draft (local model, cooperative mode) ──────────────────

	// Fresh token tracker for draft phase
	draftTracker := inference.NewTokenTracker()
	draftCtx := inference.WithTokenTracker(ctx, draftTracker)

	taskID := fmt.Sprintf("comparison_%s_%s", conditionID, t.ID)
	startTime := time.Now()

	// Build the DAG — same structure as direct mode
	graph := codegen.BuildCodeDAG(taskID, spec, targetPath, language, 500, codeCtx)

	// KEY DIFFERENCE: Do NOT register the CompilationGateHook.
	// No AfterNode compilation check, no OnEdgeTraversal confidence override,
	// no repair spawns. The local model gets exactly one shot.
	fmt.Fprintf(os.Stderr, "[Comparison/Draft] Executing single-pass draft (no compilation gate) for %s/%s\n", conditionID, t.ID)

	err := executor.GlobalEngine.ExecuteGraphReactive(draftCtx, graph)

	draftLocalUsage, _ := draftTracker.GetUsage()

	if err != nil {
		wallClock := time.Since(startTime).Milliseconds()
		return ComparisonResult{
			TaskID:      t.ID,
			TaskTier:    t.Tier,
			Condition:   conditionID,
			LocalTokens: draftLocalUsage,
			WallClockMs: wallClock,
			EstCostUSD:  EstimateCost(inference.TokenUsage{}, draftLocalUsage, pricing),
			Error:       fmt.Sprintf("%s draft phase failed: %v", conditionID, err),
		}, nil
	}

	// Extract the draft output
	rawCode := extractLastSourceCodeOutput(taskID, graph)
	if rawCode != "" {
		_, _, writeErr := codegen.WriteCodeFile(targetPath, rawCode, 500)
		if writeErr != nil {
			fmt.Fprintf(os.Stderr, "[Comparison/Draft] WriteCodeFile failed: %v\n", writeErr)
		}
	}

	var draftText string
	if data, readErr := os.ReadFile(targetPath); readErr == nil {
		draftText = string(data)
	}

	// ── Phase 2: Fix (cloud model, if draft doesn't compile) ────────────

	compResult := codegen.RunCompilationGate(language, targetPath)

	var outputText string
	var cloudUsage inference.TokenUsage

	if compResult.Pass {
		// Draft compiles — no fix needed, zero cloud tokens
		fmt.Fprintf(os.Stderr, "[Comparison/Draft] Draft COMPILES for %s/%s — skipping fix phase\n", conditionID, t.ID)
		outputText = draftText
	} else {
		// Draft doesn't compile — run cloud fix
		fmt.Fprintf(os.Stderr, "[Comparison/Draft] Draft FAILED compilation for %s/%s — running cloud fix\n  Errors: %s\n",
			conditionID, t.ID, compResult.Reason)

		// Switch to cloud mode for the fix phase
		config.GlobalConfig.ModelMode = "cloud"

		// Fresh tracker for the fix phase (cloud tokens only)
		fixTracker := inference.NewTokenTracker()
		fixCtx := inference.WithTokenTracker(ctx, fixTracker)

		// Build a repair DAG using the draft + compiler errors
		moduleCtx := codegen.DiscoverModuleContext(targetPath, language)
		fixTaskID := fmt.Sprintf("comparison_%s_fix_%s", conditionID, t.ID)
		fixGraph := codegen.BuildRepairDAG(fixTaskID, draftText, compResult.Reason, spec, language, 500, moduleCtx)

		fixErr := executor.GlobalEngine.ExecuteGraphReactive(fixCtx, fixGraph)
		_, cloudUsage = fixTracker.GetUsage()

		if fixErr != nil {
			fmt.Fprintf(os.Stderr, "[Comparison/Draft] Cloud fix FAILED for %s/%s: %v\n", conditionID, t.ID, fixErr)
			// Use the draft as output (fix failed)
			outputText = draftText
		} else {
			// Extract the fixed code
			fixedCode := extractLastSourceCodeOutput(fixTaskID, fixGraph)
			if fixedCode != "" {
				_, _, writeErr := codegen.WriteCodeFile(targetPath, fixedCode, 500)
				if writeErr != nil {
					fmt.Fprintf(os.Stderr, "[Comparison/Draft] Fix WriteCodeFile failed: %v\n", writeErr)
				}
			}

			if data, readErr := os.ReadFile(targetPath); readErr == nil {
				outputText = string(data)
			} else {
				outputText = draftText // Fallback to draft
			}

			// Log whether the fix resolved compilation
			fixCompResult := codegen.RunCompilationGate(language, targetPath)
			if fixCompResult.Pass {
				fmt.Fprintf(os.Stderr, "[Comparison/Draft] Cloud fix RESOLVED compilation for %s/%s\n", conditionID, t.ID)
			} else {
				fmt.Fprintf(os.Stderr, "[Comparison/Draft] Cloud fix did NOT resolve compilation for %s/%s: %s\n",
					conditionID, t.ID, fixCompResult.Reason)
			}
		}

		// Restore cooperative mode for cleanup
		config.GlobalConfig.ModelMode = "cooperative"
	}

	wallClock := time.Since(startTime).Milliseconds()

	return ComparisonResult{
		TaskID:      t.ID,
		TaskTier:    t.Tier,
		Condition:   conditionID,
		CloudTokens: cloudUsage,
		LocalTokens: draftLocalUsage,
		WallClockMs: wallClock,
		EstCostUSD:  EstimateCost(cloudUsage, draftLocalUsage, pricing),
		OutputText:  outputText,
		DraftText:   draftText,
	}, nil
}

// scaffoldTypeScriptEnv creates a tsconfig.json and minimal ambient Node.js
// type declarations in the given directory so that tsc can resolve process.env,
// Buffer, setTimeout, console, etc. without requiring a full npm install of
// @types/node. The tsconfig.json uses a local "typings" typeRoot.
func scaffoldTypeScriptEnv(dir string) {
	tsconfigPath := filepath.Join(dir, "tsconfig.json")
	if _, err := os.Stat(tsconfigPath); os.IsNotExist(err) {
		tsconfig := `{
  "compilerOptions": {
    "strict": true,
    "target": "es2020",
    "lib": ["es2020"],
    "moduleResolution": "node",
    "noEmit": true,
    "skipLibCheck": true,
    "typeRoots": ["./typings"]
  },
  "include": ["**/*.ts"]
}
`
		_ = os.WriteFile(tsconfigPath, []byte(tsconfig), 0644)
	}

	// Create a minimal ambient type shim for Node.js globals.
	typingsDir := filepath.Join(dir, "typings", "node")
	_ = os.MkdirAll(typingsDir, 0755)
	nodeShim := `// Minimal Node.js ambient type declarations for benchmark compilation.
// This avoids requiring @types/node via npm install.

declare var process: {
  env: Record<string, string | undefined>;
  exit(code?: number): never;
  cwd(): string;
  argv: string[];
  platform: string;
  version: string;
};

declare var __dirname: string;
declare var __filename: string;
declare function require(id: string): any;
declare var module: { exports: any };
declare var exports: any;

declare var console: {
  log(...args: any[]): void;
  error(...args: any[]): void;
  warn(...args: any[]): void;
  info(...args: any[]): void;
  debug(...args: any[]): void;
};

declare var Buffer: {
  from(data: any, encoding?: string): any;
  alloc(size: number): any;
  isBuffer(obj: any): boolean;
};

declare function setTimeout(callback: (...args: any[]) => void, ms: number, ...args: any[]): any;
declare function setInterval(callback: (...args: any[]) => void, ms: number, ...args: any[]): any;
declare function clearTimeout(id: any): void;
declare function clearInterval(id: any): void;
`
	_ = os.WriteFile(filepath.Join(typingsDir, "index.d.ts"), []byte(nodeShim), 0644)
}
// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

// copyDir recursively copies a directory from src to dst.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() || (entry.Type()&os.ModeSymlink != 0) {
			// Resolve symlink to check if it's a directory
			realPath, err := filepath.EvalSymlinks(srcPath)
			if err == nil {
				if info, err := os.Stat(realPath); err == nil && info.IsDir() {
					if strings.HasPrefix(entry.Name(), "benchmark_results_") {
						continue
					}
					if err := copyDir(srcPath, dstPath); err != nil {
						return err
					}
					continue
				}
			}
		}

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}
