package comparison

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tzro/internal/inference"
)

// GenerateReport writes the JSON results file and markdown report to outputDir.
func GenerateReport(results []ComparisonResult, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	datestamp := time.Now().Format("2006-01-02")

	// Write JSON
	jsonPath := filepath.Join(outputDir, fmt.Sprintf("comparison_results_%s.json", datestamp))
	jsonData, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}
	if err := os.WriteFile(jsonPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write JSON results: %w", err)
	}

	// Write Markdown
	mdPath := filepath.Join(outputDir, fmt.Sprintf("comparison_report_%s.md", datestamp))
	md := generateMarkdown(results, datestamp, jsonPath)
	if err := os.WriteFile(mdPath, []byte(md), 0644); err != nil {
		return fmt.Errorf("failed to write markdown report: %w", err)
	}

	return nil
}

func generateMarkdown(results []ComparisonResult, datestamp, jsonPath string) string {
	var sb strings.Builder

	// Group results by task and condition
	byTask := groupByTask(results)
	byCondition := groupByCondition(results)

	// Calculate headline metrics
	reactTokens := sumCloudTokens(byCondition[ConditionCloudReAct])
	coopTokens := sumCloudTokens(byCondition[ConditionCooperative])
	reactCost := sumCost(byCondition[ConditionCloudReAct])
	coopCost := sumCost(byCondition[ConditionCooperative])

	tokenSavings := 0.0
	if reactTokens > 0 {
		tokenSavings = (1.0 - float64(coopTokens)/float64(reactTokens)) * 100
	}
	costSavings := reactCost - coopCost

	// 1. Executive Summary
	sb.WriteString("# Comparison Benchmark Report\n\n")
	sb.WriteString(fmt.Sprintf("**Date:** %s\n\n", datestamp))
	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString(fmt.Sprintf("**Cooperative mode reduced cloud tokens by %.0f%% and cost by $%.4f vs. baseline.**\n\n", tokenSavings, costSavings))
	sb.WriteString(fmt.Sprintf("- Cloud ReAct (baseline): %d cloud tokens, $%.4f\n", reactTokens, reactCost))
	sb.WriteString(fmt.Sprintf("- Cooperative mode: %d cloud tokens, $%.4f\n\n", coopTokens, coopCost))

	// 2. Per-Task Comparison Table
	sb.WriteString("## Per-Task Comparison Table\n\n")
	sb.WriteString("| Task | Condition | Cloud Tokens | Local Tokens | Cost ($) | Wall Clock (ms) | Tool Calls | Quality |\n")
	sb.WriteString("|------|-----------|-------------|-------------|---------|----------------|-----------|--------|\n")

	for _, taskID := range sortedTaskIDs(byTask) {
		for _, r := range byTask[taskID] {
			qualityStr := fmt.Sprintf("%.2f", r.QualityScore)
			if r.Error != "" {
				qualityStr = "ERR"
			}
			sb.WriteString(fmt.Sprintf("| %s | %s | %d | %d | %.4f | %d | %d | %s |\n",
				r.TaskID, r.Condition,
				r.CloudTokens.TotalTokens, r.LocalTokens.TotalTokens,
				r.EstCostUSD, r.WallClockMs, r.ToolCallCount, qualityStr))
		}
	}
	sb.WriteString("\n")

	// 3. Scaling Analysis
	sb.WriteString("## Scaling Analysis\n\n")
	sb.WriteString("How savings percentages change from T1 (small) to T5 (large):\n\n")
	sb.WriteString("| Tier | Task | Baseline Tokens | Cooperative Tokens | Savings % |\n")
	sb.WriteString("|------|------|----------------|-------------------|----------|\n")

	for _, taskID := range sortedTaskIDs(byTask) {
		taskResults := byTask[taskID]
		var baseline, coop *ComparisonResult
		for i := range taskResults {
			if taskResults[i].Condition == ConditionCloudReAct {
				baseline = &taskResults[i]
			}
			if taskResults[i].Condition == ConditionCooperative {
				coop = &taskResults[i]
			}
		}
		if baseline != nil && coop != nil {
			savings := 0.0
			if baseline.CloudTokens.TotalTokens > 0 {
				savings = (1.0 - float64(coop.CloudTokens.TotalTokens)/float64(baseline.CloudTokens.TotalTokens)) * 100
			}
			sb.WriteString(fmt.Sprintf("| T%d | %s | %d | %d | %.1f%% |\n",
				baseline.TaskTier, taskID,
				baseline.CloudTokens.TotalTokens, coop.CloudTokens.TotalTokens, savings))
		}
	}
	sb.WriteString("\n")

	// 4. Three-Bucket Savings Attribution (ADR-0034)
	sb.WriteString("## Savings Attribution\n\n")
	sb.WriteString("Three independent savings mechanisms, computed as cross-condition deltas:\n\n")
	dagRawTokens := sumCloudTokens(byCondition[ConditionCloudDAGRaw])
	dagTokens := sumCloudTokens(byCondition[ConditionCloudDAG])

	// Bucket 1: DAG structural savings (cloud_react → cloud_dag_raw)
	dagStructuralSavings := 0.0
	if reactTokens > 0 {
		dagStructuralSavings = (1.0 - float64(dagRawTokens)/float64(reactTokens)) * 100
	}
	// Bucket 2: Pipeline compaction savings (cloud_dag_raw → cloud_dag)
	pipelineSavings := 0.0
	if dagRawTokens > 0 {
		pipelineSavings = (1.0 - float64(dagTokens)/float64(dagRawTokens)) * 100
	}
	// Bucket 3: Local offloading savings (cloud_dag → cooperative)
	localOffloadSavings := 0.0
	if dagTokens > 0 {
		localOffloadSavings = (1.0 - float64(coopTokens)/float64(dagTokens)) * 100
	}

	sb.WriteString("| Bucket | From → To | Tokens | Savings |\n")
	sb.WriteString("|--------|-----------|--------|--------|\n")
	sb.WriteString(fmt.Sprintf("| DAG Structure | cloud_react → cloud_dag_raw | %d → %d | %.0f%% |\n", reactTokens, dagRawTokens, dagStructuralSavings))
	sb.WriteString(fmt.Sprintf("| Pipeline Compaction | cloud_dag_raw → cloud_dag | %d → %d | %.0f%% |\n", dagRawTokens, dagTokens, pipelineSavings))
	sb.WriteString(fmt.Sprintf("| Local Offloading | cloud_dag → cooperative | %d → %d | %.0f%% |\n", dagTokens, coopTokens, localOffloadSavings))
	sb.WriteString(fmt.Sprintf("| **Total** | **cloud_react → cooperative** | **%d → %d** | **%.0f%%** |\n\n", reactTokens, coopTokens, tokenSavings))

	// 5. Quality Comparison
	sb.WriteString("## Quality Comparison\n\n")
	sb.WriteString("| Condition | Avg Quality Score |\n")
	sb.WriteString("|-----------|------------------|\n")

	for _, cond := range AllConditions() {
		condResults := byCondition[cond]
		avgQuality := avgQualityScore(condResults)
		sb.WriteString(fmt.Sprintf("| %s | %.2f |\n", cond, avgQuality))
	}
	sb.WriteString("\n")

	// 6. Methodology
	sb.WriteString("## Methodology\n\n")
	sb.WriteString(fmt.Sprintf("- **Date:** %s\n", datestamp))
	sb.WriteString("- **Task suite:** 5 documentation generation tasks (T1–T5) against the tzro codebase\n")
	sb.WriteString("- **Conditions:** 5 execution modes — cloud_react (baseline), cloud_dag_raw (DAG without pipeline), cloud_dag, local_only, cooperative\n")
	sb.WriteString("- **Quality scoring:** LLM-as-judge with fixed cloud model\n")
	sb.WriteString("- **Isolation:** Fresh TokenTracker and SQLite database per condition\n\n")

	// 7. Raw Data
	sb.WriteString("## Raw Data\n\n")
	sb.WriteString(fmt.Sprintf("Full results: [%s](%s)\n", filepath.Base(jsonPath), jsonPath))

	return sb.String()
}

