package comparison

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"tzro/internal/codegen"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/symbols"
	"tzro/internal/task"
	"tzro/internal/telemetry"
	"tzro/internal/tools"

	ignore "github.com/sabhiram/go-gitignore"
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

	// Save and restore model mode and phase runner config via the global pointer
	originalModelMode := config.GlobalConfig.ModelMode
	originalUsePhaseRunner := config.GlobalConfig.UsePhaseRunner
	config.GlobalConfig.ModelMode = modelMode
	config.GlobalConfig.UsePhaseRunner = true // Enable Phase Runner for Research/Analyze nodes (FM-3)
	defer func() {
		config.GlobalConfig.ModelMode = originalModelMode
		config.GlobalConfig.UsePhaseRunner = originalUsePhaseRunner
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
		// Load .gitignore from project root to avoid copying heavy ignored
		// directories (.git, .scratch, node_modules, model files, etc.).
		// Without this, targetPaths: ["."] copies the entire repo (801MB+)
		// including nested old benchmark results.
		var gi *ignore.GitIgnore
		giPath := filepath.Join(projectRoot, ".gitignore")
		if _, giErr := os.Stat(giPath); giErr == nil {
			gi, _ = ignore.CompileIgnoreFile(giPath)
		}

		for _, p := range t.TargetPaths {
			src := filepath.Join(projectRoot, p)
			dst := filepath.Join(testOutputDir, p)

			// Check if source exists
			info, err := os.Stat(src)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Warning: docgen target path %s does not exist: %v\n", src, err)
				continue
			}

			// Create parent directory in destination
			if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
				return ComparisonResult{}, fmt.Errorf("failed to create docgen target parent: %w", err)
			}

			// Copy file or directory
			if info.IsDir() {
				if err := copyDir(src, dst, projectRoot, gi); err != nil {
					fmt.Fprintf(os.Stderr, "[Comparison] Warning: failed to copy dir %s to %s: %v\n", src, dst, err)
				}
			} else {
				if err := copyFile(src, dst); err != nil {
					fmt.Fprintf(os.Stderr, "[Comparison] Warning: failed to copy file %s to %s: %v\n", src, dst, err)
				}
			}
		}
	}

	var adrCombinedPath string // Set by adr_summary pre-compilation for prompt augmentation (Fix 3)
	if t.ID == "adr_summary" {
		adrDir := filepath.Join(testOutputDir, "docs/adr")
		files, err := os.ReadDir(adrDir)
		if err == nil {
			var combined strings.Builder
			for _, file := range files {
				name := file.Name()
				if strings.HasPrefix(name, "00") && strings.HasSuffix(name, ".md") {
					content, readErr := os.ReadFile(filepath.Join(adrDir, name))
					if readErr == nil {
						combined.WriteString(fmt.Sprintf("# ADR %s\n\n%s\n\n---\n\n", name, string(content)))
					}
				}
			}
			combinedPath := filepath.Join(adrDir, "all_adrs_combined.md")
			_ = os.WriteFile(combinedPath, []byte(combined.String()), 0644)
			adrCombinedPath = combinedPath
			fmt.Fprintf(os.Stderr, "[Comparison] Pre-compiled all ADRs into %s\n", combinedPath)
		}
	} else if t.ID == "internal_architecture" {
		internalDir := filepath.Join(testOutputDir, "internal")
		graphSymbols, graphEdges, err := symbols.BuildCallGraph(internalDir)
		if err == nil && len(graphSymbols) > 0 {
			archContent, err := symbols.AssembleContext(graphSymbols, graphEdges, internalDir, false)
			if err == nil {
				combinedPath := filepath.Join(internalDir, "all_internal_combined.md")
				_ = os.WriteFile(combinedPath, []byte(archContent), 0644)
				fmt.Fprintf(os.Stderr, "[Comparison] Pre-compiled internal architecture via call graph into %s\n", combinedPath)
			}
		}
	} else if t.ID == "comprehensive_readme" {
		// Pre-compile ADRs
		adrDir := filepath.Join(testOutputDir, "docs/adr")
		files, err := os.ReadDir(adrDir)
		var adrsCombined string
		if err == nil {
			var combined strings.Builder
			for _, file := range files {
				name := file.Name()
				if strings.HasPrefix(name, "00") && strings.HasSuffix(name, ".md") {
					content, readErr := os.ReadFile(filepath.Join(adrDir, name))
					if readErr == nil {
						combined.WriteString(fmt.Sprintf("# ADR %s\n\n%s\n\n---\n\n", name, string(content)))
					}
				}
			}
			adrsCombined = combined.String()
		}

		// Pre-compile internal architecture via call graph
		internalDir := filepath.Join(testOutputDir, "internal")
		graphSymbols, graphEdges, cgErr := symbols.BuildCallGraph(internalDir)
		var archContentStr string
		if cgErr == nil && len(graphSymbols) > 0 {
			archContentStr, _ = symbols.AssembleContext(graphSymbols, graphEdges, internalDir, false)
		}
		archPath := filepath.Join(internalDir, "all_internal_combined.md")
		if archContentStr != "" {
			_ = os.WriteFile(archPath, []byte(archContentStr), 0644)
		}
		archContent, _ := os.ReadFile(archPath)

		// Create unified project compilation
		var projectCombined strings.Builder
		projectCombined.WriteString("# TZRO PROJECT COMPILATION FOR README\n\n")
		projectCombined.WriteString("## ARCHITECTURE OVERVIEW & PACKAGE MAP\n\n")
		projectCombined.WriteString(string(archContent))
		projectCombined.WriteString("\n\n---\n\n## DECISION LOGS & ADRS\n\n")
		projectCombined.WriteString(adrsCombined)

		projectPath := filepath.Join(testOutputDir, "all_project_combined.md")
		_ = os.WriteFile(projectPath, []byte(projectCombined.String()), 0644)
		fmt.Fprintf(os.Stderr, "[Comparison] Pre-compiled project map into %s\n", projectPath)
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
		// Register git_log tool for docgen tasks that need git history access
		// (e.g. holdout changelog generation task).
		tools.Register(tools.NewGitLogTool(readValidator))
		fmt.Fprintf(os.Stderr, "[Comparison] Docgen task %s: write_file scoped to %s, git_log registered\n", t.ID, testOutputDir)
	} else if t.Category == CategoryResearch {
		// Research tasks use web_search and web_browse for internet research.
		// Register these tools so the DAG planner can include them.
		tools.Register(tools.NewWebSearchTool())
		tools.Register(tools.NewWebBrowseTool())
		fmt.Fprintf(os.Stderr, "[Comparison] Research task %s: web_search + web_browse registered\n", t.ID)
	}

	// Initialize inference backend for Probe Node execution.
	// Without this, probe nodes fail with "no active inference backend".
	oldBackend := inference.ActiveBackend
	inference.ActiveBackend = inference.NewBackend(config.GlobalConfig.InferenceBackend, telemetry.Default)
	defer func() {
		inference.ActiveBackend = oldBackend
	}()

	// Auto-start both worker and router sidecars if not already running.
	// StartActive starts both in parallel (router only if routerModelPath is configured).
	status, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.StartActive(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for %s: %v\n", conditionID, err)
		} else {
			if err := waitForSidecarHealth("worker", inference.GlobalWorkerModel); err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Worker health check failed: %v\n", err)
			}
			if err := waitForSidecarHealth("router", inference.GlobalRouterModel); err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Router health check failed: %v\n", err)
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

		// Fix 3: Point the Probe at the pre-compiled ADR file so it doesn't
		// miss files due to step budget exhaustion.
		if adrCombinedPath != "" {
			taskPrompt = fmt.Sprintf("%s\n\nIMPORTANT: All ADR files have been pre-compiled into a single document at: %s. Read this file FIRST to access all ADR content in one read.", taskPrompt, adrCombinedPath)
		}
	} else if t.Category == CategoryDatanal {
		// For datanal tasks, the CSV file is at helpers/LeadSuccess.csv relative to the project root.
		taskPrompt = fmt.Sprintf("%s\n\nIMPORTANT: The data file is located in the project directory at: %s/helpers/LeadSuccess.csv", taskPrompt, projectRoot)
	} else if t.Category == CategoryResearch {
		// For research tasks, instruct the model to use web tools and cite sources.
		taskPrompt = fmt.Sprintf("%s\n\nIMPORTANT: You have access to web_search and web_browse tools. Use web_search to find relevant sources, then use web_browse to read full page content from the most promising URLs. Always cite your sources with the actual URLs you visited. Do not fabricate or hallucinate URLs.", taskPrompt)
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
	// for docgen tasks, prefer reading the created markdown documentation file from testOutputDir,
	// falling back to terminal synthesis if no file was written.
	var outputText string
	if codegenTargetPath != "" {
		if data, readErr := os.ReadFile(codegenTargetPath); readErr == nil && len(data) > 0 {
			outputText = string(data)
		} else {
			// Fallback to terminal synthesis if no file was written
			outputText = extractTerminalSynthesis(graph, taskID)
		}
	} else if t.Category == CategoryDocgen {
		if docContent := extractLastWriteContent(taskID, graph, testOutputDir); docContent != "" {
			outputText = docContent
		} else {
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

func extractTerminalSynthesis(graph *compiler.ExecutionGraph, taskID string) string {
	if graph == nil {
		return ""
	}
	var lastOutput string
	for i := len(graph.Nodes) - 1; i >= 0; i-- {
		node := graph.Nodes[i]
		if node.Type == "synthesis" || node.Type == "recall" || node.Type == "probe" || node.Type == "analyze" || node.ID == "terminal_synthesis" {
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

	// Fix 2: Sanitize synthesis output — strip model reasoning artifacts
	// (<thinking> tags, repetitive preambles) and fallback to accumulated
	// node outputs when synthesis is empty or invalid.
	cleaned := sanitizeSynthesisOutput(lastOutput, 50)
	if cleaned != "" {
		return cleaned
	}

	// Fallback: accumulate partial outputs from completed action/tool nodes
	if lastOutput != "" {
		fmt.Fprintf(os.Stderr, "[extractTerminalSynthesis] Synthesis output invalid after sanitization (%d raw chars → 0 clean chars). Falling back to accumulated partial outputs.\n", len(lastOutput))
	}
	return accumulatePartialOutputs(graph, taskID)
}

// sanitizeSynthesisOutput strips model reasoning artifacts (<thinking> tags,
// repetitive preambles) from synthesis output. If the cleaned result is too
// short (< minChars), it returns empty string to trigger fallback to
// accumulated node outputs.
func sanitizeSynthesisOutput(raw string, minChars int) string {
	if raw == "" {
		return ""
	}

	cleaned := raw

	// Strip execution tier prefixes (e.g., "[Local Tactician] ", "[Cloud Fallback] ")
	// that are observability metadata and must not appear in evaluated output.
	tierPrefixes := []string{
		"[Local Tactician] ",
		"[Cloud Fallback] ",
		"[Recall] ",
		"[Local] ",
	}
	for _, p := range tierPrefixes {
		if strings.HasPrefix(cleaned, p) {
			cleaned = cleaned[len(p):]
			break
		}
	}

	// Strip <thinking>...</thinking> blocks (greedy, handles newlines)
	thinkingRe := regexp.MustCompile(`(?s)<thinking>.*?</thinking>`)
	cleaned = thinkingRe.ReplaceAllString(cleaned, "")

	// Strip leading/trailing whitespace
	cleaned = strings.TrimSpace(cleaned)

	if len(cleaned) < minChars {
		return ""
	}

	return cleaned
}

// accumulatePartialOutputs collects outputs from completed non-synthesis nodes
// as a fallback when synthesis produces no usable content.
func accumulatePartialOutputs(graph *compiler.ExecutionGraph, taskID string) string {
	if graph == nil {
		return ""
	}
	var parts []string
	for _, node := range graph.Nodes {
		if node.Type == "synthesis" || node.ID == "terminal_synthesis" {
			continue
		}
		if state, ok := memory.DB.GetNodeState(taskID, node.ID); ok && state.Status == "completed" {
			output := state.RawOutput
			if output == "" {
				output = state.Output
			}
			if len(output) > 100 { // Only include substantive outputs
				parts = append(parts, fmt.Sprintf("## %s\n%s", node.ID, output))
			}
		}
	}
	return strings.Join(parts, "\n\n---\n\n")
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
	inference.ActiveBackend = inference.NewBackend(config.GlobalConfig.InferenceBackend, telemetry.Default)
	defer func() {
		inference.ActiveBackend = oldBackend
	}()

	// Auto-start both worker and router sidecars if needed.
	status, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	if status == "Stopped" {
		if err := inference.StartActive(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "[Comparison] Sidecar auto-start failed for %s: %v\n", conditionID, err)
		} else {
			if err := waitForSidecarHealth("worker", inference.GlobalWorkerModel); err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Worker health check failed: %v\n", err)
			}
			if err := waitForSidecarHealth("router", inference.GlobalRouterModel); err != nil {
				fmt.Fprintf(os.Stderr, "[Comparison] Router health check failed: %v\n", err)
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
	// ADR-0057: AllowCloudRepair enables cloud repair escalation after
	// local repair attempts are exhausted (Direct mode = true).
	compilationHook := &codegen.CompilationGateHook{
		FilePath:         targetPath,
		Language:         language,
		Spec:             spec,
		AllowCloudRepair: true,
		TaskTier:         t.Tier,  // ADR-0070: T4+ triggers cloud semantic review
		AllowCloudReview: true,
	}
	// FM-4: Populate OriginalContent for update tasks so the preservation
	// assertion can detect removed public symbols.
	if codeCtx != nil && codeCtx.ExistingContent != "" {
		compilationHook.OriginalContent = codeCtx.ExistingContent
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

	// Run compilation gate — if it fails and the cloud model hasn't been
	// used yet, attempt a single cloud repair pass. The in-DAG
	// CompilationGateHook requires MaxLocalRepairAttempts (2) failures to
	// escalate, but the codegen DAG only has one reason_code node, so the
	// hook fires at most once and never reaches the threshold.
	compResult := codegen.RunCompilationGate(language, targetPath)
	if !compResult.Pass {
		fmt.Fprintf(os.Stderr, "[Comparison] Compilation gate FAILED for %s/%s: %s\n", conditionID, t.ID, compResult.Reason)

		// Post-DAG cloud repair: only if no cloud tokens were used during
		// the DAG execution (avoids double cloud cost when the hook already
		// escalated). Uses the same narrow repair payload as the hook.
		_, currentCloudUsage := tracker.GetUsage()
		if currentCloudUsage.TotalTokens == 0 {
			fmt.Fprintf(os.Stderr, "[Comparison] Attempting post-DAG cloud repair for %s/%s\n", conditionID, t.ID)

			originalModelMode := config.GlobalConfig.ModelMode
			config.GlobalConfig.ModelMode = "cloud"

			moduleCtx := codegen.DiscoverModuleContext(targetPath, language)
			repairTaskID := fmt.Sprintf("comparison_%s_repair_%s", conditionID, t.ID)
			repairGraph := codegen.BuildRepairDAG(repairTaskID, outputText, compResult.Reason, spec, language, 500, moduleCtx)

			repairErr := executor.GlobalEngine.ExecuteGraphReactive(ctx, repairGraph)
			config.GlobalConfig.ModelMode = originalModelMode

			if repairErr == nil {
				repairedCode := extractLastSourceCodeOutput(repairTaskID, repairGraph)
				if repairedCode != "" {
					_, _, writeErr := codegen.WriteCodeFile(targetPath, repairedCode, 500)
					if writeErr != nil {
						fmt.Fprintf(os.Stderr, "[Comparison] Repair WriteCodeFile failed: %v\n", writeErr)
					}
				}

				// Re-read and re-check compilation
				if data, readErr := os.ReadFile(targetPath); readErr == nil {
					outputText = string(data)
				}
				recheckResult := codegen.RunCompilationGate(language, targetPath)
				if recheckResult.Pass {
					fmt.Fprintf(os.Stderr, "[Comparison] Post-DAG cloud repair RESOLVED compilation for %s/%s\n", conditionID, t.ID)
				} else {
					fmt.Fprintf(os.Stderr, "[Comparison] Post-DAG cloud repair did NOT resolve compilation for %s/%s: %s\n",
						conditionID, t.ID, recheckResult.Reason)
				}
			} else {
				fmt.Fprintf(os.Stderr, "[Comparison] Post-DAG cloud repair FAILED for %s/%s: %v\n", conditionID, t.ID, repairErr)
			}
		}
	} else {
		fmt.Fprintf(os.Stderr, "[Comparison] Compilation gate PASSED for %s/%s\n", conditionID, t.ID)
	}

	// Re-read usage after potential cloud repair
	wallClock = time.Since(startTime).Milliseconds()
	localUsage, cloudUsage = tracker.GetUsage()

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

// runDraftFixMode executes draft+fix codegen: a local draft with compilation
// feedback loop (CompilationGateHook + cloud repair escalation), then a
// post-hoc cloud fix as a second-chance fallback if compilation still fails.
//
// Benchmark results-full-2 showed that without the CompilationGateHook, Go
// tasks consistently scored ≤ 2.5 (3/7 draft tasks). Enabling the hook gives
// draft mode the same compile→diagnose→repair loop as direct mode (ADR-0036
// + ADR-0057).
func runDraftFixMode(ctx context.Context, conditionID, spec, language, targetPath, tmpDir string, t ComparisonTask, codeCtx *codegen.CodeContext, pricing PricingTable) (ComparisonResult, error) {
	// ── Phase 1: Draft (local model, cooperative mode) ──────────────────

	// Fresh token tracker for draft phase
	draftTracker := inference.NewTokenTracker()
	draftCtx := inference.WithTokenTracker(ctx, draftTracker)

	taskID := fmt.Sprintf("comparison_%s_%s", conditionID, t.ID)
	startTime := time.Now()

	// Build the DAG — same structure as direct mode
	graph := codegen.BuildCodeDAG(taskID, spec, targetPath, language, 500, codeCtx)

	// Register CompilationGateHook WITH cloud repair escalation.
	// This gives draft mode the same compile→diagnose→repair loop as direct
	// mode (ADR-0036 + ADR-0057). Without this, Go tasks consistently fail
	// compilation with no recovery path (benchmark results-full-2: 3 Go tasks
	// all scoring ≤ 2.5).
	compilationHook := &codegen.CompilationGateHook{
		FilePath:         targetPath,
		Language:         language,
		Spec:             spec,
		AllowCloudRepair: true,
		TaskTier:         t.Tier,  // ADR-0070: T4+ triggers cloud semantic review
		AllowCloudReview: true,
	}
	// FM-4: Populate OriginalContent for update tasks.
	if codeCtx != nil && codeCtx.ExistingContent != "" {
		compilationHook.OriginalContent = codeCtx.ExistingContent
	}
	executor.GlobalEngine.RegisterHook(compilationHook)
	defer executor.GlobalEngine.UnregisterHook(compilationHook)
	fmt.Fprintf(os.Stderr, "[Comparison/Draft] Executing draft WITH compilation gate for %s/%s\n", conditionID, t.ID)

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

// copyDir recursively copies a directory from src to dst, skipping entries
// that match the project's .gitignore rules. The projectRoot parameter is
// used to compute relative paths for gitignore matching. If gi is nil,
// all entries are copied (backward-compatible).
func copyDir(src, dst, projectRoot string, gi *ignore.GitIgnore) error {
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

		// Always skip .git directory — even if not in .gitignore
		if entry.Name() == ".git" {
			continue
		}

		// Check gitignore rules using path relative to project root
		if gi != nil {
			relPath, relErr := filepath.Rel(projectRoot, srcPath)
			if relErr == nil {
				// Append trailing slash for directories so gitignore
				// directory patterns (e.g. "node_modules/") match correctly
				matchPath := relPath
				if entry.IsDir() {
					matchPath = relPath + "/"
				}
				if gi.MatchesPath(matchPath) {
					continue
				}
			}
		}

		if entry.IsDir() || (entry.Type()&os.ModeSymlink != 0) {
			// Resolve symlink to check if it's a directory
			realPath, err := filepath.EvalSymlinks(srcPath)
			if err == nil {
				if info, err := os.Stat(realPath); err == nil && info.IsDir() {
					if err := copyDir(srcPath, dstPath, projectRoot, gi); err != nil {
						return err
					}
					continue
				}
			}
		}

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath, projectRoot, gi); err != nil {
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

// waitForSidecarHealth blocks until a sidecar's /health endpoint returns 200 OK,
// or gives up after 90 attempts (1s apart). Returns an error if the sidecar fails
// to become healthy. Does nothing (returns nil) if the sidecar is in "Stopped" state.
//
// The function also checks process liveness every 5 attempts to bail early if the
// sidecar process crashed during model loading (avoids wasting 90s on a dead process).
func waitForSidecarHealth(label string, model *inference.LocalModelManager) error {
	status, activePort, _, _, _ := model.GetStatusInfo()
	if strings.ToLower(status) == "stopped" || activePort == 0 {
		return nil // not configured or didn't start
	}
	fmt.Fprintf(os.Stderr, "[Comparison] Waiting for %s sidecar health on port %d...\n", label, activePort)

	const maxAttempts = 90 // 90s budget — non-catalog models may take longer to load
	for attempt := range maxAttempts {
		healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
		resp, err := http.Get(healthURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			fmt.Fprintf(os.Stderr, "[Comparison] %s sidecar healthy after %d attempts\n", label, attempt+1)
			return nil
		}
		if resp != nil {
			resp.Body.Close()
		}

		// Every 5 attempts, verify the sidecar process is still alive.
		// This prevents wasting the full 90s polling a dead process.
		if attempt > 0 && attempt%5 == 0 {
			currentStatus, _, _, _, _ := model.GetStatusInfo()
			if strings.ToLower(currentStatus) == "stopped" {
				return fmt.Errorf("%s sidecar process died during model load (port %d)", label, activePort)
			}
		}

		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("%s sidecar health check timed out after %ds on port %d", label, maxAttempts, activePort)
}
