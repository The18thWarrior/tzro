//go:build integration

package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tzro/pkg/kvlock"
	"tzro/pkg/proxy"
	"tzro/pkg/store"
)

// ---------------------------------------------------------------------------
// Per-turn metrics (local to this test, mirrors proxy benchmark)
// ---------------------------------------------------------------------------

type e2eTurnMetric struct {
	Turn             int     `json:"turn"`
	ToolName         string  `json:"tool_name,omitempty"`
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"`
	PrefixHash       string  `json:"prefix_hash"`
}

type e2eRunResult struct {
	Mode         string          `json:"mode"` // "direct" or "proxied"
	Model        string          `json:"model"`
	Turns        []e2eTurnMetric `json:"turns"`
	TotalCost    float64         `json:"total_cost_usd"`
	TotalPrompt  int             `json:"total_prompt_tokens"`
	TotalCached  int             `json:"total_cached_tokens"`
	AvgHitRatio  float64         `json:"avg_cache_hit_ratio"`
	WarmHitRatio float64         `json:"warm_cache_hit_ratio"` // excluding first turn
	PrefixStable bool            `json:"prefix_hash_stable"`
	WallClockMs  int64           `json:"wall_clock_ms"`
	FinalAnswer  string          `json:"-"`
}

type e2eBenchmarkResults struct {
	Model     string       `json:"model"`
	Timestamp string       `json:"timestamp"`
	Direct    e2eRunResult `json:"direct"`
	Proxied   e2eRunResult `json:"proxied"`
}

// ---------------------------------------------------------------------------
// Cost guard (same as proxy benchmark)
// ---------------------------------------------------------------------------

type e2eCostGuard struct {
	limit       float64
	accumulated float64
}

func newE2ECostGuard(limit float64) *e2eCostGuard {
	return &e2eCostGuard{limit: limit}
}

func (cg *e2eCostGuard) add(cost float64) error {
	cg.accumulated += cost
	if cg.accumulated > cg.limit {
		return fmt.Errorf("cost guard tripped: $%.4f spent exceeds $%.2f limit", cg.accumulated, cg.limit)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Compute prefix hash (same approach as proxy benchmark)
// ---------------------------------------------------------------------------

func e2eComputePrefixHash(reqBody []byte) string {
	guard := kvlock.NewLockGuard()
	_, hash, err := guard.NormalizeOpenAI(reqBody)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}

// ---------------------------------------------------------------------------
// Full tzro tools (probe + skeleton + expand + read_file + run_command)
// ---------------------------------------------------------------------------

func e2eTzroTools() []orTool {
	return []orTool{
		{Type: "function", Function: orToolFunction{
			Name:        "tzro_probe",
			Description: "Search for symbols, functions, types, or patterns across the entire codebase. Returns exact file:line locations in <500 tokens. USE THIS FIRST.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"query": map[string]string{"type": "string", "description": "Search query."},
			}, "required": []string{"query"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name:        "tzro_skeleton",
			Description: "Get a compressed overview of a source file (imports + signatures, bodies elided as hash tags). 70-90% smaller than full file.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"file": map[string]string{"type": "string", "description": "Path to source file (relative to workspace root)."},
			}, "required": []string{"file"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name:        "tzro_expand",
			Description: "Retrieve a function body by its hash from skeleton output (e.g. '// [body elided: #abc123]'). Returns only ~20 lines.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"hash": map[string]string{"type": "string", "description": "Hash from skeleton elision comment."},
			}, "required": []string{"hash"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name:        "read_file",
			Description: "Read the full contents of a file. Use for READMEs, configs, log files. Prefer tzro_skeleton for source code.",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
			}, "required": []string{"path"}},
		}},
		{Type: "function", Function: orToolFunction{
			Name:        "run_command",
			Description: "Run a shell command (go test, go build, etc.).",
			Parameters: map[string]any{"type": "object", "properties": map[string]any{
				"command": map[string]string{"type": "string", "description": "Shell command to execute."},
			}, "required": []string{"command"}},
		}},
	}
}

const e2eSystemPrompt = `You are a senior software engineer with efficient codebase exploration tools:

- tzro_probe: Search the entire codebase for symbols/patterns. USE THIS FIRST.
- tzro_skeleton: Compressed file overview (signatures only, bodies elided). 70-90% smaller.
- tzro_expand: Retrieve a specific function body by hash from skeleton output.
- read_file: Full file read (use for READMEs, configs, logs — prefer skeleton for source).
- run_command: Run shell commands (go test, go build, etc.)

IMPORTANT: ONE tool call per turn.

Task: Efficiently diagnose all issues in this codebase.
1. tzro_probe to discover structure
2. tzro_skeleton on key files
3. tzro_expand only when needed
4. read_file for READMEs and logs
5. run_command for tests/builds
6. Final diagnostic report`

