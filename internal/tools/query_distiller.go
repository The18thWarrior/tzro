package tools

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"tzro/internal/inference"
)

var (
	siteFilterRe     = regexp.MustCompile(`(?i)\bsite:[^\s]+`)
	filetypeFilterRe = regexp.MustCompile(`(?i)\bfiletype:[^\s]+`)
	cveRe            = regexp.MustCompile(`(?i)\bCVE-\d{4}-\d+\b`)
	punctCleanupRe   = regexp.MustCompile(`[,()\[\]{}"';]+`)
	multiWhitespaceRe = regexp.MustCompile(`\s+`)
)

// DistillSearchQuery optimizes a raw search query using the local neural sidecar
// when available, condensing verbose conversational prompts or multi-sentence
// instructions into 3-6 high-value search terms while preserving search operators
// (e.g. site:, filetype:) and technical identifiers (e.g. CVE-2024-45338).
func DistillSearchQuery(ctx context.Context, rawQuery string) string {
	trimmed := strings.TrimSpace(rawQuery)
	if trimmed == "" {
		return ""
	}

	// Fast path: if the query is already concise (<= 6 words, single line, no prompt noise), return as-is.
	words := strings.Fields(trimmed)
	if len(words) <= 6 && !strings.ContainsAny(trimmed, "\n\r") && !hasPromptBoilerplate(trimmed) {
		return trimmed
	}

	// Attempt neural distillation if local model sidecar is active.
	if isLocalModelActive() {
		distilled, err := distillViaNeuralSidecar(ctx, trimmed)
		if err == nil && distilled != "" {
			return distilled
		}
	}

	// Fallback to language-agnostic heuristic extraction
	return distillFallback(trimmed)
}

func hasPromptBoilerplate(q string) bool {
	lower := strings.ToLower(q)
	noise := []string{
		"search the web", "search for", "find articles", "browse at least",
		"synthesize a", "cite all", "deep insights", "comparative report",
		"investigate", "explore the", "comprehensive",
	}
	for _, n := range noise {
		if strings.Contains(lower, n) {
			return true
		}
	}
	return false
}

func isLocalModelActive() bool {
	if inference.GlobalLocalModel == nil {
		return false
	}
	status, _, _, _, _ := inference.GlobalLocalModel.GetStatusInfo()
	return status == "Active" || status == "Adopted"
}

func distillViaNeuralSidecar(ctx context.Context, rawQuery string) (string, error) {
	distillCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	distillCtx = context.WithValue(distillCtx, inference.MaxTokensKey, 32)
	distillCtx = context.WithValue(distillCtx, inference.TemperatureKey, 0.0)

	systemPrompt := "You are a search query optimizer. Given a verbose search request or information need in any language, output ONLY the 3 to 6 essential keyword search terms for a web search engine. Preserve domain filters (site:...) and specific version identifiers or CVE numbers. Output ONLY the query terms on a single line, with no explanations, formatting, or quotes."

	msgs := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: fmt.Sprintf("Query to distill:\n%s", rawQuery)},
	}

	res, err := inference.GlobalLocalModel.CallLocalModel(distillCtx, msgs, "")
	if err != nil {
		return "", err
	}

	out := strings.TrimSpace(res.Content)
	// Strip markdown blocks, tags, quotes if any
	out = strings.Trim(out, "\"`'\n\r ")
	if strings.HasPrefix(out, "<think>") {
		if idx := strings.Index(out, "</think>"); idx != -1 {
			out = strings.TrimSpace(out[idx+len("</think>"):])
		}
	}

	// Clean single line
	lines := strings.Split(out, "\n")
	if len(lines) > 0 {
		out = strings.TrimSpace(lines[0])
	}

	if out == "" {
		return "", fmt.Errorf("empty sidecar output")
	}

	fmt.Fprintf(os.Stderr, "[QueryDistiller] Distilled via sidecar: %q -> %q\n", rawQuery[:min(len(rawQuery), 60)], out)
	return out, nil
}

// distillFallback extracts operators and key terms in a language-agnostic manner.
func distillFallback(rawQuery string) string {
	// Preserve filters
	var operators []string
	for _, match := range siteFilterRe.FindAllString(rawQuery, -1) {
		operators = append(operators, match)
	}
	for _, match := range filetypeFilterRe.FindAllString(rawQuery, -1) {
		operators = append(operators, match)
	}
	for _, match := range cveRe.FindAllString(rawQuery, -1) {
		operators = append(operators, match)
	}

	// Remove operators and prompt noise from text
	cleaned := siteFilterRe.ReplaceAllString(rawQuery, " ")
	cleaned = filetypeFilterRe.ReplaceAllString(cleaned, " ")
	cleaned = punctCleanupRe.ReplaceAllString(cleaned, " ")
	cleaned = multiWhitespaceRe.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	// Filter out long prompt noise sentences if multiple sentences exist
	words := strings.Fields(cleaned)
	var meaningful []string
	noiseWords := map[string]bool{
		"the": true, "and": true, "for": true, "with": true, "from": true,
		"browse": true, "search": true, "find": true, "report": true, "cite": true,
		"urls": true, "distinct": true, "least": true, "insights": true,
	}

	for _, w := range words {
		wLower := strings.ToLower(w)
		if !noiseWords[wLower] && len(w) > 1 {
			meaningful = append(meaningful, w)
		}
		if len(meaningful) >= 6 {
			break
		}
	}

	resultTerms := append(meaningful, operators...)
	if len(resultTerms) == 0 {
		return rawQuery
	}

	// Deduplicate
	seen := make(map[string]bool)
	var finalTerms []string
	for _, term := range resultTerms {
		if !seen[term] {
			seen[term] = true
			finalTerms = append(finalTerms, term)
		}
	}

	return strings.Join(finalTerms, " ")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
