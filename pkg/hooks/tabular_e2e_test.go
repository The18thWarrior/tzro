//go:build integration

package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/store"
)

// ---------------------------------------------------------------------------
// TestTabularE2E: Side-by-side comparison of Pi-Coder processing business data
//
// Run A (baseline): Agent reads the raw 681KB CSV and answers in-context
// Run B (tzro):     Agent uses tzro ingest + tzro query to answer
//
// Both are asked the same 5 aggregate questions about the NZ business finance
// survey dataset (6627 rows). We compare: token usage, cost, correctness,
// and wall-clock time.
// ---------------------------------------------------------------------------

// tabularQuestions is the prompt asking the agent to compute specific metrics.
const tabularQuestions = `You have access to a CSV file at 'static/e2e_test.csv' containing New Zealand business finance survey data.
The columns are: description, industry, level, size, line_code, value.

- "description" describes the survey question or debt type.
- "industry" is the business sector (e.g. "Manufacturing", "Construction"). The value "total" means all industries combined.
- "level" indicates industry hierarchy: 0 = top-level aggregates, 1 = sector, 2 = sub-sector.
- "size" indicates employee count bucket (e.g. "6\x9619 employees") or "total" for all sizes.
- "line_code" is a survey question code (e.g. "D0201").
- "value" is a numeric count.

Answer these 5 questions with EXACT values. Format your final answer as a numbered list:

1. How many total data rows are in the dataset? (exclude the header row)
2. How many unique line_code values are there across the entire dataset?
3. What is the single highest value in the entire dataset? (just the number)
4. What is the sum of all values where industry="total" AND level=0 AND size="total"?
5. Among level=1 industries (excluding "total"), which industry has the highest sum of values where size="total"? (give the exact industry name)

Use the tools available to read and analyze the data. ONE tool call per turn.
Output your final answer as a numbered list with just the values.`

func TestTabularE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tabular E2E benchmark in short mode")
	}

	apiKey, model := loadEnv(t)
	if apiKey == "" {
		t.Skip("OPENROUTER_API_KEY not set in .env — skipping tabular E2E benchmark")
	}

	// Locate the e2e test CSV
	repoRoot := findRepoRoot(t)
	csvPath := filepath.Join(repoRoot, "static", "e2e_test.csv")
	if _, err := os.Stat(csvPath); err != nil {
		t.Fatalf("e2e_test.csv not found at %s: %v", csvPath, err)
	}

	// Build tzro binary
	tzroBin := buildTzroBinary(t)

	t.Logf("repo root: %s", repoRoot)
	t.Logf("CSV path:  %s", csvPath)
	t.Logf("model:     %s", model)
	t.Logf("tzro:      %s", tzroBin)

	// --- Run A: Baseline (raw CSV, no tzro) ---
	t.Log("=== Run A: Baseline (raw CSV in context) ===")
	baseline := tabularAgentLoop(t, apiKey, model, repoRoot, "", nil)

	// --- Run B: Full Tzro (ingest + query + hook compaction) ---
	t.Log("=== Run B: Full Tzro (ingest + query + hooks) ===")
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()
	fullTzro := tabularAgentLoop(t, apiKey, model, repoRoot, tzroBin, s)

	// --- Evaluate correctness ---
	baselineScore := scoreAnswers(t, "Baseline", baseline.FinalAnswer)
	tzroScore := scoreAnswers(t, "Full Tzro", fullTzro.FinalAnswer)

	// --- Savings calculations ---
	pct := func(base, val int) float64 {
		if base == 0 { return 0 }
		return (1.0 - float64(val)/float64(base)) * 100
	}
	pctF := func(base, val float64) float64 {
		if base == 0 { return 0 }
		return (1.0 - val/base) * 100
	}
	pctMs := func(base, val int64) float64 {
		if base == 0 { return 0 }
		return (1.0 - float64(val)/float64(base)) * 100
	}

	// --- Comparison table ---
	t.Logf("\n"+
		"┌──────────────┬────────────┬────────────┬──────────────┬──────────────┬──────────┬───────┬───────┐\n"+
		"│ Mode         │ Prompt Tok │ Compl Tok  │ Tool Out (B) │    Cost ($)  │ Wall (s) │ Turns │ Score │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┼───────┤\n"+
		"│ Baseline     │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │ %d/5   │\n"+
		"│ Full Tzro    │ %10d │ %10d │ %12d │ %12.6f │ %8.1f │ %5d │ %d/5   │\n"+
		"├──────────────┼────────────┼────────────┼──────────────┼──────────────┼──────────┼───────┼───────┤\n"+
		"│ Tzro Δ       │ %9.1f%% │          — │ %11.1f%% │ %11.1f%% │ %7.1f%% │     — │     — │\n"+
		"└──────────────┴────────────┴────────────┴──────────────┴──────────────┴──────────┴───────┴───────┘",
		baseline.PromptTokens, baseline.CompletionTokens, baseline.ToolOutputBytes, baseline.TotalCostUSD, float64(baseline.WallClockMs)/1000, baseline.Turns, baselineScore,
		fullTzro.PromptTokens, fullTzro.CompletionTokens, fullTzro.ToolOutputBytes, fullTzro.TotalCostUSD, float64(fullTzro.WallClockMs)/1000, fullTzro.Turns, tzroScore,
		pct(baseline.PromptTokens, fullTzro.PromptTokens), pct(baseline.ToolOutputBytes, fullTzro.ToolOutputBytes), pctF(baseline.TotalCostUSD, fullTzro.TotalCostUSD), pctMs(baseline.WallClockMs, fullTzro.WallClockMs),
	)

	// Billing detail
	t.Logf("\nBilling:")
	t.Logf("  Baseline:  cost=$%.6f  cached=%d  cache_write=%d",
		baseline.TotalCostUSD, baseline.CachedTokens, baseline.CacheWriteTokens)
	t.Logf("  Full Tzro: cost=$%.6f  cached=%d  cache_write=%d",
		fullTzro.TotalCostUSD, fullTzro.CachedTokens, fullTzro.CacheWriteTokens)

	// Answer dumps
	for _, pair := range []struct{ name string; r RunResult }{{"baseline", baseline}, {"full_tzro", fullTzro}} {
		if pair.r.FinalAnswer != "" {
			answerPreview := pair.r.FinalAnswer
			if len(answerPreview) > 500 { answerPreview = answerPreview[:500] + "..." }
			t.Logf("%s answer (%d chars):\n%s", pair.name, len(pair.r.FinalAnswer), answerPreview)
		} else {
			t.Logf("WARNING: %s did not produce final answer (%d turns)", pair.name, pair.r.Turns)
		}
	}
}

