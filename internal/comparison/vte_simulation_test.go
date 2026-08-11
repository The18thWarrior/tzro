package comparison

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
)

func initConfig(t *testing.T) {
	t.Helper()
	repoRoot := filepath.Join(os.Getenv("HOME"), "Desktop", "Repos", "tzro")
	t.Setenv("TZRO_DIR", repoRoot)
	if err := config.Load(); err != nil {
		t.Fatalf("config.Load() failed: %v", err)
	}
	// Verify the key loaded
	key := config.GetCloudAPIKey()
	if key == "" {
		t.Fatal("Cloud API key is empty after config.Load() — check config.json")
	}
	t.Logf("Config loaded (provider=%s, model=%s, key=%.8s...)",
		config.Get().CloudProvider, config.GetCloudModel(), key)
}

// ────────────────────────────────────────────────────────────────────────────
// Verification Gate prompt — faithfully implements the design spec:
//
//   Stage 3+4: Verification Gate + Conditional Re-Synthesis (single cloud call)
//   Input:  original goal + terminal_synthesis + compressed refinedContext
//   Output: {accepted, goalAlignment, factualGrounding, coherence, completeness, reason, reSynthesis?}
//
// Because this is a retroactive simulation, we don't have the Recall Node's
// refinedContext. We feed only goal + terminal_synthesis — this is actually a
// *harder* test for the verifier since it can't fact-check against exploration
// context. If it still aligns with the judge, the design is validated.
// ────────────────────────────────────────────────────────────────────────────

const verificationSystemPrompt = `You are a Verification Gate for an AI task execution system. Your job is to evaluate whether a local model's output adequately addresses the original task goal.

You will receive:
1. The original task GOAL
2. The local model's OUTPUT (terminal_synthesis)

Evaluate the output on four dimensions, each scored 0.0 to 1.0:
- goalAlignment: Does the output address what was asked?
- factualGrounding: Are claims supported, sourced, and plausible? Are URLs real? Are data values correct?
- coherence: Is the output well-structured, readable, and free of meta-commentary, repetition, or degenerate text?
- completeness: Does the output cover all requested aspects?

Decision rule: ACCEPT if ALL four scores >= 0.6. Otherwise REJECT.

If REJECTING, you MUST also provide a reSynthesis field containing a corrected, improved version of the output based solely on what can be inferred from the goal and available context. If accepting, omit reSynthesis.

Respond with ONLY a JSON object (no markdown fences) matching this exact schema:
{
  "accepted": true,
  "goalAlignment": 0.9,
  "factualGrounding": 0.85,
  "coherence": 0.95,
  "completeness": 0.8,
  "reason": "Brief explanation of the decision"
}

Or on rejection:
{
  "accepted": false,
  "goalAlignment": 0.3,
  "factualGrounding": 0.2,
  "coherence": 0.8,
  "completeness": 0.4,
  "reason": "Explanation of why rejected",
  "reSynthesis": "The corrected output..."
}

Be strict but fair. A synthesis that is well-structured and addresses the goal but has minor gaps should be accepted. A synthesis that contains meta-commentary about the task instead of actual content, or has fabricated data, should be rejected.`

type VerificationRubric struct {
	Accepted         bool    `json:"accepted"`
	GoalAlignment    float64 `json:"goalAlignment"`
	FactualGrounding float64 `json:"factualGrounding"`
	Coherence        float64 `json:"coherence"`
	Completeness     float64 `json:"completeness"`
	Reason           string  `json:"reason"`
	ReSynthesis      string  `json:"reSynthesis,omitempty"`
}

// verificationRubricSchema is the JSON schema passed to the cloud model's
// structured output mode (response_format: json_schema). This constrains
// token generation to valid JSON matching this schema, eliminating parse
// failures from unescaped characters in reSynthesis content.
const verificationRubricSchema = `{
  "type": "object",
  "properties": {
    "accepted": { "type": "boolean" },
    "goalAlignment": { "type": "number" },
    "factualGrounding": { "type": "number" },
    "coherence": { "type": "number" },
    "completeness": { "type": "number" },
    "reason": { "type": "string" },
    "reSynthesis": { "type": "string" }
  },
  "required": ["accepted", "goalAlignment", "factualGrounding", "coherence", "completeness", "reason"]
}`

type SimulationResult struct {
	TaskID           string             `json:"taskId"`
	Category         string             `json:"category"`
	Tier             int                `json:"tier"`
	BenchmarkQuality float64            `json:"benchmarkQuality"`
	Verification     VerificationRubric `json:"verification"`
	AlignedWithJudge bool               `json:"alignedWithJudge"`
	ReSynthesisLen   int                `json:"reSynthesisLen"`
}

// benchmarkResult matches the JSON shape from comparison_results_*.json
type benchmarkResult struct {
	TaskID       string  `json:"taskId"`
	TaskTier     int     `json:"taskTier"`
	Condition    string  `json:"condition"`
	OutputText   string  `json:"outputText"`
	QualityScore float64 `json:"qualityScore"`
	QualityNotes string  `json:"qualityNotes"`
}

