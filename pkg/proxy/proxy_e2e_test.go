//go:build integration

package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/store"
)

// ---------------------------------------------------------------------------
// OpenRouter types with extended billing fields
// ---------------------------------------------------------------------------

type orToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type orTool struct {
	Type     string         `json:"type"`
	Function orToolFunction `json:"function"`
}

type orToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type orMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content,omitempty"`
	ToolCalls  []orToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type orRequest struct {
	Model    string      `json:"model"`
	Messages []orMessage `json:"messages"`
	Tools    []orTool    `json:"tools,omitempty"`
}

type promptTokensDetails struct {
	CachedTokens     int `json:"cached_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
	AudioTokens      int `json:"audio_tokens"`
}

type orUsage struct {
	PromptTokens        int                  `json:"prompt_tokens"`
	CompletionTokens    int                  `json:"completion_tokens"`
	TotalTokens         int                  `json:"total_tokens"`
	PromptTokensDetails *promptTokensDetails `json:"prompt_tokens_details,omitempty"`
	Cost                float64              `json:"cost,omitempty"`
	CacheDiscount       float64              `json:"cache_discount,omitempty"`
}

type orChoice struct {
	Message      orMessage `json:"message"`
	FinishReason string    `json:"finish_reason"`
}

type orResponse struct {
	Choices []orChoice `json:"choices"`
	Usage   orUsage    `json:"usage"`
}

// ---------------------------------------------------------------------------
// RunResult with billing
// ---------------------------------------------------------------------------

type RunResult struct {
	PromptTokens     int
	CompletionTokens int
	ToolOutputBytes  int
	WallClockMs      int64
	FinalAnswer      string
	Turns            int
	// Billing
	TotalCostUSD     float64
	CacheDiscountUSD float64
	CachedTokens     int
	CacheWriteTokens int
}

// ---------------------------------------------------------------------------
// Virtual workspace (smaller than hooks test — targets ~20 turns)
// ---------------------------------------------------------------------------

func scaffoldWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		"go.mod": `module acme/inventory
go 1.22.0

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/jmoiron/sqlx v1.3.5
)
`,
		"main.go": `package main

import (
	"fmt"
	"log"
	"net/http"
	"acme/inventory/internal/api"
	"acme/inventory/internal/db"
)

func main() {
	store, err := db.Connect("postgres://localhost/inventory")
	if err != nil { log.Fatalf("db: %v", err) }
	defer store.Close()
	router := api.NewRouter(store)
	fmt.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", router))
}
`,
		"README.md": `# Inventory Service

## Packages
- internal/api — HTTP handlers
- internal/db — Database layer
- internal/models — Domain types

## Known Issues
- TestUpdateStock fails (see logs/test_output.log)
- Build has type errors (see logs/build_output.log)
`,
		"internal/models/product.go": `package models

import "fmt"