// ---------------------------------------------------------------------------
// Agent loop with per-turn cache tracking
// ---------------------------------------------------------------------------

func e2eAgentLoop(t *testing.T, apiKey, model, apiBaseURL, tzroBin, workspaceDir, mode string, cg *e2eCostGuard) e2eRunResult {
	t.Helper()

	tools := e2eTzroTools()
	messages := []orMessage{
		{Role: "system", Content: e2eSystemPrompt},
		{Role: "user", Content: "Efficiently diagnose this codebase using the tzro discovery tools. One tool call per turn."},
	}

	client := &http.Client{Timeout: 90 * time.Second}
	result := e2eRunResult{Mode: mode, Model: model}
	start := time.Now()
	const maxTurns = 50

	for turn := 0; turn < maxTurns; turn++ {
		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("%s turn %d: marshal: %v", mode, turn, err)
		}

		// Compute prefix hash client-side
		prefixHash := e2eComputePrefixHash(reqJSON)

		endpoint := apiBaseURL + "/v1/chat/completions"
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-e2e-kvcache-benchmark")

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("%s turn %d: request failed (ending): %v", mode, turn, err)
			break
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("%s turn %d: API returned %d (ending): %s", mode, turn, resp.StatusCode,
				string(respBody[:min(len(respBody), 300)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("%s turn %d: unparseable response (ending)", mode, turn)
			break
		}

		// Per-turn cache metrics
		cachedToks := 0
		cacheWriteToks := 0
		if orResp.Usage.PromptTokensDetails != nil {
			cachedToks = orResp.Usage.PromptTokensDetails.CachedTokens
			cacheWriteToks = orResp.Usage.PromptTokensDetails.CacheWriteTokens
		}

		hitRatio := 0.0
		if orResp.Usage.PromptTokens > 0 {
			hitRatio = float64(cachedToks) / float64(orResp.Usage.PromptTokens)
		}

		// Determine tool name for logging
		toolName := "(final)"
		if len(orResp.Choices) > 0 && len(orResp.Choices[0].Message.ToolCalls) > 0 {
			toolName = orResp.Choices[0].Message.ToolCalls[0].Function.Name
		}

		tm := e2eTurnMetric{
			Turn:             turn + 1,
			ToolName:         toolName,
			PromptTokens:     orResp.Usage.PromptTokens,
			CachedTokens:     cachedToks,
			CacheWriteTokens: cacheWriteToks,
			CompletionTokens: orResp.Usage.CompletionTokens,
			CostUSD:          orResp.Usage.Cost,
			CacheHitRatio:    hitRatio,
			PrefixHash:       prefixHash,
		}
		result.Turns = append(result.Turns, tm)

		// Cost guard
		if err := cg.add(orResp.Usage.Cost); err != nil {
			t.Logf("%s turn %d: %v", mode, turn, err)
			break
		}

		t.Logf("%s turn %2d [%-15s]: prompt=%6d cached=%6d ratio=%5.1f%% hash=%s cost=$%.6f",
			mode, turn+1, toolName, orResp.Usage.PromptTokens, cachedToks, hitRatio*100, prefixHash, orResp.Usage.Cost)

		if len(orResp.Choices) == 0 {
			break
		}

		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			result.FinalAnswer = assistantMsg.Content
			break
		}

		// Execute tool calls with tzro + hooks
		for _, tc := range assistantMsg.ToolCalls {
			rawOutput, err := executeLocalToolWithTzro(tzroBin, workspaceDir, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				rawOutput = fmt.Sprintf("tool error: %v", err)
			}

			// Apply pi-coder hooks
			toolOutput := rawOutput
			hookInput := PiCoderPostToolInput{ToolName: tc.Function.Name, ToolOutput: rawOutput}
			hookJSON, _ := json.Marshal(hookInput)
			var hookOut bytes.Buffer
			if err := HandlePiCoderPostTool(bytes.NewReader(hookJSON), &hookOut, nil); err == nil {
				var hookResp PiCoderPostToolOutput
				if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err == nil {
					if s, ok := hookResp.ToolOutput.(string); ok {
						toolOutput = s
					}
				}
			}

			messages = append(messages, orMessage{
				Role: "tool", Content: toolOutput, ToolCallID: tc.ID,
			})
		}
	}

	result.WallClockMs = time.Since(start).Milliseconds()

	// Compute summary stats
	if len(result.Turns) > 0 {
		totalPrompt := 0
		totalCached := 0
		totalCost := 0.0
		sumRatio := 0.0
		warmSum := 0.0
		warmCount := 0
		hashes := make([]string, len(result.Turns))

		for i, tm := range result.Turns {
			totalPrompt += tm.PromptTokens
			totalCached += tm.CachedTokens
			totalCost += tm.CostUSD
			sumRatio += tm.CacheHitRatio
			hashes[i] = tm.PrefixHash
			if i > 0 { // skip cold turn
				warmSum += tm.CacheHitRatio
				warmCount++
			}
		}

		result.TotalPrompt = totalPrompt
		result.TotalCached = totalCached
		result.TotalCost = totalCost
		result.AvgHitRatio = sumRatio / float64(len(result.Turns))
		if warmCount > 0 {
			result.WarmHitRatio = warmSum / float64(warmCount)
		}

		// Check prefix stability
		result.PrefixStable = true
		for i := 1; i < len(hashes); i++ {
			if hashes[i] != hashes[0] {
				result.PrefixStable = false
				break
			}
		}
	}

	return result
}

