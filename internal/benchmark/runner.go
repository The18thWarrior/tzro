package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"tzro/internal/benchmark/matcher"
	"tzro/internal/benchmark/mock"
	"tzro/internal/benchmark/vfs"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/executor"
	"tzro/internal/inference"
	"tzro/internal/memory"
	"tzro/internal/task"
	"tzro/internal/tools"
)

// ToolDefinition represents standard API metadata within the benchmark.
type ToolDefinition struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Parameters  map[string]interface{} `json:"parameters"`
}

// ExpectedCall represents an expected tool invocation and its parameters.
type ExpectedCall struct {
	ToolName string                 `json:"tool_name"`
	Args     map[string]interface{} `json:"args"`
}

// BenchmarkTurn represents a conversational back-and-forth step.
type BenchmarkTurn struct {
	UserMessage      string                 `json:"user_message"`
	ExpectedCalls    []ExpectedCall         `json:"expected_calls,omitempty"`
	ExpectedToolCall string                 `json:"expected_tool_call,omitempty"`
	ExpectedArgs     map[string]interface{} `json:"expected_args,omitempty"`
	MockResponse     string                 `json:"mock_response"`
}

// BenchmarkTestCase is a single benchmark flow case.
type BenchmarkTestCase struct {
	ID            string                 `json:"id"`
	Dataset       string                 `json:"dataset"` // "bfcl" | "complexfuncbench"
	SystemPrompt  string                 `json:"system_prompt"`
	Tools         []ToolDefinition       `json:"tools"`
	Turns         []BenchmarkTurn        `json:"turns"`
	InitialConfig map[string]interface{} `json:"initial_config,omitempty"`
}

// BenchmarkResult records the analytics outcome.
type BenchmarkResult struct {
	TestCaseID          string               `json:"testCaseId"`
	Dataset             string               `json:"dataset"`
	Passed              bool                 `json:"passed"`
	PlanningMatch       bool                 `json:"planningMatch"`
	ParameterMatch      bool                 `json:"parameterMatch"`
	FuzzyMatchUsed      bool                 `json:"fuzzyMatchUsed"`
	ErrorMessage        string               `json:"errorMessage,omitempty"`
	ExecutedToolCalls   []string             `json:"executedToolCalls"`
	ExecutionDurationMs int64                `json:"executionDurationMs"`
	LocalTokens         inference.TokenUsage `json:"localTokens"`
	CloudTokens         inference.TokenUsage `json:"cloudTokens"`
}

// ExecutedCall tracks parameters passed to mock tools during execution.
type ExecutedCall struct {
	ToolName string
	Args     map[string]interface{}
}

// TurnExecution tracks expected vs actual execution sequence and arguments per interactive turn.
type TurnExecution struct {
	ExpectedTool  string
	ExpectedArgs  map[string]interface{}
	UserMessage   string
	ExecutedCalls []ExecutedCall
}

// VirtualFilesystem was extracted to the vfs package.

var (
	executedCallsMutex sync.Mutex
	executedCalls      []ExecutedCall
)

func recordExecutedCall(name string, args map[string]interface{}) {
	executedCallsMutex.Lock()
	defer executedCallsMutex.Unlock()
	executedCalls = append(executedCalls, ExecutedCall{ToolName: name, Args: args})
}

// LoadTestCases loads the complete suite from JSON files under testdata.
func LoadTestCases(dataset string) ([]BenchmarkTestCase, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	// If running under go test, load lightweight test samples to avoid taking 10 minutes per run
	isTest := false
	for _, arg := range os.Args {
		if strings.HasPrefix(arg, "-test.") || strings.Contains(arg, "test") {
			isTest = true
			break
		}
	}

	baseName := dataset + "_samples.json"
	if isTest && dataset == "bfcl" {
		baseName = "bfcl_test_samples.json"
	}

	var filename string
	// Dynamic check to handle execution from repository root or internal/benchmark package
	if strings.HasSuffix(wd, filepath.Join("internal", "benchmark")) {
		filename = filepath.Join(wd, "testdata", baseName)
	} else {
		filename = filepath.Join(wd, "internal", "benchmark", "testdata", baseName)
	}

	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read dataset file %s: %w", filename, err)
	}

	var list []BenchmarkTestCase
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to parse dataset json: %w", err)
	}

	return list, nil
}