type Product struct {
	ID         int64  ` + "`json:\"id\" db:\"id\"`" + `
	Name       string ` + "`json:\"name\" db:\"name\"`" + `
	SKU        string ` + "`json:\"sku\" db:\"sku\"`" + `
	PriceCents int    ` + "`json:\"price_cents\" db:\"price_cents\"`" + `
	StockQty   int    ` + "`json:\"stock_qty\" db:\"stock_qty\"`" + `
}

func (p Product) Validate() error {
	if p.Name == "" { return fmt.Errorf("name required") }
	if p.SKU == "" { return fmt.Errorf("sku required") }
	return nil
}
`,
		"internal/models/order.go": `package models

import "time"

type OrderStatus string
const (
	OrderPending  OrderStatus = "pending"
	OrderShipped  OrderStatus = "shipped"
)

type Order struct {
	ID         int64       ` + "`json:\"id\" db:\"id\"`" + `
	CustomerID int64       ` + "`json:\"customer_id\" db:\"customer_id\"`" + `
	Status     OrderStatus ` + "`json:\"status\" db:\"status\"`" + `
	TotalCents int         ` + "`json:\"total_cents\" db:\"total_cents\"`" + `
	CreatedAt  time.Time   ` + "`json:\"created_at\" db:\"created_at\"`" + `
}
`,
		"internal/db/store.go": `package db

import (
	"database/sql"
	"fmt"
	"acme/inventory/internal/models"
)

type Store struct { db *sql.DB }

func Connect(dsn string) (*Store, error) {
	conn, err := sql.Open("postgres", dsn)
	if err != nil { return nil, fmt.Errorf("db connect: %w", err) }
	return &Store{db: conn}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) ListProducts() ([]models.Product, error) {
	rows, err := s.db.Query("SELECT id, name, sku, price_cents, stock_qty FROM products ORDER BY name")
	if err != nil { return nil, err }
	defer rows.Close()
	var products []models.Product
	for rows.Next() {
		var p models.Product
		if err := rows.Scan(&p.ID, &p.Name, &p.SKU, &p.PriceCents, &p.StockQty); err != nil { return nil, err }
		products = append(products, p)
	}
	return products, rows.Err()
}

func (s *Store) UpdateStock(productID int64, delta int) error {
	_, err := s.db.Exec("UPDATE products SET stock_qty = stock_qty + $1 WHERE id = $2", delta, productID)
	return err
}
`,
		"internal/api/router.go": `package api

import (
	"net/http"
	"acme/inventory/internal/db"
)

func NewRouter(store *db.Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/products", listProductsHandler(store))
	mux.HandleFunc("PATCH /api/products/{id}/stock", updateStockHandler(store))
	mux.HandleFunc("GET /health", healthHandler)
	return mux
}
`,
		"internal/api/handlers.go": `package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"acme/inventory/internal/db"
)

func listProductsHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		products, err := store.ListProducts()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(products)
	}
}

func updateStockHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
		var body struct {
			Delta float64 ` + "`json:\"delta\"`" + ` // BUG: should be int
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		if err := store.UpdateStock(id, int(body.Delta)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
`,
		// Log files with stack traces for compactor
		"logs/test_output.log":   generateTestLog(),
		"logs/build_output.log":  generateBuildLog(),
		"logs/api_response.json": generateAPIResponseLog(),
	}

	for relPath, content := range files {
		absPath := filepath.Join(dir, relPath)
		if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(absPath), err)
		}
		if err := os.WriteFile(absPath, []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", absPath, err)
		}
	}
	return dir
}

// ---------------------------------------------------------------------------
// Log generators
// ---------------------------------------------------------------------------

func generateTestLog() string {
	var sb strings.Builder
	sb.WriteString("=== RUN   TestListProducts\n--- PASS: TestListProducts (0.02s)\n")
	sb.WriteString("=== RUN   TestUpdateStock\n--- FAIL: TestUpdateStock (0.01s)\n")
	sb.WriteString("    store_test.go:89: expected stock_qty 15, got 10\n")
	sb.WriteString("=== RUN   TestCreateProductDuplicate\n--- FAIL: TestCreateProductDuplicate (0.02s)\n")
	sb.WriteString("panic: runtime error: invalid memory address or nil pointer dereference\n")
	sb.WriteString("[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x1234567]\n\n")
	sb.WriteString("goroutine 42 [running]:\n")
	sb.WriteString("acme/inventory/internal/db.(*Store).CreateProduct(0xc0000b2000, {0xc0000fe000})\n")
	sb.WriteString("\t/app/internal/db/store.go:72 +0x1a5\n")
	for i := 0; i < 30; i++ {
		sb.WriteString(fmt.Sprintf("testing.go:%d +0x%x\n", 800+i, 0x20+i))
	}
	for i := 0; i < 25; i++ {
		sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 200+i, 0x10+i))
	}
	sb.WriteString("\nFAIL\tacme/inventory/internal/db\t0.12s\n")
	return sb.String()
}