type taskDef struct {
	ID       string `json:"id"`
	Category string `json:"category"`
	Tier     int    `json:"tier"`
	Prompt   string `json:"prompt"`
}

func TestVTERetroactiveSimulation(t *testing.T) {
	if os.Getenv("RUN_VTE_SIM") == "" {
		t.Skip("Skipping VTE simulation (set RUN_VTE_SIM=1 to run)")
	}

	initConfig(t)

	// ── Load benchmark results ──────────────────────────────────────────
	resultsPath := filepath.Join(
		os.Getenv("HOME"), "Desktop", "Repos", "tzro",
		".scratch", "benchmark", "results-full-11",
		"comparison_results_2026-08-04.json",
	)

	resultsData, err := os.ReadFile(resultsPath)
	if err != nil {
		t.Fatalf("Failed to read benchmark results: %v", err)
	}

	var results []benchmarkResult
	if err := json.Unmarshal(resultsData, &results); err != nil {
		t.Fatalf("Failed to parse benchmark results: %v", err)
	}

	// ── Load task definitions (goals) ───────────────────────────────────
	testdataDir := filepath.Join("testdata")
	taskFiles := []string{
		"research_tasks.json",
		"docgen_tasks.json",
		"datanal_tasks.json",
	}

	goalMap := make(map[string]taskDef)
	for _, f := range taskFiles {
		data, err := os.ReadFile(filepath.Join(testdataDir, f))
		if err != nil {
			t.Fatalf("Failed to read %s: %v", f, err)
		}
		var tasks []taskDef
		if err := json.Unmarshal(data, &tasks); err != nil {
			t.Fatalf("Failed to parse %s: %v", f, err)
		}
		for _, td := range tasks {
			goalMap[td.ID] = td
		}
	}

	// ── Identify VTE-eligible tasks (non-codegen) ───────────────────────
	codegenTasks := map[string]bool{
		"update_add_method":         true,
		"create_hello_handler":      true,
		"update_add_error_handling": true,
		"create_config_parser":      true,
		"create_cache_layer":        true,
		"update_refactor_middleware": true,
		"update_add_pagination":     true,
		"create_event_emitter":      true,
		"create_query_builder":      true,
		"update_migrate_interface":  true,
		"cache_function_index":      true, // docgen but probe-based
	}

	// Filter to VTE-eligible tasks only
	var eligible []benchmarkResult
	for _, r := range results {
		if codegenTasks[r.TaskID] {
			continue
		}
		// Must have a corresponding task definition
		if _, ok := goalMap[r.TaskID]; ok {
			eligible = append(eligible, r)
		}
	}

	t.Logf("VTE-eligible tasks: %d (of %d total)", len(eligible), len(results))

	// ── Run verification against each task ──────────────────────────────
	ctx := context.Background()
	var simResults []SimulationResult

	for i, r := range eligible {
		td := goalMap[r.TaskID]
		t.Logf("[%d/%d] Verifying %s (judge score: %.2f)...", i+1, len(eligible), r.TaskID, r.QualityScore)

		// Construct the verification prompt
		userMessage := fmt.Sprintf(
			"## GOAL\n\n%s\n\n## LOCAL MODEL'S OUTPUT\n\n%s",
			td.Prompt,
			r.OutputText,
		)

		messages := []inference.InferenceMessage{
			{Role: "system", Content: verificationSystemPrompt},
			{Role: "user", Content: userMessage},
		}

		// Call cloud model
		startTime := time.Now()
		response, err := inference.CallCloudModel(ctx, messages, verificationRubricSchema)
		elapsed := time.Since(startTime)

		if err != nil {
			t.Logf("  ERROR calling cloud model: %v", err)
			simResults = append(simResults, SimulationResult{
				TaskID:           r.TaskID,
				Category:         td.Category,
				Tier:             td.Tier,
				BenchmarkQuality: r.QualityScore,
				Verification: VerificationRubric{
					Accepted: false,
					Reason:   fmt.Sprintf("Cloud call failed: %v", err),
				},
			})
			continue
		}

		t.Logf("  Cloud response in %v (%d chars)", elapsed.Round(time.Millisecond), len(response))

		// Parse verification rubric
		cleaned := stripCodeFences(response)
		var rubric VerificationRubric
		if err := json.Unmarshal([]byte(cleaned), &rubric); err != nil {
			t.Logf("  PARSE ERROR: %v (raw: %.200s)", err, cleaned)
			simResults = append(simResults, SimulationResult{
				TaskID:           r.TaskID,
				Category:         td.Category,
				Tier:             td.Tier,
				BenchmarkQuality: r.QualityScore,
				Verification: VerificationRubric{
					Accepted: false,
					Reason:   fmt.Sprintf("Parse failed: %v", err),
				},
			})
			continue
		}

		// Determine alignment: does the verifier agree with the judge?
		// Judge score < 2.5 should be rejected, >= 4.0 should be accepted
		var aligned bool
		switch {
		case r.QualityScore >= 4.0:
			aligned = rubric.Accepted // should accept
		case r.QualityScore < 2.5:
			aligned = !rubric.Accepted // should reject
		default:
			aligned = true // borderline, either decision is fine
		}

		reSynthLen := len(rubric.ReSynthesis)

		t.Logf("  Result: accepted=%v, goalAlign=%.2f, factual=%.2f, coherence=%.2f, complete=%.2f",
			rubric.Accepted, rubric.GoalAlignment, rubric.FactualGrounding, rubric.Coherence, rubric.Completeness)
		t.Logf("  Reason: %s", rubric.Reason)
		if reSynthLen > 0 {
			t.Logf("  Re-synthesis: %d chars", reSynthLen)
		}
		t.Logf("  Judge alignment: %v (judge=%.2f, verifier=%v)", aligned, r.QualityScore, rubric.Accepted)

		simResults = append(simResults, SimulationResult{
			TaskID:           r.TaskID,
			Category:         td.Category,
			Tier:             td.Tier,
			BenchmarkQuality: r.QualityScore,
			Verification:     rubric,
			AlignedWithJudge: aligned,
			ReSynthesisLen:   reSynthLen,
		})

		// Small delay between calls to avoid rate limiting
		time.Sleep(500 * time.Millisecond)
	}

	// ── Generate summary report ─────────────────────────────────────────
	sort.Slice(simResults, func(i, j int) bool {
		return simResults[i].BenchmarkQuality < simResults[j].BenchmarkQuality
	})

	var accepted, rejected, aligned, misaligned int
	var totalGoalAlign, totalFactual, totalCoherence, totalComplete float64

	for _, sr := range simResults {
		if sr.Verification.Accepted {
			accepted++
		} else {
			rejected++
		}
		if sr.AlignedWithJudge {
			aligned++
		} else {
			misaligned++
		}
		totalGoalAlign += sr.Verification.GoalAlignment
		totalFactual += sr.Verification.FactualGrounding
		totalCoherence += sr.Verification.Coherence
		totalComplete += sr.Verification.Completeness
	}

	n := float64(len(simResults))
	t.Logf("\n%s", strings.Repeat("=", 60))
	t.Logf("VTE RETROACTIVE SIMULATION RESULTS")
	t.Logf("%s", strings.Repeat("=", 60))
	t.Logf("Total tasks verified: %d", len(simResults))
	t.Logf("Accepted: %d (%.0f%%)", accepted, float64(accepted)/n*100)
	t.Logf("Rejected: %d (%.0f%%)", rejected, float64(rejected)/n*100)
	t.Logf("Aligned with judge: %d/%d (%.0f%%)", aligned, len(simResults), float64(aligned)/n*100)
	t.Logf("Misaligned: %d/%d", misaligned, len(simResults))
	t.Logf("")
	t.Logf("Average rubric scores:")
	t.Logf("  goalAlignment:     %.2f", totalGoalAlign/n)
	t.Logf("  factualGrounding:  %.2f", totalFactual/n)
	t.Logf("  coherence:         %.2f", totalCoherence/n)
	t.Logf("  completeness:      %.2f", totalComplete/n)
	t.Logf("")

	// Detailed per-task table
	t.Logf("%-35s %5s %5s %5s %5s %5s %7s %7s %s",
		"TASK", "JUDGE", "GOAL", "FACT", "COHR", "COMP", "ACCEPT", "ALIGN", "REASON")
	t.Logf("%s", strings.Repeat("-", 120))
	for _, sr := range simResults {
		acceptStr := "ACCEPT"
		if !sr.Verification.Accepted {
			acceptStr = "REJECT"
		}
		alignStr := "✓"
		if !sr.AlignedWithJudge {
			alignStr = "✗"
		}
		reason := sr.Verification.Reason
		if len(reason) > 40 {
			reason = reason[:40] + "…"
		}
		t.Logf("%-35s %5.2f %5.2f %5.2f %5.2f %5.2f %7s %7s %s",
			sr.TaskID, sr.BenchmarkQuality,
			sr.Verification.GoalAlignment, sr.Verification.FactualGrounding,
			sr.Verification.Coherence, sr.Verification.Completeness,
			acceptStr, alignStr, reason)
	}

	// ── Save structured results to JSON ─────────────────────────────────
	outputDir := filepath.Join(
		os.Getenv("HOME"), "Desktop", "Repos", "tzro",
		".scratch", "benchmark", "results-full-11",
	)
	outputPath := filepath.Join(outputDir, "vte_simulation_results.json")

	reportData, _ := json.MarshalIndent(simResults, "", "  ")
	if err := os.WriteFile(outputPath, reportData, 0644); err != nil {
		t.Errorf("Failed to write simulation results: %v", err)
	} else {
		t.Logf("\nResults saved to: %s", outputPath)
	}

	// ── Assertions ──────────────────────────────────────────────────────
	alignmentRate := float64(aligned) / n
	t.Logf("\nAlignment rate: %.0f%% (target: ≥75%%)", alignmentRate*100)
	if alignmentRate < 0.75 {
		t.Errorf("Alignment rate %.0f%% below 75%% threshold — verifier disagrees with judge too often", alignmentRate*100)
	}
}