// MockTool wraps a benchmark tool description dynamically in the tools.Registry.
type MockTool struct {
	name         string
	schema       string
	mockResponse string
	vfs          *vfs.VirtualFilesystem
}

func (m *MockTool) Name() string {
	return m.name
}

func (m *MockTool) GetSchema() (string, error) {
	return m.schema, nil
}

func (m *MockTool) Call(ctx context.Context, args map[string]interface{}) (string, error) {
	// Clean structural parameters injected by parser
	cleanedArgs := make(map[string]interface{})
	if toolArgs, ok := args["tool_arguments"].(map[string]interface{}); ok {
		cleanedArgs = toolArgs
	} else {
		for k, v := range args {
			if k != "tool_arguments" {
				cleanedArgs[k] = v
			}
		}
	}

	if m.vfs != nil {
		m.vfs.Mutate(m.name, cleanedArgs)
	}

	recordExecutedCall(m.name, cleanedArgs)
	return m.mockResponse, nil
}

// SuiteCallbacks represents lifecycle hooks triggered during benchmark suite execution.
type SuiteCallbacks struct {
	OnTestStart    func(testCaseID string)
	OnTestComplete func(result BenchmarkResult)
}

// RunSuite runs the entire dataset suite in consolidated or interactive mode.
func RunSuite(ctx context.Context, dataset string, mode string, modelMode string, realLLM bool, limit int, callbacks ...SuiteCallbacks) ([]BenchmarkResult, error) {
	// Set mock config settings in memory only to avoid polluting disk configs
	oldConfig := config.Get()
	defer config.Override(&oldConfig)

	cfg := oldConfig
	cfg.ModelMode = modelMode
	if !realLLM {
		cfg.CloudProvider = "openai"
		cfg.CloudAPIKey = "mock-key"
	}
	config.Override(&cfg)

	// Automatically start and manage local llama-server GGUF sidecar if local/cooperative real benchmark is selected
	if realLLM && (modelMode == "local" || modelMode == "cooperative") {
		status, activePort, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
		if status == "Stopped" {
			fmt.Fprintln(os.Stderr, "[Benchmark] Starting local model sidecar automatically...")
			if err := inference.GlobalLocalModel.Start(ctx); err != nil {
				return nil, fmt.Errorf("failed to start local model sidecar: %w", err)
			}
			defer func() {
				fmt.Fprintln(os.Stderr, "[Benchmark] Stopping local model sidecar...")
				_ = inference.GlobalLocalModel.Stop()
			}()

			// Retrieve the newly allocated port
			_, activePort, _, _, _ = inference.GlobalLocalModel.GetStatusInfo()

			// Wait for llama-server to become healthy (maximum 60 seconds)
			fmt.Fprintf(os.Stderr, "[Benchmark] Waiting for local model sidecar on port %d to load GGUF and become healthy...\n", activePort)
			healthURL := fmt.Sprintf("http://localhost:%d/health", activePort)
			client := &http.Client{Timeout: 2 * time.Second}
			startWait := time.Now()
			healthy := false

			for time.Since(startWait) < 60*time.Second {
				req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
				if err == nil {
					resp, err := client.Do(req)
					if err == nil {
						if resp.StatusCode == http.StatusOK {
							healthy = true
							resp.Body.Close()
							break
						}
						resp.Body.Close()
					}
				}
				time.Sleep(1 * time.Second)
				fmt.Fprint(os.Stderr, ".")
			}
			fmt.Fprintln(os.Stderr)

			if !healthy {
				return nil, fmt.Errorf("local model sidecar failed to become healthy within 60 seconds")
			}
			fmt.Fprintln(os.Stderr, "[Benchmark] Local model sidecar is active, healthy, and ready!")
		}
	}

	// Set database path for test isolation
	oldDBPath := memory.DB.GetDBPathForTesting()
	memory.DB.SetDBPathForTesting("tzro_benchmark.db")
	defer func() {
		memory.DB.Close()
		_ = os.Remove("tzro_benchmark.db")
		memory.DB.SetDBPathForTesting(oldDBPath)
		_ = memory.DB.Init()
	}()

	if err := memory.DB.Init(); err != nil {
		return nil, fmt.Errorf("failed to init benchmark database: %w", err)
	}

	// Initialize standard tool registry to register cache tools (e.g. jq_cached_data)
	_ = tools.Init("")

	testCases, err := LoadTestCases(dataset)
	if err != nil {
		return nil, err
	}
	if limit > 0 && limit < len(testCases) {
		testCases = StratifiedSample(testCases, limit)
	}

	var results []BenchmarkResult

	if !realLLM {
		var mu sync.Mutex
		results = make([]BenchmarkResult, len(testCases))
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(10)

		var transportMu sync.Mutex

		for idx, tc := range testCases {
			idx := idx
			tc := tc
			g.Go(func() error {
				for _, cb := range callbacks {
					if cb.OnTestStart != nil {
						cb.OnTestStart(tc.ID)
					}
				}
				transportMu.Lock()
				res, err := runSingleTestCase(gCtx, tc, mode, realLLM)
				transportMu.Unlock()
				if err != nil {
					res.TestCaseID = tc.ID
					res.Dataset = tc.Dataset
					res.Passed = false
					res.ErrorMessage = err.Error()
				}
				for _, cb := range callbacks {
					if cb.OnTestComplete != nil {
						cb.OnTestComplete(res)
					}
				}
				mu.Lock()
				results[idx] = res
				mu.Unlock()
				return nil
			})
		}
		_ = g.Wait()
	} else {
		for _, tc := range testCases {
			for _, cb := range callbacks {
				if cb.OnTestStart != nil {
					cb.OnTestStart(tc.ID)
				}
			}
			res, err := runSingleTestCase(ctx, tc, mode, realLLM)
			if err != nil {
				res.TestCaseID = tc.ID
				res.Dataset = tc.Dataset
				res.Passed = false
				res.ErrorMessage = err.Error()
			}
			for _, cb := range callbacks {
				if cb.OnTestComplete != nil {
					cb.OnTestComplete(res)
				}
			}
			results = append(results, res)
		}
	}

	return results, nil
}