func generateBuildLog() string {
	var sb strings.Builder
	sb.WriteString("# acme/inventory/internal/api\n")
	sb.WriteString("internal/api/handlers.go:42:42: cannot use body.Delta (variable of type float64) as int value in argument to store.UpdateStock\n")
	sb.WriteString("\n--- Stack trace ---\n")
	sb.WriteString("goroutine 1 [running]:\n")
	for i := 0; i < 15; i++ {
		sb.WriteString(fmt.Sprintf("runtime/proc.go:%d +0x%x\n", 300+i, 0x10+i))
	}
	sb.WriteString("\nexit status 2\n")
	return sb.String()
}

func generateAPIResponseLog() string {
	type product struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		SKU      string `json:"sku"`
		Price    int    `json:"price_cents"`
		StockQty int    `json:"stock_qty"`
	}
	products := make([]product, 40)
	for i := range products {
		products[i] = product{
			ID: i + 1, Name: fmt.Sprintf("Product %d", i+1),
			SKU: fmt.Sprintf("SKU-%04d", i+1), Price: (i+1)*999 + 50, StockQty: (i * 7) % 100,
		}
	}
	data, _ := json.MarshalIndent(products, "", "  ")
	return string(data)
}

// ---------------------------------------------------------------------------
// Local tool execution
// ---------------------------------------------------------------------------

func executeLocalTool(workspaceDir, toolName, argsJSON string) (string, error) {
	var args map[string]string
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", fmt.Errorf("parse tool args: %w", err)
	}

	switch toolName {
	case "list_dir":
		path := args["path"]
		if path == "" {
			path = "."
		}
		absPath := filepath.Join(workspaceDir, path)
		entries, err := os.ReadDir(absPath)
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		var sb strings.Builder
		for _, e := range entries {
			kind := "file"
			if e.IsDir() {
				kind = "dir"
			}
			info, _ := e.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			sb.WriteString(fmt.Sprintf("%s (%s, %d bytes)\n", e.Name(), kind, size))
		}
		return sb.String(), nil

	case "read_file":
		path := args["path"]
		absPath := filepath.Join(workspaceDir, path)
		data, err := os.ReadFile(absPath)
		if err != nil {
			return fmt.Sprintf("error: %v", err), nil
		}
		return string(data), nil

	case "run_command":
		cmd := args["command"]
		switch {
		case strings.Contains(cmd, "go test"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "test_output.log"))
			return string(data), nil
		case strings.Contains(cmd, "go build"):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "build_output.log"))
			return string(data), nil
		case strings.Contains(cmd, "curl") || (strings.Contains(cmd, "cat") && strings.Contains(cmd, "api_response")):
			data, _ := os.ReadFile(filepath.Join(workspaceDir, "logs", "api_response.json"))
			return string(data), nil
		default:
			return fmt.Sprintf("command executed: %s\nexit code: 0", cmd), nil
		}

	default:
		return fmt.Sprintf("unknown tool: %s", toolName), nil
	}
}

// ---------------------------------------------------------------------------
// Agent loop (reusable for direct and proxied modes)
// ---------------------------------------------------------------------------