// ---------------------------------------------------------------------------
// findRepoRoot walks up from CWD to find the tzro repo root.
// ---------------------------------------------------------------------------
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "tzro", "main.go")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not find repo root (no cmd/tzro/main.go found)")
	return ""
}

// ---------------------------------------------------------------------------
// Tabular agent loop — supports both baseline and tzro modes
// ---------------------------------------------------------------------------

func tabularAgentLoop(t *testing.T, apiKey, model, repoRoot, tzroBin string, s *store.Store) RunResult {
	t.Helper()

	useTzro := tzroBin != "" && s != nil

	// Build tools based on mode
	var tools []orTool
	if useTzro {
		tools = []orTool{
			{Type: "function", Function: orToolFunction{
				Name: "read_file",
				Description: "Read a file from the workspace. For tabular data (CSV/TSV/JSON), tzro will automatically import it into a queryable SQLite table and return a data envelope with sample rows and a table pointer.",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
				}, "required": []string{"path"}},
			}},
			{Type: "function", Function: orToolFunction{
				Name: "tzro_query",
				Description: "Execute a read-only SQL query against an imported tabular data table. Use the table name from the data envelope returned by read_file. Supports standard SQL: SELECT, WHERE, GROUP BY, ORDER BY, COUNT, AVG, MAX, MIN, SUM, etc.",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{
					"table": map[string]string{"type": "string", "description": "Table name from the data envelope (e.g. tbl_abc123)."},
					"sql":   map[string]string{"type": "string", "description": "SQL SELECT query to execute."},
				}, "required": []string{"table", "sql"}},
			}},
		}
	} else {
		tools = []orTool{
			{Type: "function", Function: orToolFunction{
				Name: "read_file",
				Description: "Read the full contents of a file in the workspace.",
				Parameters: map[string]any{"type": "object", "properties": map[string]any{
					"path": map[string]string{"type": "string", "description": "Relative path from workspace root."},
				}, "required": []string{"path"}},
			}},
		}
	}

	var systemPrompt string
	if useTzro {
		systemPrompt = `You are a data analyst with efficient query tools:

- read_file: Read a file. For CSV/TSV/JSON data, the system auto-imports it into SQLite and returns a data envelope with schema, sample rows, and a table pointer.
- tzro_query: Execute SQL against imported tables. Supports COUNT, AVG, MAX, MIN, GROUP BY, ORDER BY, etc.

IMPORTANT: ONE tool call per turn. All data values are stored as TEXT in SQLite — use CAST(col AS INTEGER) or CAST(col AS REAL) for numeric comparisons and aggregations.

Workflow:
1. read_file to load the CSV (you'll get back an envelope with the table name)
2. Use tzro_query with SQL to answer each question
3. Output your final numbered answers`
	} else {
		systemPrompt = `You are a data analyst. You can read files from the workspace.

IMPORTANT: ONE tool call per turn.

Read the data file and analyze it to answer the questions. Output your final numbered answers.`
	}

	messages := []orMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: tabularQuestions},
	}

	client := &http.Client{Timeout: 120 * time.Second}
	var result RunResult
	start := time.Now()
	const maxTurns = 20

	// Track the table name from ingest for tzro_query
	var importedTableName string

	for turn := 0; turn < maxTurns; turn++ {
		result.Turns = turn + 1
		reqBody := orRequest{Model: model, Messages: messages, Tools: tools}
		reqJSON, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("POST", "https://openrouter.ai/api/v1/chat/completions", bytes.NewReader(reqJSON))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("HTTP-Referer", "https://github.com/The18thWarrior/tzro")
		req.Header.Set("X-Title", "tzro-tabular-benchmark")

		resp, err := client.Do(req)
		if err != nil { t.Logf("turn %d: request failed: %v", turn, err); break }
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Logf("turn %d: API %d: %s", turn, resp.StatusCode, string(respBody[:min(len(respBody), 300)]))
			break
		}

		var orResp orResponse
		if err := json.Unmarshal(respBody, &orResp); err != nil {
			t.Logf("turn %d: unparseable response", turn); break
		}

		result.PromptTokens += orResp.Usage.PromptTokens
		result.CompletionTokens += orResp.Usage.CompletionTokens
		result.TotalCostUSD += orResp.Usage.Cost
		result.CacheDiscountUSD += orResp.Usage.CacheDiscount
		if orResp.Usage.PromptTokensDetails != nil {
			result.CachedTokens += orResp.Usage.PromptTokensDetails.CachedTokens
			result.CacheWriteTokens += orResp.Usage.PromptTokensDetails.CacheWriteTokens
		}

		if len(orResp.Choices) == 0 { t.Logf("turn %d: no choices", turn); break }
		choice := orResp.Choices[0]
		assistantMsg := choice.Message
		messages = append(messages, assistantMsg)

		if len(assistantMsg.ToolCalls) == 0 || choice.FinishReason == "stop" {
			result.FinalAnswer = assistantMsg.Content
			break
		}

		for _, tc := range assistantMsg.ToolCalls {
			var toolOutput string

			switch tc.Function.Name {
			case "read_file":
				var args map[string]string
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				path := args["path"]
				absPath := filepath.Join(repoRoot, path)
				data, err := os.ReadFile(absPath)
				if err != nil {
					toolOutput = fmt.Sprintf("error: %v", err)
				} else {
					rawContent := string(data)
					if useTzro {
						// Pass through Pi-Coder post-tool hook with store for tabular interception
						hookInput := PiCoderPostToolInput{ToolName: "read_file", ToolOutput: rawContent}
						hookJSON, _ := json.Marshal(hookInput)
						var hookOut bytes.Buffer
						if err := HandlePiCoderPostTool(bytes.NewReader(hookJSON), &hookOut, s); err == nil {
							var hookResp PiCoderPostToolOutput
							if err := json.Unmarshal(hookOut.Bytes(), &hookResp); err == nil {
								if out, ok := hookResp.ToolOutput.(string); ok {
									toolOutput = out
									// Extract table name from envelope
									if idx := strings.Index(toolOutput, "Table: `"); idx >= 0 {
										start := idx + len("Table: `")
										end := strings.Index(toolOutput[start:], "`")
										if end > 0 {
											importedTableName = toolOutput[start : start+end]
											t.Logf("turn %d: tabular data imported as %s", turn, importedTableName)
										}
									}
								}
							}
						}
						if toolOutput == "" { toolOutput = rawContent }
					} else {
						toolOutput = rawContent
					}
				}

			case "tzro_query":
				var args map[string]string
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				tableName := args["table"]
				sqlQuery := args["sql"]
				if tableName == "" && importedTableName != "" {
					tableName = importedTableName
				}
				// Execute query via tzro binary
				cmd := exec.Command(tzroBin, "query", tableName, sqlQuery)
				cmd.Dir = repoRoot
				out, err := cmd.CombinedOutput()
				if err != nil {
					// Try via store directly as fallback (in-memory store for test)
					results, cols, qErr := s.QuerySQL(sqlQuery)
					if qErr != nil {
						toolOutput = fmt.Sprintf("query error: %v\ntzro output: %s", qErr, string(out))
					} else {
						var sb strings.Builder
						sb.WriteString(fmt.Sprintf("# Query Results (%d rows)\n", len(results)))
						if len(cols) > 0 {
							sb.WriteString("| " + strings.Join(cols, " | ") + " |\n")
							sb.WriteString("|" + strings.Repeat(" --- |", len(cols)) + "\n")
							for _, row := range results {
								var vals []string
								for _, col := range cols {
									vals = append(vals, row[col])
								}
								sb.WriteString("| " + strings.Join(vals, " | ") + " |\n")
							}
						}
						toolOutput = sb.String()
					}
				} else {
					toolOutput = string(out)
				}

			default:
				toolOutput = fmt.Sprintf("unknown tool: %s", tc.Function.Name)
			}

			result.ToolOutputBytes += len(toolOutput)
			messages = append(messages, orMessage{
				Role: "tool", Content: toolOutput, ToolCallID: tc.ID,
			})

			t.Logf("turn %d: [%s] output=%d bytes (tzro=%v)", turn, tc.Function.Name, len(toolOutput), useTzro)
		}
	}

	result.WallClockMs = time.Since(start).Milliseconds()
	return result
}

