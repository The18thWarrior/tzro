package signaldensity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBenchmark_OfflineIntegration(t *testing.T) {
	// Mock LLM server providing responses satisfying ground-truth
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		prompt := ""
		if len(req.Messages) > 0 {
			prompt = req.Messages[0].Content
		}

		// Token count simulation: raw prompts are longer than tzro prompts
		promptTokens := len(prompt) / 4
		if promptTokens < 10 {
			promptTokens = 10
		}

		// Provide a helpful response containing expected keywords based on prompt
		var answer string
		switch {
		case strings.Contains(prompt, "Driver") && strings.Contains(prompt, "interface"):
			answer = "The Driver interface defines Get(key string) ([]byte, error), Set(key string, val []byte, ttl time.Duration) error, and Delete(key string) error."
		case strings.Contains(prompt, "UserService.createUser"):
			answer = "It invokes insert on userRepo and throws ErrUserExists if user exists."
		case strings.Contains(prompt, "NewConfigBuilder"):
			answer = "NewConfigBuilder returns *ConfigBuilder, and DefaultOptions returns 30 * time.Second timeout."
		case strings.Contains(prompt, "EventDispatcher"):
			answer = "EventDispatcher takes T extends EventPayload and uses dispatch method."
		case strings.Contains(prompt, "AuthMiddleware"):
			answer = "Context key is ContextKeyClaims and status is http.StatusUnauthorized."
		case strings.Contains(prompt, "bucket_test.go") || strings.Contains(prompt, "TestTokenBucket"):
			answer = "Offending file is bucket_test.go at line 84 with error: expected bucket tokens 10, got 5."
		case strings.Contains(prompt, "Unhandled Rejection") || strings.Contains(prompt, "Database connection refused"):
			answer = "Error is Database connection refused at src/db/client.ts line 42."
		case strings.Contains(prompt, "pytest") || strings.Contains(prompt, "test_get_user"):
			answer = "Exception is AssertionError in test_users.py at line 67."
		case strings.Contains(prompt, "panic") || strings.Contains(prompt, "SIGSEGV"):
			answer = "Panic caused by nil pointer dereference at route.go line 118."
		case strings.Contains(prompt, "TestAuth_ExpiredToken"):
			answer = "TestAuth_ExpiredToken failed with expected status 401, got 200."
		case strings.Contains(prompt, "us-east-1a") && strings.Contains(prompt, "RUNNING"):
			answer = "3"
		case strings.Contains(prompt, "highest total_spend"):
			answer = "User usr_0033 has 9850 total spend."
		case strings.Contains(prompt, "c8f12a4"):
			answer = "Committed by Alice Walker with message: Fix buffer overflow in packet parser."
		case strings.Contains(prompt, "status 'delivered' and price greater than 100"):
			answer = "Matching orders: ord_014 && ord_028."
		case strings.Contains(prompt, "dev_ops_admin"):
			answer = "Role is cluster-admin and can delete is true."
		case strings.Contains(prompt, "Marketing"):
			answer = "50000"
		case strings.Contains(prompt, "checkout"):
			answer = "35"
		case strings.Contains(prompt, "Electronics"):
			answer = "600"
		case strings.Contains(prompt, "Engineering"):
			answer = "125000"
		case strings.Contains(prompt, "churn"):
			answer = "40"
		default:
			answer = "Generic test completion"
		}

		resp := chatResponse{
			Choices: []chatChoice{
				{
					Message: chatMessage{
						Role:    "assistant",
						Content: answer,
					},
				},
			},
			Usage: chatUsage{
				PromptTokens:     promptTokens,
				CompletionTokens: 30,
				Cost:             0.0001,
			},
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := BenchmarkConfig{
		Model:     "anthropic/claude-3.5-sonnet",
		Tier:      TierMicro,
		Primitive: PrimitiveAll,
		MaxCost:   2.00,
		BaseURL:   server.URL,
		NoCache:   true,
	}

	runner := NewRunner(cfg)
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(report.Batteries) != 4 {
		t.Fatalf("expected 4 micro batteries, got %d", len(report.Batteries))
	}

	// Verify terminal report rendering
	termOutput := RenderTerminalReport(report, 2.00)
	if !strings.Contains(termOutput, "TZRO SIGNAL DENSITY BENCHMARK REPORT") {
		t.Errorf("expected title in report")
	}
	if !strings.Contains(termOutput, "COMPOSITE SCORE") {
		t.Errorf("expected COMPOSITE SCORE in report")
	}
	if !strings.Contains(termOutput, "AST Skeletonization") {
		t.Errorf("expected AST Skeletonization in table")
	}

	// Verify JSON export
	tmpDir := t.TempDir()
	outPath := filepath.Join(tmpDir, "sdm_test.json")
	savedPath, err := SaveJSONReport(report, outPath)
	if err != nil {
		t.Fatalf("failed to save JSON report: %v", err)
	}

	data, err := os.ReadFile(savedPath)
	if err != nil {
		t.Fatalf("failed to read saved report: %v", err)
	}

	var parsed BenchmarkReport
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal saved report: %v", err)
	}

	if parsed.Schema != "https://tzro.dev/schemas/signal-density-benchmark-v1.json" {
		t.Errorf("schema mismatch: %s", parsed.Schema)
	}
	if parsed.Metadata.Model != "anthropic/claude-3.5-sonnet" {
		t.Errorf("model mismatch: %s", parsed.Metadata.Model)
	}
	if parsed.Summary.RawTokens <= 0 || parsed.Summary.TzroTokens <= 0 {
		t.Errorf("expected positive token counts in summary")
	}
}

