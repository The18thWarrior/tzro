//go:build integration

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"tzro/pkg/kvlock"
	"tzro/pkg/store"
)

// ---------------------------------------------------------------------------
// Per-turn and per-sweep data structures
// ---------------------------------------------------------------------------

// TurnMetric captures cache metrics for a single conversational turn.
type TurnMetric struct {
	Turn             int     `json:"turn"`
	PromptTokens     int     `json:"prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	CacheDiscountUSD float64 `json:"cache_discount_usd"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"` // cached_tokens / prompt_tokens
	PrefixHash       string  `json:"prefix_hash"`
}

// SweepMetric captures cache metrics for a single token-size step.
type SweepMetric struct {
	TargetTokens     int     `json:"target_tokens"`
	ActualPromptToks int     `json:"actual_prompt_tokens"`
	CachedTokens     int     `json:"cached_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	CacheHitRatio    float64 `json:"cache_hit_ratio"`
	CostUSD          float64 `json:"cost_usd"`
	PrefixHash       string  `json:"prefix_hash"`
	Status           string  `json:"status"` // "cached", "CLIFF", "error", "context_overflow"
	Error            string  `json:"error,omitempty"`
}

// BenchmarkResults is the combined output for both phases.
type BenchmarkResults struct {
	Model           string        `json:"model"`
	Timestamp       string        `json:"timestamp"`
	Phase1Turns     []TurnMetric  `json:"phase1_incremental"`
	Phase1Summary   PhaseSummary  `json:"phase1_summary"`
	Phase2Steps     []SweepMetric `json:"phase2_sweep"`
	Phase2Summary   PhaseSummary  `json:"phase2_summary"`
	TotalCostUSD    float64       `json:"total_cost_usd"`
	CostLimitUSD    float64       `json:"cost_limit_usd"`
}

// PhaseSummary holds aggregate stats for a phase.
type PhaseSummary struct {
	AvgCacheHitRatio float64 `json:"avg_cache_hit_ratio"`
	MaxCacheHitRatio float64 `json:"max_cache_hit_ratio"`
	MinCacheHitRatio float64 `json:"min_cache_hit_ratio"`
	PrefixHashStable bool    `json:"prefix_hash_stable"`
	CliffDetectedAt  string  `json:"cliff_detected_at,omitempty"` // turn or token-size where ratio dropped below 50%
}

// ---------------------------------------------------------------------------
// Cost guard
// ---------------------------------------------------------------------------

type costGuard struct {
	limit       float64
	accumulated float64
}

func newCostGuard(limit float64) *costGuard {
	return &costGuard{limit: limit}
}

func (cg *costGuard) add(cost float64) error {
	cg.accumulated += cost
	if cg.accumulated > cg.limit {
		return fmt.Errorf("cost guard tripped: $%.4f spent exceeds $%.2f limit", cg.accumulated, cg.limit)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared tools and system prompt (constant across both phases)
// ---------------------------------------------------------------------------

func benchTools() []orTool {
	return []orTool{
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
}

const benchSystemPrompt = `You are a coding agent. You have three tools: list_dir, read_file, run_command.

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// computePrefixHash uses kvlock to compute the prefix hash for a request body,
// exactly as the proxy would. This lets the test verify prefix stability.
func computePrefixHash(reqBody []byte) string {
	guard := kvlock.NewLockGuard()
	_, hash, err := guard.NormalizeOpenAI(reqBody)
	if err != nil {
		return "error:" + err.Error()
	}
	return hash
}

// summarizePhase computes aggregate stats from a slice of cache hit ratios and prefix hashes.
func summarizePhase(ratios []float64, hashes []string) PhaseSummary {
	if len(ratios) == 0 {
		return PhaseSummary{}
	}

	sum := 0.0
	minR := math.MaxFloat64
	maxR := -1.0
	for _, r := range ratios {
		sum += r
		if r < minR {
			minR = r
		}
		if r > maxR {
			maxR = r
		}
	}

	stable := true
	if len(hashes) > 1 {
		for i := 1; i < len(hashes); i++ {
			if hashes[i] != hashes[0] {
				stable = false
				break
			}
		}
	}

	cliff := ""
	// Look for first point where ratio drops below 50% (excluding turn 1 which is always a cold write)
	for i, r := range ratios {
		if i == 0 {
			continue // skip cold write
		}
		if r < 0.50 {
			cliff = fmt.Sprintf("index %d (ratio=%.1f%%)", i, r*100)
			break
		}
	}

	return PhaseSummary{
		AvgCacheHitRatio: sum / float64(len(ratios)),
		MinCacheHitRatio: minR,
		MaxCacheHitRatio: maxR,
		PrefixHashStable: stable,
		CliffDetectedAt:  cliff,
	}
}

// generatePaddingMessages creates deterministic user/assistant message pairs
// to inflate a conversation to approximately targetTokens total.
// Uses repeating lorem-like text (deterministic content is critical for cache hits).
func generatePaddingMessages(targetTokens int, baseTokens int) []orMessage {
	if targetTokens <= baseTokens {
		return nil
	}

	// Each token ≈ 4 characters. We need to fill (targetTokens - baseTokens) tokens.
	tokensNeeded := targetTokens - baseTokens
	// Each message pair (user + assistant) contributes roughly equal content.
	// We'll use ~200 tokens per message, so we need tokensNeeded/200 messages.
	charsPerMessage := 800 // ~200 tokens at 4 chars/token
	numMessages := (tokensNeeded * 4) / charsPerMessage
	if numMessages < 2 {
		numMessages = 2
	}

	// Deterministic padding text — must be identical across requests for cache to work.
	paddingUnit := "The quick brown fox jumps over the lazy dog. " +
		"Pack my box with five dozen liquor jugs. " +
		"How vexingly quick daft zebras jump. " +
		"The five boxing wizards jump quickly. "

	messages := make([]orMessage, 0, numMessages)
	for i := 0; i < numMessages; i++ {
		// Build content by repeating the padding unit to fill ~charsPerMessage chars
		repeats := charsPerMessage / len(paddingUnit)
		if repeats < 1 {
			repeats = 1
		}
		content := fmt.Sprintf("[padding message %d] %s", i, strings.Repeat(paddingUnit, repeats))

		if i%2 == 0 {
			messages = append(messages, orMessage{Role: "user", Content: content})
		} else {
			messages = append(messages, orMessage{Role: "assistant", Content: content})
		}
	}

	// Ensure the last message before the final user message is from assistant
	if len(messages) > 0 && messages[len(messages)-1].Role == "user" {
		messages = append(messages, orMessage{Role: "assistant", Content: "[padding acknowledged]"})
	}

	return messages
}

// ---------------------------------------------------------------------------
// Phase 1: Incremental Agent Loop
// ---------------------------------------------------------------------------

func testPhaseIncremental(t *testing.T, apiKey, model, proxyURL, workspaceDir string, cg *costGuard) []TurnMetric {
	t.Helper()

	tools := benchTools()
	messages := []orMessage{
		{Role: "system", Content: benchSystemPrompt},
		{Role: "user", Content: "Please explore and diagnose this codebase. One tool call per turn."},
	}

	client := &http.Client{Timeout: 60 * time.Second}
	var metrics []TurnMetric
	const maxTurns = 20

	for turn := 0; turn < maxTurns; turn++ {
		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, err := json.Marshal(reqBody)
		if err != nil {
			t.Fatalf("phase1 turn %d: marshal: %v", turn, err)
		}

		// Compute prefix hash client-side (same normalization the proxy applies)
		prefixHash := computePrefixHash(reqJSON)

		endpoint := proxyURL + "/v1/chat/completions"
		req, _ := http.NewRequest("POST", endpoint, bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-kvcache-benchmark")

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("phase1 turn %d: request failed (ending): %v", turn, err)
			break
		}

		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("phase1 turn %d: API returned %d (ending): %s", turn, resp.StatusCode,
				string(respBody[:min(len(respBody), 300)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("phase1 turn %d: unparseable response (ending)", turn)
			break
		}

		// Compute per-turn cache metrics
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

		tm := TurnMetric{
			Turn:             turn + 1,
			PromptTokens:     orResp.Usage.PromptTokens,
			CachedTokens:     cachedToks,
			CacheWriteTokens: cacheWriteToks,
			CompletionTokens: orResp.Usage.CompletionTokens,
			CostUSD:          orResp.Usage.Cost,
			CacheDiscountUSD: orResp.Usage.CacheDiscount,
			CacheHitRatio:    hitRatio,
			PrefixHash:       prefixHash,
		}
		metrics = append(metrics, tm)

		// Cost guard
		if err := cg.add(orResp.Usage.Cost); err != nil {
			t.Logf("phase1 turn %d: %v", turn, err)
			break
		}

		t.Logf("phase1 turn %2d: prompt=%5d cached=%5d writes=%5d ratio=%5.1f%% hash=%s cost=$%.6f",
			turn+1, orResp.Usage.PromptTokens, cachedToks, cacheWriteToks, hitRatio*100, prefixHash, orResp.Usage.Cost)

		if len(orResp.Choices) == 0 {
			t.Logf("phase1 turn %d: no choices (ending)", turn)
			break
		}

		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		// If the model stopped generating (no tool calls), we're done
		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			break
		}

		// Execute tool calls and append results
		for _, tc := range assistantMsg.ToolCalls {
			rawOutput, err := executeLocalTool(workspaceDir, tc.Function.Name, tc.Function.Arguments)
			if err != nil {
				rawOutput = fmt.Sprintf("tool error: %v", err)
			}
			messages = append(messages, orMessage{
				Role: "tool", Content: rawOutput, ToolCallID: tc.ID,
			})
		}
	}

	return metrics
}

// ---------------------------------------------------------------------------
// Phase 2: Controlled Token-Size Sweep
// ---------------------------------------------------------------------------

func testPhaseSweep(t *testing.T, apiKey, model, proxyURL string, cg *costGuard) []SweepMetric {
	t.Helper()

	tools := benchTools()
	targetSizes := []int{1000, 5000, 10000, 25000, 50000, 100000}
	client := &http.Client{Timeout: 120 * time.Second}
	var results []SweepMetric

	// Estimate base token count (system + tools + 1 user message ≈ 400 tokens)
	baseTokens := 400

	for _, target := range targetSizes {
		t.Logf("phase2: sweep at %d target tokens...", target)

		paddingMsgs := generatePaddingMessages(target, baseTokens)

		// Build the conversation: system + padding + final user message
		messages := []orMessage{
			{Role: "system", Content: benchSystemPrompt},
		}
		messages = append(messages, paddingMsgs...)
		messages = append(messages, orMessage{
			Role: "user", Content: "Briefly acknowledge you're ready to help. One sentence only.",
		})

		// --- Request A: Cache Write ---
		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, err := json.Marshal(reqBody)
		if err != nil {
			t.Logf("phase2 sweep %d: marshal error: %v", target, err)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error", Error: err.Error(),
			})
			continue
		}

		prefixHash := computePrefixHash(reqJSON)

		req, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-kvcache-benchmark-sweep")

		resp, err := client.Do(req)
		if err != nil {
			t.Logf("phase2 sweep %d write: request failed: %v", target, err)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error", Error: err.Error(),
			})
			continue
		}
		writeBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 400 || resp.StatusCode == 413 || resp.StatusCode == 429 {
			t.Logf("phase2 sweep %d: context overflow or rate limit (%d), stopping sweep",
				target, resp.StatusCode)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "context_overflow",
				Error: fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(writeBody[:min(len(writeBody), 200)])),
			})
			break
		}

		if resp.StatusCode != 200 {
			t.Logf("phase2 sweep %d write: API returned %d: %s", target, resp.StatusCode,
				string(writeBody[:min(len(writeBody), 200)]))
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error",
				Error: fmt.Sprintf("HTTP %d", resp.StatusCode),
			})
			continue
		}

		var writeResp orResponse
		if err := json.Unmarshal(writeBody, &writeResp); err != nil {
			t.Logf("phase2 sweep %d write: unparseable: %v", target, err)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error", Error: err.Error(),
			})
			continue
		}

		if err := cg.add(writeResp.Usage.Cost); err != nil {
			t.Logf("phase2 sweep %d: %v — stopping sweep", target, err)
			break
		}

		t.Logf("phase2 sweep %d write: prompt=%d cost=$%.6f (cache primed)",
			target, writeResp.Usage.PromptTokens, writeResp.Usage.Cost)

		// --- Wait for cache to commit ---
		time.Sleep(3 * time.Second)

		// --- Request B: Cache Read ---
		// Append the assistant's response and a new user message to the same conversation
		if len(writeResp.Choices) > 0 {
			messages = append(messages, writeResp.Choices[0].Message)
		}
		messages = append(messages, orMessage{
			Role: "user", Content: "Thanks. What's 2+2?",
		})

		readReqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		readJSON, _ := json.Marshal(readReqBody)

		readReq, _ := http.NewRequest("POST", proxyURL+"/v1/chat/completions", bytes.NewReader(readJSON))
		readReq.Header.Set("Content-Type", "application/json")
		readReq.Header.Set("Authorization", "Bearer "+apiKey)
		readReq.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		readReq.Header.Set("X-Title", "tzro-kvcache-benchmark-sweep")

		readResp, err := client.Do(readReq)
		if err != nil {
			t.Logf("phase2 sweep %d read: request failed: %v", target, err)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error", Error: err.Error(), PrefixHash: prefixHash,
			})
			continue
		}
		readRespBody, _ := io.ReadAll(readResp.Body)
		readResp.Body.Close()

		if readResp.StatusCode != 200 {
			t.Logf("phase2 sweep %d read: API returned %d", target, readResp.StatusCode)
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error",
				Error:      fmt.Sprintf("HTTP %d", readResp.StatusCode),
				PrefixHash: prefixHash,
			})
			continue
		}

		var readOR orResponse
		if err := json.Unmarshal(readRespBody, &readOR); err != nil {
			results = append(results, SweepMetric{
				TargetTokens: target, Status: "error", Error: err.Error(), PrefixHash: prefixHash,
			})
			continue
		}

		if err := cg.add(readOR.Usage.Cost); err != nil {
			t.Logf("phase2 sweep %d: %v — stopping sweep", target, err)
			break
		}

		cachedToks := 0
		cacheWriteToks := 0
		if readOR.Usage.PromptTokensDetails != nil {
			cachedToks = readOR.Usage.PromptTokensDetails.CachedTokens
			cacheWriteToks = readOR.Usage.PromptTokensDetails.CacheWriteTokens
		}

		hitRatio := 0.0
		if readOR.Usage.PromptTokens > 0 {
			hitRatio = float64(cachedToks) / float64(readOR.Usage.PromptTokens)
		}

		status := "cached"
		if hitRatio < 0.50 {
			status = "CLIFF"
		}

		sm := SweepMetric{
			TargetTokens:     target,
			ActualPromptToks: readOR.Usage.PromptTokens,
			CachedTokens:     cachedToks,
			CacheWriteTokens: cacheWriteToks,
			CacheHitRatio:    hitRatio,
			CostUSD:          readOR.Usage.Cost,
			PrefixHash:       prefixHash,
			Status:           status,
		}
		results = append(results, sm)

		t.Logf("phase2 sweep %6d: prompt=%6d cached=%6d ratio=%5.1f%% → %s",
			target, readOR.Usage.PromptTokens, cachedToks, hitRatio*100, status)

		if status == "CLIFF" {
			t.Logf("phase2: cliff detected at %d tokens, stopping sweep", target)
			break
		}
	}

	return results
}