// ---------------------------------------------------------------------------
// ASCII table printers
// ---------------------------------------------------------------------------

func printE2EPerTurnTable(t *testing.T, r e2eRunResult) {
	t.Helper()
	if len(r.Turns) == 0 {
		t.Logf("%s: no data collected", r.Mode)
		return
	}

	t.Logf("\n" +
		"┌───────┬─────────────────┬────────────┬────────────┬────────────┬──────────────┬──────────────┐\n" +
		"│ Turn  │ Tool            │ Prompt Tok │ Cached Tok │ Hit Ratio  │   Cost ($)   │ Prefix Hash  │\n" +
		"├───────┼─────────────────┼────────────┼────────────┼────────────┼──────────────┼──────────────┤")

	for _, tm := range r.Turns {
		t.Logf("│ %5d │ %-15s │ %10d │ %10d │ %8.1f%%  │ %12.6f │ %12s │",
			tm.Turn, tm.ToolName, tm.PromptTokens, tm.CachedTokens,
			tm.CacheHitRatio*100, tm.CostUSD, tm.PrefixHash)
	}

	hashStatus := "✓"
	if !r.PrefixStable {
		hashStatus = "✗ UNSTABLE"
	}

	t.Logf("├───────┴─────────────────┴────────────┴────────────┴────────────┴──────────────┴──────────────┤")
	t.Logf("│ %s: %d turns | Avg hit ratio: %.1f%% | Warm avg: %.1f%% | Prefix: %-10s | Cost: $%.4f │",
		r.Mode, len(r.Turns), r.AvgHitRatio*100, r.WarmHitRatio*100, hashStatus, r.TotalCost)
	t.Logf("└─────────────────────────────────────────────────────────────────────────────────────────────────┘")
}

func printE2EComparisonTable(t *testing.T, direct, proxied e2eRunResult) {
	t.Helper()

	promptDelta := proxied.TotalPrompt - direct.TotalPrompt
	cachedDelta := proxied.TotalCached - direct.TotalCached
	ratioDelta := proxied.WarmHitRatio - direct.WarmHitRatio
	costDelta := proxied.TotalCost - direct.TotalCost

	t.Logf("\n"+
		"┌──────────────┬───────┬────────────┬────────────┬──────────────┬────────────────┬──────────┐\n"+
		"│ Mode         │ Turns │ Prompt Tok │ Cached Tok │ Warm Hit %%   │ Total Cost ($) │ Wall (s) │\n"+
		"├──────────────┼───────┼────────────┼────────────┼──────────────┼────────────────┼──────────┤\n"+
		"│ Direct       │ %5d │ %10d │ %10d │ %10.1f%%  │ %14.6f │ %8.1f │\n"+
		"│ Proxied      │ %5d │ %10d │ %10d │ %10.1f%%  │ %14.6f │ %8.1f │\n"+
		"├──────────────┼───────┼────────────┼────────────┼──────────────┼────────────────┼──────────┤\n"+
		"│ Proxy Δ      │     — │ %+10d │ %+10d │ %+10.1f%%  │ %+14.6f │        — │\n"+
		"└──────────────┴───────┴────────────┴────────────┴──────────────┴────────────────┴──────────┘",
		len(direct.Turns), direct.TotalPrompt, direct.TotalCached, direct.WarmHitRatio*100, direct.TotalCost, float64(direct.WallClockMs)/1000,
		len(proxied.Turns), proxied.TotalPrompt, proxied.TotalCached, proxied.WarmHitRatio*100, proxied.TotalCost, float64(proxied.WallClockMs)/1000,
		promptDelta, cachedDelta, ratioDelta*100, costDelta,
	)
}

// ---------------------------------------------------------------------------
// Main Test: E2E KV-Cache Benchmark
// ---------------------------------------------------------------------------