func agentLoop(t *testing.T, apiKey, model, apiBaseURL, workspaceDir string) RunResult {
	t.Helper()

	tools := []orTool{
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "list_dir",
				Description: "List the contents of a directory in the workspace.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]string{"type": "string", "description": "Relative path from workspace root. Use '.' for root."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "read_file",
				Description: "Read the full contents of a file in the workspace.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
					},
					"required": []string{"path"},
				},
			},
		},
		{
			Type: "function",
			Function: orToolFunction{
				Name:        "run_command",
				Description: "Run a shell command. Supports: go test ./..., go build ./..., cat logs/filename.log, curl, etc.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]string{"type": "string", "description": "The shell command to execute."},
					},
					"required": []string{"command"},
				},
			},
		},
	}

	systemPrompt := `You are a coding agent. You have three tools: list_dir, read_file, run_command.

IMPORTANT: Call only ONE tool per turn. No batching.

Your task: Explore the workspace and diagnose all issues.
1. List root directory
2. Read README.md
3. List each subdirectory (internal/models, internal/db, internal/api, logs)
4. Read every source file one at a time
5. Run: go test ./...
6. Run: go build ./...
7. Read each log file in logs/
8. Output a final diagnostic report (no tool calls).

Remember: ONE tool call per turn.`

	messages := []orMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "Please explore and diagnose this codebase. One tool call per turn."},
	}

	client := &http.Client{Timeout: 60 * time.Second}
	var result RunResult
	start := time.Now()

	const maxTurns = 30

	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1

		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("turn %d: marshal: %v", turn, err)
		}

		endpoint := apiBaseURL + "/v1/chat/completions"
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-proxy-benchmark")

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("turn %d: request failed (ending run): %v", turn, err)
			break
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("turn %d: API returned %d (ending run): %s", turn, resp.StatusCode, string(respBody[:min(len(respBody), 300)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("turn %d: unparseable response (ending run, likely context overflow)", turn)
			break
		}

		// Accumulate token metrics
		result.PromptTokens += orResp.Usage.PromptTokens
		result.CompletionTokens += orResp.Usage.CompletionTokens

		// Accumulate billing metrics
		result.TotalCostUSD += orResp.Usage.Cost
		result.CacheDiscountUSD += orResp.Usage.CacheDiscount
		if orResp.Usage.PromptTokensDetails != nil {
			result.CachedTokens += orResp.Usage.PromptTokensDetails.CachedTokens
			result.CacheWriteTokens += orResp.Usage.PromptTokensDetails.CacheWriteTokens
		}

		if len(orResp.Choices) == 0 {
			t.Logf("turn %d: no choices (ending run)", turn)
			break
		}

		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			result.FinalAnswer = assistantMsg.Content
			break
		}

		// Execute tool calls
		for _, tc := range assistantMsg.ToolCalls {
			rawOutput, err := executeLocalTool(workspaceDir, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				rawOutput = fmt.Sprintf("tool error: %v", err)
			}
			result.ToolOutputBytes += len(rawOutput)
			messages = append(messages, orMessage{
				Role: "tool", Content: rawOutput, ToolCallID: tc.ID,
			})
		}

		t.Logf("turn %d: %d tool calls (cost=$%.6f, cached=%d)",
			turn, len(assistantMsg.ToolCalls), orResp.Usage.Cost,
			func() int {
				if orResp.Usage.PromptTokensDetails != nil {
					return orResp.Usage.PromptTokensDetails.CachedTokens
				}
				return 0
			}())
	}

	result.WallClockMs = time.Since(start).Milliseconds()
	return result
}

// ---------------------------------------------------------------------------
// .env loader
// ---------------------------------------------------------------------------

func loadEnv(t *testing.T) (apiKey, model string) {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		envPath := filepath.Join(dir, ".env")
		if f, err := os.Open(envPath); err == nil {
			scanner := bufio.NewScanner(f)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if k, v, ok := strings.Cut(line, "="); ok {
					switch strings.TrimSpace(k) {
					case "OPENROUTER_API_KEY":
						apiKey = strings.TrimSpace(v)
					case "OPENROUTER_MODEL":
						model = strings.TrimSpace(v)
					}
				}
			}
			f.Close()
			break
		}
		dir = filepath.Dir(dir)
	}
	if model == "" {
		model = "openai/gpt-4o-mini"
	}
	return
}

// ---------------------------------------------------------------------------
// Main E2E: Direct vs Proxied
// ---------------------------------------------------------------------------