// ---------------------------------------------------------------------------
// ASCII table printers
// ---------------------------------------------------------------------------

func printPhase1Table(t *testing.T, metrics []TurnMetric) {
	t.Helper()
	if len(metrics) == 0 {
		t.Log("Phase 1: no data collected")
		return
	}

	t.Logf("\n" +
		"┌───────┬────────────┬────────────┬──────────────┬────────────┬──────────────┬──────────────┐\n" +
		"│ Turn  │ Prompt Tok │ Cached Tok │ Cache Writes │ Hit Ratio  │   Cost ($)   │ Prefix Hash  │\n" +
		"├───────┼────────────┼────────────┼──────────────┼────────────┼──────────────┼──────────────┤")

	for _, m := range metrics {
		t.Logf("│ %5d │ %10d │ %10d │ %12d │ %8.1f%%  │ %12.6f │ %12s │",
			m.Turn, m.PromptTokens, m.CachedTokens, m.CacheWriteTokens,
			m.CacheHitRatio*100, m.CostUSD, m.PrefixHash)
	}

	// Summary row
	ratios := make([]float64, len(metrics))
	hashes := make([]string, len(metrics))
	totalCost := 0.0
	for i, m := range metrics {
		ratios[i] = m.CacheHitRatio
		hashes[i] = m.PrefixHash
		totalCost += m.CostUSD
	}
	summary := summarizePhase(ratios, hashes)

	hashStatus := "✓"
	if !summary.PrefixHashStable {
		hashStatus = "✗ UNSTABLE"
	}
	cliffStatus := "none"
	if summary.CliffDetectedAt != "" {
		cliffStatus = summary.CliffDetectedAt
	}

	t.Logf("├───────┴────────────┴────────────┴──────────────┴────────────┴──────────────┴──────────────┤")
	t.Logf("│ Phase 1 Summary                                                                          │")
	t.Logf("│   Avg cache hit ratio: %5.1f%%   Prefix hash stable: %-10s  Total cost: $%.4f       │",
		summary.AvgCacheHitRatio*100, hashStatus, totalCost)
	t.Logf("│   Cliff detected: %-60s         │", cliffStatus)
	t.Logf("└──────────────────────────────────────────────────────────────────────────────────────────────┘")
}