// TestKVCacheE2E runs the full picoder engine (probe + skeleton + expand + hooks)
// in two modes — Direct (no proxy) and Proxied (through tzro kvlock) — and
// compares per-turn cache hit ratios.
//
// Run with:
//
//	KVCACHE_BENCH_MAX_COST=5.00 go test -tags integration -run TestKVCacheE2E -v -timeout 20m ./pkg/hooks/
func TestKVCacheE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E KV-cache benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping E2E KV-cache benchmark")
	}

	// Cost guard
	costLimit := 5.0
	if v := os.Getenv("KVCACHE_BENCH_MAX_COST"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			costLimit = parsed
		}
	}
	cg := newE2ECostGuard(costLimit)
	t.Logf("E2E KV-cache benchmark starting (model=%s, cost_limit=$%.2f)", model, costLimit)

	// Build tzro binary
	tzroBin := buildTzroBinary(t)
	t.Logf("tzro binary: %s", tzroBin)

	// Scaffold workspace
	workspace := scaffoldWorkspace(t)
	t.Logf("workspace: %s", workspace)

	// Pre-index workspace
	t.Log("Pre-indexing workspace with tzro skeleton...")
	_ = filepath.Walk(workspace, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") {
			cmd := exec.Command(tzroBin, "skeleton", path)
			cmd.Dir = workspace
			cmd.CombinedOutput()
		}
		return nil
	})
	t.Log("Pre-indexing complete")

	// --- Mode A: Direct (no proxy) ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  MODE A: Direct (no proxy, native provider caching)")
	t.Log("═══════════════════════════════════════════════════════")

	directResult := e2eAgentLoop(t, apiKey, model, "https://openrouter.ai/api", tzroBin, workspace, "direct", cg)
	printE2EPerTurnTable(t, directResult)

	// --- Mode B: Proxied (through tzro) ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  MODE B: Proxied (through tzro kvlock normalization)")
	t.Log("═══════════════════════════════════════════════════════")

	// Start proxy in-process
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	proxySrv := proxy.NewServer(proxy.Config{
		ListenAddr:     "127.0.0.1:0",
		UpstreamOpenAI: "https://openrouter.ai/api",
		Store:          s,
	})

	testSrv := httptest.NewServer(proxySrv.Handler())
	defer testSrv.Close()
	proxyURL := testSrv.URL
	t.Logf("proxy listening at: %s", proxyURL)

	proxiedResult := e2eAgentLoop(t, apiKey, model, proxyURL, tzroBin, workspace, "proxied", cg)
	printE2EPerTurnTable(t, proxiedResult)

	// --- Comparison ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  COMPARISON: Direct vs Proxied")
	t.Log("═══════════════════════════════════════════════════════")

	printE2EComparisonTable(t, directResult, proxiedResult)

	// --- Write JSON results ---
	results := e2eBenchmarkResults{
		Model:     model,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Direct:    directResult,
		Proxied:   proxiedResult,
	}

	resultsDir := filepath.Join("testdata")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Logf("WARNING: could not create testdata dir: %v", err)
	} else {
		resultsJSON, _ := json.MarshalIndent(results, "", "  ")
		resultsFile := filepath.Join(resultsDir, "kvcache_e2e_benchmark_results.json")
		if err := os.WriteFile(resultsFile, resultsJSON, 0o644); err != nil {
			t.Logf("WARNING: could not write results: %v", err)
		} else {
			t.Logf("results written to: %s", resultsFile)
		}
	}

	// --- Verdict ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  VERDICT")
	t.Log("═══════════════════════════════════════════════════════")

	t.Logf("Total benchmark cost: $%.4f / $%.2f limit", cg.accumulated, costLimit)

	t.Logf("Direct warm-turn avg:  %.1f%%", directResult.WarmHitRatio*100)
	t.Logf("Proxied warm-turn avg: %.1f%%", proxiedResult.WarmHitRatio*100)

	delta := proxiedResult.WarmHitRatio - directResult.WarmHitRatio
	if delta > 0 {
		t.Logf("✓ Proxy improved cache hit rate by +%.1f percentage points", delta*100)
	} else if math.Abs(delta) < 0.02 {
		t.Logf("≈ Proxy had negligible effect on cache hit rate (Δ=%.1f%%)", delta*100)
	} else {
		t.Logf("⚠ Proxy reduced cache hit rate by %.1f percentage points", -delta*100)
	}

	if directResult.PrefixStable && proxiedResult.PrefixStable {
		t.Log("✓ Prefix hash stable in both modes")
	} else {
		if !directResult.PrefixStable {
			t.Log("✗ Prefix hash UNSTABLE in direct mode")
		}
		if !proxiedResult.PrefixStable {
			t.Log("✗ Prefix hash UNSTABLE in proxied mode")
		}
	}

	if proxiedResult.TotalCost < directResult.TotalCost {
		savings := (1.0 - proxiedResult.TotalCost/directResult.TotalCost) * 100
		t.Logf("✓ Proxy saved %.1f%% on total cost ($%.4f vs $%.4f)",
			savings, proxiedResult.TotalCost, directResult.TotalCost)
	}
}
