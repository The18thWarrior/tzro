package comparison

import "tzro/internal/inference"

// EstimateCost computes the estimated USD cost of a single condition × task run.
// Cloud tokens are billed according to the pricing table; local tokens cost $0.00.
func EstimateCost(cloud, local inference.TokenUsage, pricing PricingTable) float64 {
	promptCost := float64(cloud.PromptTokens) / 1000.0 * pricing.PromptPer1KTokens
	completionCost := float64(cloud.CompletionTokens) / 1000.0 * pricing.CompletionPer1KTokens
	return promptCost + completionCost
}