func printPhase2Table(t *testing.T, results []SweepMetric) {
	t.Helper()
	if len(results) == 0 {
		t.Log("Phase 2: no data collected")
		return
	}

	t.Logf("\n" +
		"┌─────────────┬────────────┬────────────┬────────────┬──────────────┬──────────────────┐\n" +
		"│ Target Size │ Prompt Tok │ Cached Tok │ Hit Ratio  │   Cost ($)   │ Status           │\n" +
		"├─────────────┼────────────┼────────────┼────────────┼──────────────┼──────────────────┤")

	for _, s := range results {
		statusStr := s.Status
		if s.Error != "" {
			statusStr = fmt.Sprintf("%s: %s", s.Status, s.Error)
			if len(statusStr) > 16 {
				statusStr = statusStr[:16]
			}
		}
		t.Logf("│ %11d │ %10d │ %10d │ %8.1f%%  │ %12.6f │ %-16s │",
			s.TargetTokens, s.ActualPromptToks, s.CachedTokens,
			s.CacheHitRatio*100, s.CostUSD, statusStr)
	}

	// Find cliff
	cliffMsg := "no cliff detected (cache held across all sizes)"
	for _, s := range results {
		if s.Status == "CLIFF" {
			cliffMsg = fmt.Sprintf("cliff at ~%d tokens (ratio=%.1f%%)", s.TargetTokens, s.CacheHitRatio*100)
			break
		}
		if s.Status == "context_overflow" {
			cliffMsg = fmt.Sprintf("context overflow at ~%d tokens (provider limit reached)", s.TargetTokens)
			break
		}
	}

	t.Logf("├─────────────┴────────────┴────────────┴────────────┴──────────────┴──────────────────┤")
	t.Logf("│ Phase 2: %-73s │", cliffMsg)
	t.Logf("└─────────────────────────────────────────────────────────────────────────────────────────┘")
}

