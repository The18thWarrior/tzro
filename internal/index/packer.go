package index

import (
	"fmt"
	"strings"
)

// PackedContext represents the formatted, token-budgeted context ready for Probe Direct Synthesis.
type PackedContext struct {
	Buffer       string   `json:"buffer"`
	TokensUsed   int      `json:"tokensUsed"`
	ItemsCount   int      `json:"itemsCount"`
	AverageScore float64  `json:"averageScore"`
	IncludedIDs  []string `json:"includedIDs"`
}

// PackContextBudget greedily packs ranked search results into a formatted markdown context block,
// discarding results below minScore and stopping when maxTokenBudget is reached.
func PackContextBudget(results []SearchResult, minScore float64, maxTokenBudget int) PackedContext {
	if maxTokenBudget <= 0 {
		maxTokenBudget = 6000 // Default 70% budget for 8k window
	}

	var included []SearchResult
	var totalScore float64
	var includedIDs []string
	tokensUsed := 0

	var b strings.Builder
	b.WriteString("# Repository Pre-Index Context\n\n")
	overheadTokens := estimateTokens(b.String())
	tokensUsed += overheadTokens

	for _, res := range results {
		if res.Score < minScore {
			continue
		}

		itemFormatted := formatResultItem(res)
		itemTokens := estimateTokens(itemFormatted)

		if tokensUsed+itemTokens > maxTokenBudget {
			// If we haven't included any items yet, include at least the top item
			if len(included) == 0 {
				included = append(included, res)
				includedIDs = append(includedIDs, res.ID)
				totalScore += res.Score
				tokensUsed += itemTokens
				b.WriteString(itemFormatted)
			}
			break
		}

		included = append(included, res)
		includedIDs = append(includedIDs, res.ID)
		totalScore += res.Score
		tokensUsed += itemTokens
		b.WriteString(itemFormatted)
	}

	avgScore := 0.0
	if len(included) > 0 {
		avgScore = totalScore / float64(len(included))
	}

	return PackedContext{
		Buffer:       b.String(),
		TokensUsed:   tokensUsed,
		ItemsCount:   len(included),
		AverageScore: avgScore,
		IncludedIDs:  includedIDs,
	}
}

func formatResultItem(res SearchResult) string {
	var sb strings.Builder
	if res.SourceType == "code" {
		sb.WriteString(fmt.Sprintf("## Symbol: `%s` (%s)\n", res.Title, res.Kind))
		sb.WriteString(fmt.Sprintf("**File**: `%s`\n", res.FilePath))
		if res.Signature != "" {
			sb.WriteString(fmt.Sprintf("```go\n%s\n```\n", res.Signature))
		}
		if res.Content != "" {
			sb.WriteString(fmt.Sprintf("%s\n\n", res.Content))
		} else {
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString(fmt.Sprintf("## Document Section: %s\n", res.Title))
		sb.WriteString(fmt.Sprintf("**File**: `%s`\n\n", res.FilePath))
		sb.WriteString(fmt.Sprintf("%s\n\n", res.Content))
	}
	sb.WriteString("---\n\n")
	return sb.String()
}

func estimateTokens(text string) int {
	// Standard heuristic: ~4 characters per token
	chars := len(text)
	if chars == 0 {
		return 0
	}
	tokens := (chars + 3) / 4
	return tokens
}