func runSingleTestCase(ctx context.Context, tc BenchmarkTestCase, mode string, realLLM bool) (res BenchmarkResult, err error) {
	// Populate expected_calls dynamically for all turns to maintain absolute backward compatibility
	for i, turn := range tc.Turns {
		if len(turn.ExpectedCalls) == 0 && turn.ExpectedToolCall != "" {
			turn.ExpectedCalls = []ExpectedCall{
				{
					ToolName: turn.ExpectedToolCall,
					Args:     turn.ExpectedArgs,
				},
			}
			tc.Turns[i] = turn
		}
	}

	vfsObj := vfs.NewVirtualFilesystem(tc.InitialConfig)
	tracker := inference.NewTokenTracker()
	ctx = inference.WithTokenTracker(ctx, tracker)
	ctx = context.WithValue(ctx, "is_benchmark", true)
	defer func() {
		localUsage, cloudUsage := tracker.GetUsage()
		res.LocalTokens = localUsage
		res.CloudTokens = cloudUsage
	}()
	// 1. Dynamic Mock tool registration in tools registry
	for _, tool := range tc.Tools {
		schemaBytes, _ := json.Marshal(tool.Parameters)
		schemaStr := string(schemaBytes)

		// Create dynamic GBNF wrapped format containing tool_arguments nesting
		wrappedSchema := fmt.Sprintf(`{
			"type": "object",
			"description": %q,
			"properties": {
				"tool_arguments": %s
			},
			"required": ["tool_arguments"]
		}`, tool.Description, schemaStr)

		// Map appropriate mock return payload from turns
		mockResp := `{"status":"mock"}`
		for _, turn := range tc.Turns {
			found := false
			if turn.ExpectedToolCall == tool.Name {
				found = true
			} else {
				for _, ec := range turn.ExpectedCalls {
					if ec.ToolName == tool.Name {
						found = true
						break
					}
				}
			}
			if found {
				mockResp = turn.MockResponse
				break
			}
		}

		mockT := &MockTool{
			name:         tool.Name,
			schema:       wrappedSchema,
			mockResponse: mockResp,
			vfs:          vfsObj,
		}
		tools.Register(mockT)
		defer tools.Unregister(tool.Name)
	}

	// 2. Start dynamic completions HTTP Interceptor if mock simulation mode is active
	if !realLLM {
		tcJSON, _ := json.Marshal(tc)
		runner := &mock.Runner{}
		runner.StartMockServer(tcJSON, mode)
		defer runner.StopMockServer()
	}

	// Clear executed calls list prior to run
	executedCallsMutex.Lock()
	executedCalls = []ExecutedCall{}
	executedCallsMutex.Unlock()

	startTime := time.Now()

	var planningMatch, parameterMatch bool
	var turnExecs []TurnExecution

	if mode == "consolidated" {
		// --- CONSOLIDATED DAG PLANNING AND EXECUTION ---
		// We assemble the user's multi-step goal in a single consolidated prompt
		var goalPrompts []string
		for _, t := range tc.Turns {
			goalPrompts = append(goalPrompts, t.UserMessage)
		}
		fullPrompt := strings.Join(goalPrompts, " and then ")

		// Call deep Task.Execute seam (runs planning, sorting and parallel executor node runtimes)
		_, _, err := task.Execute(ctx, fullPrompt, task.ExecuteOptions{
			TaskID:     tc.ID,
			IntentType: "workflow",
		})
		if err != nil {
			return BenchmarkResult{}, fmt.Errorf("consolidated execution failed: %w", err)
		}

	} else {
		// --- STATEFUL INTERACTIVE MULTI-TURN SIMULATION ---
		// Executed turn-by-turn. At each turn:
		// 1. We plan a sub-DAG (max 10 nodes).
		// 2. Run execution, retrieve mock responses.
		// 3. Write responses to Tabular/Graph-RAG memories to inject as context into subsequent turns.
		for turnIdx, turn := range tc.Turns {
			// Inject virtual filesystem state context for GorillaFileSystem evaluations
			augmentedUserPrompt := turn.UserMessage
			if tc.InitialConfig != nil && tc.InitialConfig["GorillaFileSystem"] != nil {
				vfsState := vfsObj.RenderEnvironmentBlock()
				augmentedUserPrompt = fmt.Sprintf("%s\n\n%s", turn.UserMessage, vfsState)
			}

			// Record starting index of executed calls for this turn
			executedCallsMutex.Lock()
			startIdx := len(executedCalls)
			executedCallsMutex.Unlock()

			// Compile DAG planning
			graph, err := task.Plan(ctx, augmentedUserPrompt, task.ExecuteOptions{
				TaskID:     fmt.Sprintf("%s_t%d", tc.ID, turnIdx),
				IntentType: "task",
			})
			if err != nil {
				return BenchmarkResult{}, fmt.Errorf("turn %d planning failed: %w", turnIdx, err)
			}

			// Constraint Check: Max 10 nodes compiled per turn
			if len(graph.Nodes) > 10 {
				return BenchmarkResult{}, fmt.Errorf("turn %d planned DAG has %d nodes, exceeding multi-turn limit of 10", turnIdx, len(graph.Nodes))
			}

			// Topological compile and execution
			levels, err := compiler.CompileAndSort(graph)
			if err != nil {
				return BenchmarkResult{}, fmt.Errorf("turn %d compilation failed: %w", turnIdx, err)
			}

			err = executor.GlobalEngine.ExecuteGraph(ctx, graph, levels)
			if err != nil {
				return BenchmarkResult{}, fmt.Errorf("turn %d execution failed: %w", turnIdx, err)
			}

			// Capture executed calls for this turn
			executedCallsMutex.Lock()
			endIdx := len(executedCalls)
			var turnCalls []ExecutedCall
			if endIdx > startIdx {
				turnCalls = append(turnCalls, executedCalls[startIdx:endIdx]...)
			}
			executedCallsMutex.Unlock()

			turnExecs = append(turnExecs, TurnExecution{
				ExpectedTool:  turn.ExpectedToolCall,
				ExpectedArgs:  turn.ExpectedArgs,
				UserMessage:   turn.UserMessage,
				ExecutedCalls: turnCalls,
			})

			// Log Dialogue Turn to Memory (Session History Compaction support)
			var executedTools []string
			for _, ac := range turnCalls {
				argsBytes, _ := json.Marshal(ac.Args)
				executedTools = append(executedTools, fmt.Sprintf("%s(%s)", ac.ToolName, string(argsBytes)))
			}
			sessionID := memory.GetSessionID(tc.ID)
			memory.DB.AddSessionTurn(sessionID, turn.UserMessage, executedTools)

			// STATEFUL CONTEXT MANAGEMENT SETUP: Ingest results into SQLite memory and Knowledge Graph
			// Combine outputs/mock results of all executed calls in this turn to feed back to memories
			for _, ac := range turnCalls {
				mockResp := `{"status":"success"}`
				for _, t := range tc.Tools {
					if t.Name == ac.ToolName {
						for _, turnItem := range tc.Turns {
							if turnItem.ExpectedToolCall == t.Name {
								mockResp = turnItem.MockResponse
								break
							}
						}
						break
					}
				}

				var parsed map[string]interface{}
				if json.Unmarshal([]byte(mockResp), &parsed) == nil {
					// Inject Tabular Memory
					_ = memory.DB.AddMemory(memory.FactMemory{
						UserID:     "benchmark_user",
						Type:       "fact",
						Content:    fmt.Sprintf("Turn %d execution of %s result details: %s", turnIdx+1, ac.ToolName, mockResp),
						Context:    turn.UserMessage,
						Confidence: 0.99,
						Source:     "auto_reflection",
					})

					// Ingest into Relational Knowledge Graph as target nodes
					nodeID := fmt.Sprintf("entity_%s_t%d_%s", ac.ToolName, turnIdx, tc.ID)
					_ = memory.DB.AddNode(memory.KGNode{
						ID:       nodeID,
						NodeType: "turn_result",
						Name:     ac.ToolName,
						Metadata: parsed,
						Source:   "benchmark_runner",
						Weight:   1.0,
					})
				}
			}
		}
	}

	durationMs := time.Since(startTime).Milliseconds()

	// 3. ANALYTICS: Evaluate Planning and Parameter Extraction Accuracy
	executedCallsMutex.Lock()
	actualCalls := append([]ExecutedCall(nil), executedCalls...)
	executedCallsMutex.Unlock()

	var actualNames []string
	for _, ac := range actualCalls {
		actualNames = append(actualNames, ac.ToolName)
	}

	var fuzzyMatchUsed bool

	if mode == "interactive" {
		planningMatch = true
		parameterMatch = true

		for idx, te := range turnExecs {
			turn := tc.Turns[idx]

			// Handle empty expected tool calls (turns where no tool is expected, e.g. mismatches)
			if len(turn.ExpectedCalls) == 0 {
				if len(te.ExecutedCalls) == 0 {
					// Perfect match: expected no tool calls, and got none!
					continue
				} else {
					// Failure: expected no tool calls, but the model executed one or more tools!
					planningMatch = false
					parameterMatch = false
					continue
				}
			}

			// Perform sequence-agnostic bipartite greedy matching for this turn
			matchedActual := make(map[int]bool)
			matchedExpected := make(map[int]bool)

			// Pass 1: Match tool name AND parameters
			for i, ec := range turn.ExpectedCalls {
				for j, ac := range te.ExecutedCalls {
					if matchedActual[j] {
						continue
					}

					cleanAcName := ac.ToolName
					if idx := strings.Index(cleanAcName, "."); idx != -1 {
						cleanAcName = cleanAcName[idx+1:]
					}
					cleanEcName := ec.ToolName
					if idx := strings.Index(cleanEcName, "."); idx != -1 {
						cleanEcName = cleanEcName[idx+1:]
					}

					if cleanAcName == cleanEcName || ac.ToolName == ec.ToolName {
						// Check if parameters match under relaxation or strict comparison
						paramsMatch := matcher.MatchParameters(ac.ToolName, te.UserMessage, ec.Args, ac.Args, matcher.DefaultRelaxationPolicy())

						if paramsMatch {
							matchedActual[j] = true
							matchedExpected[i] = true

							// Check fuzzy match usage for metadata
							for k, expectedVal := range ec.Args {
								actualVal, exists := ac.Args[k]
								if exists {
									if !reflect.DeepEqual(expectedVal, actualVal) {
										expNum, expIsNum := matcher.ToFloat64(expectedVal)
										actNum, actIsNum := matcher.ToFloat64(actualVal)
										if !(expIsNum && actIsNum && expNum == actNum) {
											fuzzyMatchUsed = true
										}
									}
								}
							}
							break
						}
					}
				}
			}

			// Pass 2: Match remaining by planning (tool name only)
			for i, ec := range turn.ExpectedCalls {
				if matchedExpected[i] {
					continue
				}
				for j, ac := range te.ExecutedCalls {
					if matchedActual[j] {
						continue
					}

					cleanAcName := ac.ToolName
					if idx := strings.Index(cleanAcName, "."); idx != -1 {
						cleanAcName = cleanAcName[idx+1:]
					}
					cleanEcName := ec.ToolName
					if idx := strings.Index(cleanEcName, "."); idx != -1 {
						cleanEcName = cleanEcName[idx+1:]
					}

					if cleanAcName == cleanEcName || ac.ToolName == ec.ToolName {
						matchedActual[j] = true
						matchedExpected[i] = true
						// Planning matched but parameter failed
						parameterMatch = false
						break
					}
				}
			}

			// If any expected calls were not matched at all, then planningMatch failed
			if len(matchedExpected) < len(turn.ExpectedCalls) {
				planningMatch = false
				parameterMatch = false
			}
			// If we had more executed calls than expected, then planning/parameter failed as well
			if len(te.ExecutedCalls) != len(turn.ExpectedCalls) {
				planningMatch = false
				parameterMatch = false
			}
		}
	} else {
		// Consolidated mode sequence-agnostic bipartite greedy matching
		type ExpectedCall struct {
			ToolName    string
			Args        map[string]interface{}
			UserMessage string
		}
		var expected []ExpectedCall
		for _, turn := range tc.Turns {
			if turn.ExpectedToolCall != "" {
				expected = append(expected, ExpectedCall{
					ToolName:    turn.ExpectedToolCall,
					Args:        turn.ExpectedArgs,
					UserMessage: turn.UserMessage,
				})
			}
		}

		matchedActual := make(map[int]bool)
		matchedExpected := make(map[int]bool)
		parameterMatchCount := 0
		planningMatchCount := 0

		// Pass 1: Perfect & Relaxed matches
		for i, ac := range actualCalls {
			for j, ec := range expected {
				if matchedExpected[j] {
					continue
				}
				if ac.ToolName == ec.ToolName {
					// Check parameter match under relaxation or strict comparison
					paramsMatch := matcher.MatchParameters(ac.ToolName, ec.UserMessage, ec.Args, ac.Args, matcher.DefaultRelaxationPolicy())

					if paramsMatch {
						matchedActual[i] = true
						matchedExpected[j] = true
						planningMatchCount++
						parameterMatchCount++

						// Record fuzzy match usage if any parameter was relaxed
						for k, expectedVal := range ec.Args {
							actualVal, exists := ac.Args[k]
							if exists {
								if !reflect.DeepEqual(expectedVal, actualVal) {
									expNum, expIsNum := matcher.ToFloat64(expectedVal)
									actNum, actIsNum := matcher.ToFloat64(actualVal)
									if !(expIsNum && actIsNum && expNum == actNum) {
										fuzzyMatchUsed = true
									}
								}
							}
						}
						break
					}
				}
			}
		}

		// Pass 2: Planning matches only
		for i, ac := range actualCalls {
			if matchedActual[i] {
				continue
			}
			for j, ec := range expected {
				if matchedExpected[j] {
					continue
				}
				if ac.ToolName == ec.ToolName {
					matchedActual[i] = true
					matchedExpected[j] = true
					planningMatchCount++
					break
				}
			}
		}

		// Verify multiset size equivalence as well
		planningMatch = (planningMatchCount == len(expected)) && (len(actualCalls) == len(expected))
		parameterMatch = (parameterMatchCount == len(expected)) && (len(actualCalls) == len(expected))
	}

	passed := planningMatch && parameterMatch

	return BenchmarkResult{
		TestCaseID:          tc.ID,
		Dataset:             tc.Dataset,
		Passed:              passed,
		PlanningMatch:       planningMatch,
		ParameterMatch:      parameterMatch,
		FuzzyMatchUsed:      fuzzyMatchUsed,
		ExecutedToolCalls:   actualNames,
		ExecutionDurationMs: durationMs,
	}, nil
}

