package signaldensity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tzro/pkg/store"
)

// Runner orchestrates benchmark execution across conditions R and T.
type Runner struct {
	Config BenchmarkConfig
	Client *Client
}

// NewRunner initializes a Runner instance.
func NewRunner(cfg BenchmarkConfig) *Runner {
	client := NewClient(cfg.BaseURL, cfg.APIKey, cfg.Timeout, cfg.NoCache)
	return &Runner{
		Config: cfg,
		Client: client,
	}
}

// Run executes the complete benchmark battery and returns the final report.
func (r *Runner) Run(ctx context.Context) (*BenchmarkReport, error) {
	start := time.Now()

	// 1. Initialize Content Store
	storePath := r.Config.StorePath
	if storePath == "" {
		home, _ := os.UserHomeDir()
		storePath = filepath.Join(home, ".tzro", "token_shield.db")
	}
	s, _ := store.OpenStore(storePath)
	if s != nil {
		defer s.Close()
	}

	// Validate API key when using OpenRouter
	if strings.Contains(r.Client.BaseURL, "openrouter.ai") && r.Client.APIKey == "" {
		return nil, fmt.Errorf("missing API key: please set OPENROUTER_API_KEY (or OPENAI_API_KEY) in your environment, or pass --api-key <key>")
	}

	// 2. Setup Cost Tracker
	costTracker := NewCostTracker(r.Config.MaxCost)

	// 3. Resolve Model Pricing
	pricing, ok := DefaultPricingMap[r.Config.Model]
	if !ok {
		pricing = FallbackPricing
	}

	// 4. Load & Filter Task Cases
	allCases, err := LoadAllTaskCases(s)
	if err != nil {
		return nil, fmt.Errorf("failed to load task cases: %w", err)
	}

	filteredCases := filterCases(allCases, r.Config.Tier, r.Config.Primitive)
	if len(filteredCases) == 0 {
		return nil, fmt.Errorf("no test cases matched tier=%q primitive=%q", r.Config.Tier, r.Config.Primitive)
	}

	// 5. Execute Cases
	var caseResults []CaseResult

	for _, task := range filteredCases {
		if costTracker.IsExceeded() {
			break
		}

		cRes := CaseResult{
			TaskID:  task.ID,
			Name:    task.Name,
			Battery: task.Battery,
			Tier:    task.Tier,
		}

		// Mini-Macro coding tasks: run via Pi-Coder agent loop
		if task.Battery == BatteryMiniMacro {
			// Condition R: Raw Pi-Coder Agent (uncompacted)
			resRaw, err := runPiCoderAgentTask(ctx, r.Client, r.Config.Model, task, pricing, s, false)
			if err != nil {
				cRes.Error = fmt.Sprintf("raw agent error: %v", err)
			} else if resRaw.Error != "" {
				cRes.Error = fmt.Sprintf("raw agent error: %s", resRaw.Error)
			}
			if resRaw != nil {
				cRes.RawPromptTokens = resRaw.PromptTokens
				cRes.RawCompletionTokens = resRaw.CompletionTokens
				cRes.RawCostUSD = resRaw.CostUSD
				cRes.RawResponse = resRaw.FinalResponse
				cRes.RawPassed = resRaw.Passed
				cRes.Turns = resRaw.Turns
				costTracker.AddCost(resRaw.CostUSD)
			}

			if costTracker.IsExceeded() {
				caseResults = append(caseResults, cRes)
				break
			}

			// Condition T: Tzro Pi-Coder Agent (hooked compaction)
			resTzro, err := runPiCoderAgentTask(ctx, r.Client, r.Config.Model, task, pricing, s, true)
			if err != nil {
				if cRes.Error != "" {
					cRes.Error += "; "
				}
				cRes.Error += fmt.Sprintf("tzro agent error: %v", err)
			} else if resTzro.Error != "" {
				if cRes.Error != "" {
					cRes.Error += "; "
				}
				cRes.Error += fmt.Sprintf("tzro agent error: %s", resTzro.Error)
			}
			if resTzro != nil {
				cRes.TzroPromptTokens = resTzro.PromptTokens
				cRes.TzroCompletionTokens = resTzro.CompletionTokens
				cRes.TzroCostUSD = resTzro.CostUSD
				cRes.TzroResponse = resTzro.FinalResponse
				cRes.TzroPassed = resTzro.Passed
				if resTzro.Turns > cRes.Turns {
					cRes.Turns = resTzro.Turns
				}
				costTracker.AddCost(resTzro.CostUSD)
			}

			if cRes.Error != "" {
				fmt.Fprintf(os.Stderr, "[-] Task %s: %s\n", task.ID, cRes.Error)
			}
			caseResults = append(caseResults, cRes)
			continue
		}

		// Micro batteries: Condition R (Raw Baseline)
		respRaw, err := r.Client.Complete(ctx, r.Config.Model, task.PromptRaw, pricing)
		if err != nil {
			cRes.Error = fmt.Sprintf("raw completion error: %v", err)
		} else {
			cRes.RawPromptTokens = respRaw.PromptTokens
			cRes.RawCompletionTokens = respRaw.CompletionTokens
			cRes.RawCostUSD = respRaw.CostUSD
			cRes.RawResponse = respRaw.Content
			costTracker.AddCost(respRaw.CostUSD)

			passed, err := EvaluateCompletion(respRaw.Content, task, "")
			if err != nil {
				cRes.Error = fmt.Sprintf("raw evaluation error: %v", err)
			}
			cRes.RawPassed = passed
		}

		if costTracker.IsExceeded() {
			caseResults = append(caseResults, cRes)
			break
		}

		// Micro batteries: Condition T (Tzro Optimized)
		respTzro, err := r.Client.Complete(ctx, r.Config.Model, task.PromptTzro, pricing)
		if err != nil {
			if cRes.Error != "" {
				cRes.Error += "; "
			}
			cRes.Error += fmt.Sprintf("tzro completion error: %v", err)
		} else {
			cRes.TzroPromptTokens = respTzro.PromptTokens
			cRes.TzroCompletionTokens = respTzro.CompletionTokens
			cRes.TzroCostUSD = respTzro.CostUSD
			cRes.TzroResponse = respTzro.Content
			cRes.Turns = 1
			costTracker.AddCost(respTzro.CostUSD)

			passed, err := EvaluateCompletion(respTzro.Content, task, "")
			if err != nil {
				if cRes.Error != "" {
					cRes.Error += "; "
				}
				cRes.Error += fmt.Sprintf("tzro evaluation error: %v", err)
			}
			cRes.TzroPassed = passed
		}

		if cRes.Error != "" {
			fmt.Fprintf(os.Stderr, "[-] Task %s: %s\n", task.ID, cRes.Error)
		}

		caseResults = append(caseResults, cRes)
	}

	// If every executed case failed with an error, return early
	allFailedWithError := true
	var firstError string
	for _, cr := range caseResults {
		if cr.Error == "" {
			allFailedWithError = false
			break
		} else if firstError == "" {
			firstError = cr.Error
		}
	}
	if len(caseResults) > 0 && allFailedWithError && firstError != "" {
		return nil, fmt.Errorf("benchmark execution failed: %s", firstError)
	}

	// 6. Aggregate by Battery
	batteryOrder := []string{
		BatteryASTSkeleton,
		BatteryCompactorLogs,
		BatterySmartJSON,
		BatteryTabularSQL,
		BatteryMiniMacro,
	}

	groupedCases := make(map[string][]CaseResult)
	for _, cr := range caseResults {
		groupedCases[cr.Battery] = append(groupedCases[cr.Battery], cr)
	}

	var batteryResults []BatteryResult
	for _, bName := range batteryOrder {
		cases, exists := groupedCases[bName]
		if exists && len(cases) > 0 {
			batteryResults = append(batteryResults, AggregateBattery(bName, cases))
		}
	}

	// Handle any custom batteries not in default order
	for bName, cases := range groupedCases {
		found := false
		for _, oName := range batteryOrder {
			if oName == bName {
				found = true
				break
			}
		}
		if !found && len(cases) > 0 {
			batteryResults = append(batteryResults, AggregateBattery(bName, cases))
		}
	}

	// 7. Aggregate Overall Summary
	summary := AggregateSummary(batteryResults)

	report := &BenchmarkReport{
		Schema: "https://tzro.dev/schemas/signal-density-benchmark-v1.json",
		Metadata: BenchmarkMetadata{
			Model:           r.Config.Model,
			Timestamp:       time.Now().UTC().Format(time.RFC3339),
			TotalCostUSD:    costTracker.CurrentSpend(),
			CompositeSDM:    summary.CompositeSDM,
			DurationSeconds: time.Since(start).Seconds(),
		},
		Summary:   summary,
		Batteries: batteryResults,
	}

	return report, nil
}