func TestBenchmark_MacroIntegration(t *testing.T) {
	// Mock server that returns valid Go code for macro task A
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		prompt := ""
		for _, msg := range req.Messages {
			prompt += msg.Content + "\n"
		}

		var answer string
		switch {
		case strings.Contains(prompt, "memory.go"):
			answer = "```go\n// file: memory.go\npackage cache\n\ntype MemoryDriver struct {\n\titems map[string]string\n}\n\nfunc NewMemoryDriver() Driver {\n\treturn &MemoryDriver{items: make(map[string]string)}\n}\n\nfunc (m *MemoryDriver) Get(key string) (string, error) {\n\tv, ok := m.items[key]\n\tif !ok { return \"\", ErrNotFound }\n\treturn v, nil\n}\n\nfunc (m *MemoryDriver) Set(key, value string) error {\n\tm.items[key] = value\n\treturn nil\n}\n\nfunc (m *MemoryDriver) Delete(key string) error {\n\tdelete(m.items, key)\n\treturn nil\n}\n```"
		case strings.Contains(prompt, "limiter.go"):
			answer = "```go\n// file: limiter.go\npackage rate\n\ntype Limiter struct {\n\ttokens int\n\tburst  int\n}\n\nfunc NewLimiter(burst int) *Limiter {\n\treturn &Limiter{burst: burst, tokens: burst}\n}\n\nfunc (l *Limiter) Allow() bool {\n\tif l.tokens <= 0 {\n\t\treturn false\n\t}\n\tl.tokens--\n\treturn true\n}\n```"
		case strings.Contains(prompt, "item.go"):
			answer = "```go\n// file: item.go\npackage model\n\ntype Item struct {\n\tID    string\n\tTitle string\n\tTags  []string\n}\n\nfunc NewItem(id, title string) *Item {\n\treturn &Item{ID: id, Title: title}\n}\n```"
		case strings.Contains(prompt, "claims.go"):
			answer = "```go\n// file: claims.go\npackage auth\n\ntype Claims struct {\n\tUser  string\n\tRole  string\n\tValid bool\n}\n\nfunc ValidateClaims(c *Claims) bool {\n\treturn c.Valid\n}\n```"
		default:
			answer = "noop"
		}

		resp := chatResponse{
			Choices: []chatChoice{
				{
					Message: chatMessage{
						Role:    "assistant",
						Content: answer,
					},
				},
			},
			Usage: chatUsage{
				PromptTokens:     100,
				CompletionTokens: 50,
				Cost:             0.001,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := BenchmarkConfig{
		Model:     "openai/gpt-4o",
		Tier:      TierMacro,
		Primitive: PrimitiveAll,
		MaxCost:   2.00,
		BaseURL:   server.URL,
		NoCache:   true,
	}

	runner := NewRunner(cfg)
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(report.Batteries) != 1 {
		t.Fatalf("expected 1 macro battery, got %d", len(report.Batteries))
	}
	if report.Batteries[0].Name != BatteryMiniMacro {
		t.Errorf("expected mini_macro battery, got %s", report.Batteries[0].Name)
	}
	if report.Batteries[0].AccuracyRaw != 1.0 || report.Batteries[0].AccuracyTzro != 1.0 {
		t.Errorf("expected 100%% accuracy for macro, got raw=%f tzro=%f",
			report.Batteries[0].AccuracyRaw, report.Batteries[0].AccuracyTzro)
	}

	table := RenderTerminalReport(report, 2.00)
	if !strings.Contains(table, "Mini-Macro Coding") {
		t.Errorf("expected Mini-Macro Coding in table output:\n%s", table)
	}
}

func TestBenchmark_ASTInterfaceExtraction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		lastMsg := req.Messages[len(req.Messages)-1].Content

		var answer string
		switch {
		case strings.Contains(lastMsg, "UserRepository"):
			answer = "The UserRepository defines findById, insert, and delete. User has id, email, name."
		case strings.Contains(lastMsg, "ConfigBuilder"):
			answer = "ConfigBuilder defines WithTimeout. Options struct has Timeout and Retries."
		case strings.Contains(lastMsg, "EventDispatcher"):
			answer = "EventDispatcher<T extends EventPayload> uses dispatch to publish."
		case strings.Contains(lastMsg, "ContextKeyClaims"):
			answer = "ContextKeyClaims is user_claims. Claims struct has Subject and Role. AuthMiddleware returns http.Handler."
		default:
			answer = "Get && Set && Delete"
		}

		resp := chatResponse{
			Choices: []chatChoice{
				{
					Message: chatMessage{
						Role:    "assistant",
						Content: answer,
					},
				},
			},
			Usage: chatUsage{
				PromptTokens:     100,
				CompletionTokens: 30,
				Cost:             0.0005,
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := BenchmarkConfig{
		Model:     "openai/gpt-4o",
		Tier:      TierMicro,
		Primitive: PrimitiveSkeleton,
		MaxCost:   2.00,
		BaseURL:   server.URL,
		NoCache:   true,
	}

	runner := NewRunner(cfg)
	report, err := runner.Run(context.Background())
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if len(report.Batteries) != 1 {
		t.Fatalf("expected 1 battery, got %d", len(report.Batteries))
	}
	b := report.Batteries[0]
	if b.AccuracyTzro != 1.0 {
		t.Errorf("expected 100%% accuracy for AST interface extraction, got %f", b.AccuracyTzro)
	}
}

func TestPiCoderAgentLoop_ToolCalling(t *testing.T) {
	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		var req chatRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		var resp chatResponse
		resp.Usage = chatUsage{PromptTokens: 50, CompletionTokens: 20, Cost: 0.0001}

		switch step {
		case 1:
			// Turn 1: Call tzro_skeleton
			resp.Choices = []chatChoice{
				{
					Message: chatMessage{
						Role: "assistant",
						ToolCalls: []chatToolCall{
							{
								ID:   "call_1",
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "tzro_skeleton",
									Arguments: `{"file":"driver.go"}`,
								},
							},
						},
					},
				},
			}
		case 2:
			// Turn 2: Call write_file
			code := `package cache

type MemoryDriver struct {
	items map[string]string
}

func NewMemoryDriver() Driver {
	return &MemoryDriver{items: make(map[string]string)}
}

func (m *MemoryDriver) Get(key string) (string, error) {
	v, ok := m.items[key]
	if !ok { return "", ErrNotFound }
	return v, nil
}

func (m *MemoryDriver) Set(key, value string) error {
	m.items[key] = value
	return nil
}

func (m *MemoryDriver) Delete(key string) error {
	delete(m.items, key)
	return nil
}
`
			argsJSON, _ := json.Marshal(map[string]string{
				"path":    "memory.go",
				"content": code,
			})
			resp.Choices = []chatChoice{
				{
					Message: chatMessage{
						Role: "assistant",
						ToolCalls: []chatToolCall{
							{
								ID:   "call_2",
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "write_file",
									Arguments: string(argsJSON),
								},
							},
						},
					},
				},
			}
		case 3:
			// Turn 3: Call run_command
			resp.Choices = []chatChoice{
				{
					Message: chatMessage{
						Role: "assistant",
						ToolCalls: []chatToolCall{
							{
								ID:   "call_3",
								Type: "function",
								Function: struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								}{
									Name:      "run_command",
									Arguments: `{"command":"go test ./..."}`,
								},
							},
						},
					},
				},
			}
		default:
			// Turn 4: Confirmation
			resp.Choices = []chatChoice{
				{
					Message: chatMessage{
						Role:    "assistant",
						Content: "All tests pass successfully.",
					},
				},
			}
		}

		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cases, err := loadMiniMacroCases(nil)
	if err != nil {
		t.Fatalf("loadMiniMacroCases: %v", err)
	}

	taskA := cases[0] // macro_1_cache_impl
	client := NewClient(server.URL, "mock-key", 10*time.Second, true)

	res, err := runPiCoderAgentTask(context.Background(), client, "mock-model", taskA, FallbackPricing, nil, true)
	if err != nil {
		t.Fatalf("runPiCoderAgentTask error: %v", err)
	}

	if !res.Passed {
		t.Errorf("expected agent task to pass, got false (error: %s)", res.Error)
	}
	if res.Turns < 2 {
		t.Errorf("expected at least 2 turns, got %d", res.Turns)
	}
}