func TestProxyE2E_DirectVsProxied(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E proxy benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping E2E proxy benchmark")
	}

	workspace := scaffoldWorkspace(t)
	t.Logf("workspace: %s", workspace)
	t.Logf("model: %s", model)

	// --- Run A: Direct to OpenRouter (no proxy) ---
	t.Log("=== Run A: Direct (no proxy) ===")
	directResult := agentLoop(t, apiKey, model, "https://openrouter.ai/api", workspace)

	// --- Run B: Through tzro proxy ---
	t.Log("=== Run B: Proxied (tzro KV-cache prefix lock) ===")

	// Start proxy in-process on a random port
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	proxySrv := NewServer(Config{
		ListenAddr:     "127.0.0.1:0",
		UpstreamOpenAI: "https://openrouter.ai/api",
		Store:          s,
	})

	// Use httptest to get a real server on a random port
	testSrv := httptest.NewServer(proxySrv.httpSrv.Handler)
	defer testSrv.Close()

	proxyURL := testSrv.URL
	t.Logf("proxy listening at: %s", proxyURL)

	proxiedResult := agentLoop(t, apiKey, model, proxyURL, workspace)

	// --- Comparison table ---
	costSavings := 0.0
	if directResult.TotalCostUSD > 0 {
		costSavings = (1.0 - proxiedResult.TotalCostUSD/directResult.TotalCostUSD) * 100
	}
	promptSavings := 0.0
	if directResult.PromptTokens > 0 {
		promptSavings = (1.0 - float64(proxiedResult.PromptTokens)/float64(directResult.PromptTokens)) * 100
	}
	timeSavings := 0.0
	if directResult.WallClockMs > 0 {
		timeSavings = (1.0 - float64(proxiedResult.WallClockMs)/float64(directResult.WallClockMs)) * 100
	}

	t.Logf("\n"+
		"┌──────────────┬────────────┬────────────┬──────────────┬──────────────┬──────────────┬──────────┬───────┐\n"+
		"│ Mode         │ Prompt Tok │ Cached Tok │ Tool Out (B) │    Cost ($)  │  Cache ($)   │ Wall (s) │ Turns │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────────┼──────────┼───────┤\n"+
		"│ Direct       │ %10d │ %10d │ %12d │ %12.6f │ %12.6f │ %8.1f │ %5d │\n"+
		"│ Proxied      │ %10d │ %10d │ %12d │ %12.6f │ %12.6f │ %8.1f │ %5d │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────────┼──────────┼───────┤\n"+
		"│ Savings      │ %9.1f%% │          — │          —   │ %11.1f%% │          —   │ %7.1f%% │     — │\n"+
		"└──────────────┴────────────┴────────────┴──────────────┴──────────────┴──────────────┴──────────┴───────┘",
		directResult.PromptTokens, directResult.CachedTokens, directResult.ToolOutputBytes, directResult.TotalCostUSD, directResult.CacheDiscountUSD, float64(directResult.WallClockMs)/1000, directResult.Turns,
		proxiedResult.PromptTokens, proxiedResult.CachedTokens, proxiedResult.ToolOutputBytes, proxiedResult.TotalCostUSD, proxiedResult.CacheDiscountUSD, float64(proxiedResult.WallClockMs)/1000, proxiedResult.Turns,
		promptSavings, costSavings, timeSavings,
	)

	// Per-turn billing detail
	t.Logf("\nBilling summary:")
	t.Logf("  Direct:  total=$%.6f  cache_discount=$%.6f  cached_tokens=%d  cache_write_tokens=%d",
		directResult.TotalCostUSD, directResult.CacheDiscountUSD, directResult.CachedTokens, directResult.CacheWriteTokens)
	t.Logf("  Proxied: total=$%.6f  cache_discount=$%.6f  cached_tokens=%d  cache_write_tokens=%d",
		proxiedResult.TotalCostUSD, proxiedResult.CacheDiscountUSD, proxiedResult.CachedTokens, proxiedResult.CacheWriteTokens)

	// Log answer lengths
	if directResult.FinalAnswer != "" {
		t.Logf("direct answer length:  %d chars", len(directResult.FinalAnswer))
	} else {
		t.Logf("WARNING: direct run did not produce final answer (%d turns)", directResult.Turns)
	}
	if proxiedResult.FinalAnswer != "" {
		t.Logf("proxied answer length: %d chars", len(proxiedResult.FinalAnswer))
	} else {
		t.Logf("WARNING: proxied run did not produce final answer (%d turns)", proxiedResult.Turns)
	}

	// Sanity: proxy should have processed requests
	t.Logf("proxy metrics: %+v", proxySrv.metrics)
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// getPort extracts the port from a net.Listener address.
func getPort(l net.Listener) int {
	return l.Addr().(*net.TCPAddr).Port
}