func filterCases(all []TaskCase, tier, primitive string) []TaskCase {
	tier = strings.ToLower(strings.TrimSpace(tier))
	primitive = strings.ToLower(strings.TrimSpace(primitive))

	var filtered []TaskCase
	for _, c := range all {
		if tier != "" && tier != TierAll && c.Tier != tier {
			continue
		}

		if primitive != "" && primitive != PrimitiveAll {
			// Support shorthand primitive names
			matched := false
			switch primitive {
			case PrimitiveSkeleton, "ast", "ast_skeleton":
				matched = (c.Primitive == PrimitiveSkeleton || c.Battery == BatteryASTSkeleton)
			case PrimitiveCompactor, "logs", "compactor_logs":
				matched = (c.Primitive == PrimitiveCompactor || c.Battery == BatteryCompactorLogs)
			case PrimitiveJSON, "crusher", "smart_json":
				matched = (c.Primitive == PrimitiveJSON || c.Battery == BatterySmartJSON)
			case PrimitiveTabular, "sql", "tabular_sql":
				matched = (c.Primitive == PrimitiveTabular || c.Battery == BatteryTabularSQL)
			case PrimitiveMacro, "mini_macro":
				matched = (c.Primitive == PrimitiveMacro || c.Battery == BatteryMiniMacro)
			default:
				matched = (c.Primitive == primitive || c.Battery == primitive)
			}
			if !matched {
				continue
			}
		}

		filtered = append(filtered, c)
	}

	return filtered
}