// ---------------------------------------------------------------------------
// Answer scoring — checks the final answer against ground truth
// ---------------------------------------------------------------------------

func scoreAnswers(t *testing.T, label, answer string) int {
	t.Helper()
	if answer == "" {
		t.Logf("%s: no answer produced — score 0/5", label)
		return 0
	}

	score := 0
	lower := strings.ToLower(answer)
	_ = lower // used in Q5

	// Q1: Total rows = 6627
	if strings.Contains(answer, "6627") || strings.Contains(answer, "6,627") {
		score++
		t.Logf("%s Q1 (total rows): ✓ found 6627", label)
	} else {
		t.Logf("%s Q1 (total rows): ✗ expected 6627", label)
	}

	// Q2: Unique line_codes = 141
	if strings.Contains(answer, "141") {
		score++
		t.Logf("%s Q2 (unique line_codes): ✓ found 141", label)
	} else {
		t.Logf("%s Q2 (unique line_codes): ✗ expected 141", label)
	}

	// Q3: Highest single value = 42840
	if strings.Contains(answer, "42840") || strings.Contains(answer, "42,840") {
		score++
		t.Logf("%s Q3 (highest value): ✓ found 42840", label)
	} else {
		t.Logf("%s Q3 (highest value): ✗ expected 42840", label)
	}

	// Q4: Sum of values (industry=total, level=0, size=total) = 826461
	if strings.Contains(answer, "826461") || strings.Contains(answer, "826,461") {
		score++
		t.Logf("%s Q4 (sum total/0/total): ✓ found 826461", label)
	} else {
		t.Logf("%s Q4 (sum total/0/total): ✗ expected 826461", label)
	}

	// Q5: Level=1 industry with highest sum (size=total) = Construction (125841)
	if strings.Contains(lower, "construction") {
		score++
		t.Logf("%s Q5 (top L1 industry): ✓ found Construction", label)
	} else {
		t.Logf("%s Q5 (top L1 industry): ✗ expected Construction", label)
	}

	t.Logf("%s: correctness score = %d/5", label, score)
	return score
}