// --- helpers ---

func groupByTask(results []ComparisonResult) map[string][]ComparisonResult {
	m := make(map[string][]ComparisonResult)
	for _, r := range results {
		m[r.TaskID] = append(m[r.TaskID], r)
	}
	return m
}

func groupByCondition(results []ComparisonResult) map[string][]ComparisonResult {
	m := make(map[string][]ComparisonResult)
	for _, r := range results {
		m[r.Condition] = append(m[r.Condition], r)
	}
	return m
}

func sumCloudTokens(results []ComparisonResult) int {
	total := 0
	for _, r := range results {
		total += r.CloudTokens.TotalTokens
	}
	return total
}

func sumCost(results []ComparisonResult) float64 {
	total := 0.0
	for _, r := range results {
		total += r.EstCostUSD
	}
	return total
}

func avgQualityScore(results []ComparisonResult) float64 {
	if len(results) == 0 {
		return 0
	}
	total := 0.0
	count := 0
	for _, r := range results {
		if r.Error == "" {
			total += r.QualityScore
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func sortedTaskIDs(byTask map[string][]ComparisonResult) []string {
	// Sort by tier to maintain T1-T5 order
	type taskInfo struct {
		id   string
		tier int
	}
	var tasks []taskInfo
	for id, results := range byTask {
		tier := 0
		if len(results) > 0 {
			tier = results[0].TaskTier
		}
		tasks = append(tasks, taskInfo{id, tier})
	}
	// Simple insertion sort (5 items max)
	for i := 1; i < len(tasks); i++ {
		for j := i; j > 0 && tasks[j].tier < tasks[j-1].tier; j-- {
			tasks[j], tasks[j-1] = tasks[j-1], tasks[j]
		}
	}
	ids := make([]string, len(tasks))
	for i, t := range tasks {
		ids[i] = t.id
	}
	return ids
}

// Ensure inference.TokenUsage is used (it's referenced in types.go but also needed here for summing)
var _ inference.TokenUsage