// Legacy parameter matching helpers were extracted to the matcher package.

// StratifiedSample selects a balanced subset of test cases across categories.
func StratifiedSample(testCases []BenchmarkTestCase, limit int) []BenchmarkTestCase {
	// 1. Group test cases by category using ID naming conventions
	categories := make(map[string][]BenchmarkTestCase)
	for _, tc := range testCases {
		var cat string
		id := tc.ID
		if strings.HasPrefix(id, "simple_") || strings.HasPrefix(id, "live_simple_") {
			cat = "simple"
		} else if strings.HasPrefix(id, "parallel_multiple_") || strings.HasPrefix(id, "live_parallel_multiple_") {
			cat = "parallel_multiple"
		} else if strings.HasPrefix(id, "parallel_") || strings.HasPrefix(id, "live_parallel_") {
			cat = "parallel"
		} else if strings.HasPrefix(id, "multiple_") || strings.HasPrefix(id, "live_multiple_") {
			cat = "multiple"
		} else if strings.HasPrefix(id, "multi_turn_") {
			cat = "multi_turn"
		} else {
			cat = "other"
		}
		categories[cat] = append(categories[cat], tc)
	}

	// 2. Sort keys to ensure deterministic quota allocation
	var keys []string
	for k := range categories {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// 3. Shuffle each category deterministically using seed 42
	r := rand.New(rand.NewSource(42))
	for _, cat := range keys {
		list := categories[cat]
		r.Shuffle(len(list), func(i, j int) {
			list[i], list[j] = list[j], list[i]
		})
	}

	// 4. Distribute limit evenly across available categories
	numCats := len(keys)
	if numCats == 0 {
		return nil
	}
	baseQuota := limit / numCats
	remainder := limit % numCats

	quotas := make(map[string]int)
	for _, cat := range keys {
		quotas[cat] = baseQuota
	}
	for i := 0; i < remainder; i++ {
		quotas[keys[i]]++
	}

	// 5. Slice and extract items up to allocated category quotas
	var selected []BenchmarkTestCase
	for _, cat := range keys {
		quota := quotas[cat]
		list := categories[cat]
		if quota > len(list) {
			quota = len(list)
		}
		selected = append(selected, list[:quota]...)
	}

	// 6. Final mix shuffle so execution category order is blended
	r.Shuffle(len(selected), func(i, j int) {
		selected[i], selected[j] = selected[j], selected[i]
	})

	return selected
}