// ---------------------------------------------------------------------------
// Main Test Entry Point
// ---------------------------------------------------------------------------

// TestKVCacheBenchmark runs a two-phase benchmark to validate KV-cache
// prefix locking effectiveness and find the cache cliff.
//
// Phase 1: Incremental agent loop — tracks per-turn cache hit ratio and prefix
// hash stability through a natural multi-turn exploration.
//
// Phase 2: Controlled token-size sweep — sends progressively larger
// conversations (1K to 100K tokens) to find where cache hits stop.
//
// Run with:
//
//	go test -tags integration -run TestKVCacheBenchmark -v -timeout 10m ./pkg/proxy/
func TestKVCacheBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping KV-cache benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping KV-cache benchmark")
	}

	// Cost guard: default $2.00, overridable via env
	costLimit := 2.0
	if v := os.Getenv("KVCACHE_BENCH_MAX_COST"); v != "" {
		if parsed, err := strconv.ParseFloat(v, 64); err == nil {
			costLimit = parsed
		}
	}
	cg := newCostGuard(costLimit)
	t.Logf("KV-cache benchmark starting (model=%s, cost_limit=$%.2f)", model, costLimit)

	// Start proxy in-process
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

	testSrv := httptest.NewServer(proxySrv.httpSrv.Handler)
	defer testSrv.Close()
	proxyURL := testSrv.URL
	t.Logf("proxy listening at: %s", proxyURL)

	// --- Phase 1: Incremental Agent Loop ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  PHASE 1: Incremental Agent Loop (per-turn tracking)")
	t.Log("═══════════════════════════════════════════════════════")

	workspace := scaffoldWorkspace(t)
	phase1Metrics := testPhaseIncremental(t, apiKey, model, proxyURL, workspace, cg)
	printPhase1Table(t, phase1Metrics)

	// --- Phase 2: Controlled Token-Size Sweep ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  PHASE 2: Controlled Token-Size Sweep (cliff finder)")
	t.Log("═══════════════════════════════════════════════════════")

	phase2Metrics := testPhaseSweep(t, apiKey, model, proxyURL, cg)
	printPhase2Table(t, phase2Metrics)

	// --- Build combined results and write JSON ---
	p1Ratios := make([]float64, len(phase1Metrics))
	p1Hashes := make([]string, len(phase1Metrics))
	for i, m := range phase1Metrics {
		p1Ratios[i] = m.CacheHitRatio
		p1Hashes[i] = m.PrefixHash
	}

	p2Ratios := make([]float64, len(phase2Metrics))
	p2Hashes := make([]string, len(phase2Metrics))
	for i, m := range phase2Metrics {
		p2Ratios[i] = m.CacheHitRatio
		p2Hashes[i] = m.PrefixHash
	}

	results := BenchmarkResults{
		Model:         model,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Phase1Turns:   phase1Metrics,
		Phase1Summary: summarizePhase(p1Ratios, p1Hashes),
		Phase2Steps:   phase2Metrics,
		Phase2Summary: summarizePhase(p2Ratios, p2Hashes),
		TotalCostUSD:  cg.accumulated,
		CostLimitUSD:  costLimit,
	}

	// Write JSON results
	resultsDir := filepath.Join("testdata")
	if err := os.MkdirAll(resultsDir, 0o755); err != nil {
		t.Logf("WARNING: could not create testdata dir: %v", err)
	} else {
		resultsJSON, _ := json.MarshalIndent(results, "", "  ")
		resultsFile := filepath.Join(resultsDir, "kvcache_benchmark_results.json")
		if err := os.WriteFile(resultsFile, resultsJSON, 0o644); err != nil {
			t.Logf("WARNING: could not write results: %v", err)
		} else {
			t.Logf("results written to: %s", resultsFile)
		}
	}

	// --- Final Verdict ---
	t.Log("\n═══════════════════════════════════════════════════════")
	t.Log("  VERDICT")
	t.Log("═══════════════════════════════════════════════════════")

	t.Logf("Total benchmark cost: $%.4f / $%.2f limit", cg.accumulated, costLimit)

	// Check the 90% claim (excluding turn 1 which is always a cold write)
	if len(phase1Metrics) > 2 {
		warmRatios := p1Ratios[1:] // skip cold write
		warmSum := 0.0
		for _, r := range warmRatios {
			warmSum += r
		}
		warmAvg := warmSum / float64(len(warmRatios))
		t.Logf("Phase 1 warm-turn avg cache hit ratio: %.1f%% (claim: ≥90%%)", warmAvg*100)
		if warmAvg >= 0.90 {
			t.Logf("✓ 90%% cache hit rate claim VALIDATED")
		} else if warmAvg >= 0.75 {
			t.Logf("⚠ Cache hit rate is %.1f%% — below 90%% claim but above 75%%", warmAvg*100)
		} else {
			t.Logf("✗ Cache hit rate is %.1f%% — BELOW 90%% claim", warmAvg*100)
		}
	}

	if results.Phase1Summary.PrefixHashStable {
		t.Log("✓ Prefix hash stable across all turns")
	} else {
		t.Log("✗ PREFIX HASH UNSTABLE — kvlock normalization is not deterministic!")
	}

	if results.Phase2Summary.CliffDetectedAt != "" {
		t.Logf("⚠ Cache cliff: %s", results.Phase2Summary.CliffDetectedAt)
	} else {
		t.Log("✓ No cache cliff detected across tested sizes")
	}
}
