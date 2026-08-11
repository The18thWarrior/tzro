package comparison

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"tzro/internal/config"
	"tzro/internal/inference"
)

// stripCodeFences removes markdown code fences (```json ... ``` or ``` ... ```)
// that LLMs sometimes wrap around JSON responses.
var codeFenceRe = regexp.MustCompile("(?s)^\\s*```(?:json)?\\s*\n(.*?)\\s*```\\s*$")

func stripCodeFences(s string) string {
	s = strings.TrimSpace(s)

	// 1. Try to find content between triple backticks
	// This regex finds the first occurrence of ```[lang] ... ``` and captures the content
	re := regexp.MustCompile("(?s)```(?:[a-zA-Z]+)?\\s*\n?(.*?)\\s*```")
	if m := re.FindStringSubmatch(s); len(m) > 1 {
		return strings.TrimSpace(m[1])
	}

	// 2. Fallback: handle unterminated blocks (common with local models or truncated output)
	if idx := strings.Index(s, "```"); idx >= 0 {
		// Find the end of the opening tag (e.g., ```json\n)
		start := idx + 3
		// Skip language identifier if present
		newlineIdx := strings.Index(s[start:], "\n")
		if newlineIdx >= 0 {
			start += newlineIdx + 1
		}
		content := s[start:]
		return strings.TrimSpace(content)
	}

	return s
}

// JudgeCriterionScore holds a single criterion evaluation from the LLM judge.
type JudgeCriterionScore struct {
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Reasoning string  `json:"reasoning"`
}

// JudgeResponse is the structured response from the LLM-as-judge.
type JudgeResponse struct {
	Criteria     []JudgeCriterionScore `json:"criteria"`
	OverallScore float64               `json:"overallScore"`
	Summary      string                `json:"summary"`
}

const judgeSystemPrompt = `You are a documentation quality evaluator. You will receive a generated documentation file and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Missing/wrong
  2 = Minimal/mostly incorrect
  3 = Adequate but incomplete
  4 = Good, covers most requirements
  5 = Excellent, comprehensive and accurate

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "overallScore": 4.0,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const codeJudgeSystemPrompt = `You are a code quality evaluator. You will receive generated source code and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Does not compile/parse, or completely wrong
  2 = Compiles but major logic errors or missing requirements
  3 = Functional but incomplete or has style issues
  4 = Good, meets most requirements with minor issues
  5 = Excellent, correct, complete, idiomatic, and well-structured

For "Preservation" criteria (update tasks): verify that existing code, types, method signatures, and imports that were not part of the spec remain unchanged.

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "overallScore": 4.0,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const datanalJudgeSystemPrompt = `You are a data analysis quality evaluator. You will receive a data analysis result
produced by an AI model, along with the expected correct answer. Score each criterion
on a 1-5 scale:
  1 = Completely wrong or missing
  2 = Partially correct but major errors in values or groupings
  3 = Mostly correct but some missing data points or minor calculation errors
  4 = Correct values and groupings with only cosmetic issues
  5 = Exact match with expected answer, clearly formatted

Compare the model's output against the Expected Correct Answer section.

Respond with ONLY a JSON object in this exact format:
{
  "scores": {
    "Correctness": <1-5>,
    "Completeness": <1-5>,
    "Formatting": <1-5>,
    "Methodology": <1-5>
  },
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const researchJudgeSystemPrompt = `You are a research quality evaluator. You will receive a web research synthesis and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Missing, wrong, or no sources cited
  2 = Minimal research with unreliable or fabricated sources
  3 = Adequate research but incomplete coverage or weak sourcing
  4 = Good research with multiple real sources and solid analysis
  5 = Excellent, comprehensive research with authoritative sources and insightful synthesis

Pay special attention to:
- Whether cited URLs appear to be real and relevant (not fabricated)
- Whether the synthesis goes beyond simply listing search snippets
- Whether claims are supported by the cited sources
- Whether the analysis addresses all aspects of the research question

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "overallScore": 4.0,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

// JudgeSystemPromptForCategory returns the appropriate judge system prompt
// for the given task category.
func JudgeSystemPromptForCategory(category string) string {
	switch category {
	case CategoryCodegen:
		return codeJudgeSystemPrompt
	case CategoryDatanal:
		return datanalJudgeSystemPrompt
	case CategoryResearch:
		return researchJudgeSystemPrompt
	default:
		return judgeSystemPrompt
	}
}

// JudgeOutput sends the generated output and rubric to the cloud model for quality scoring.
// Returns per-criterion scores and an overall composite score.
// Judge tokens are tracked separately (not part of condition tracking).
func JudgeOutput(ctx context.Context, outputText string, rubric QualityRubric) (float64, string, error) {
	return JudgeOutputWithOptions(ctx, outputText, rubric, "", "")
}

// JudgeOutputWithEndpoint is like JudgeOutput but allows overriding the API endpoint (for testing).
func JudgeOutputWithEndpoint(ctx context.Context, outputText string, rubric QualityRubric, endpoint string) (float64, string, error) {
	return JudgeOutputWithOptions(ctx, outputText, rubric, endpoint, "")
}

// JudgeOutputWithOptions is the full-featured judge function supporting category-aware prompts
// and endpoint overrides.
func JudgeOutputWithOptions(ctx context.Context, outputText string, rubric QualityRubric, endpoint string, category string) (float64, string, error) {
	// Select the appropriate system prompt
	sysPrompt := JudgeSystemPromptForCategory(category)

	// Build the rubric description
	rubricText := "Quality Rubric (score each 1-5):\n"
	for _, c := range rubric.Criteria {
		rubricText += fmt.Sprintf("- %s: %s\n", c.Name, c.Description)
	}

	contentLabel := "Generated Output"
	switch category {
	case CategoryCodegen:
		contentLabel = "Generated Code"
	case CategoryDatanal:
		contentLabel = "Data Analysis Result"
	case CategoryResearch:
		contentLabel = "Research Synthesis"
	default:
		contentLabel = "Generated Documentation"
	}

	userMessage := fmt.Sprintf("## %s\n\n%s\n\n## Evaluation Rubric\n\n%s", contentLabel, outputText, rubricText)

	var responseText string
	var err error

	if endpoint != "" {
		// Testing path: direct HTTP call to the provided endpoint
		responseText, err = callJudgeEndpoint(ctx, endpoint, userMessage, sysPrompt)
	} else {
		// Production path: use the standard cloud model
		messages := []inference.InferenceMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userMessage},
		}
		responseText, err = inference.CallCloudModel(ctx, messages, "")
	}
	if err != nil {
		return 0, "", fmt.Errorf("judge API call failed: %w", err)
	}

	responseText = stripCodeFences(responseText)

	// Try structured JudgeResponse first
	var judgeResp JudgeResponse
	if err := json.Unmarshal([]byte(responseText), &judgeResp); err == nil && judgeResp.OverallScore > 0 {
		return judgeResp.OverallScore, judgeResp.Summary, nil
	}

	// Fallback: handle flat {"criterionName": score, ...} format that models often produce
	score, summary, fallbackErr := parseFlatJudgeResponse(responseText, rubric)
	if fallbackErr != nil {
		return 0, "", fmt.Errorf("failed to parse judge response in any format (raw: %s)", responseText)
	}
	return score, summary, nil
}

// parseFlatJudgeResponse handles responses where the model returns a flat map
// of criterion names to scores, e.g. {"completeness": 5, "accuracy": 4}.
// Returns the mean score and an auto-generated summary.
func parseFlatJudgeResponse(responseText string, rubric QualityRubric) (float64, string, error) {
	var flat map[string]interface{}
	if err := json.Unmarshal([]byte(responseText), &flat); err != nil {
		return 0, "", err
	}

	// Extract summary if present at root level
	summaryText := ""
	if s, ok := flat["summary"].(string); ok {
		summaryText = s
	}

	// Try nested {"scores": {"Correctness": 1, ...}} format first
	if scoresObj, ok := flat["scores"]; ok {
		if scoresMap, ok := scoresObj.(map[string]interface{}); ok {
			score, summary, err := matchCriteriaScores(scoresMap, rubric, summaryText)
			if err == nil {
				return score, summary, nil
			}
		}
	}

	// Fallback: flat root-level {"correctness": 5, "accuracy": 4} format
	return matchCriteriaScores(flat, rubric, summaryText)
}

// matchCriteriaScores extracts criterion scores from a map using case-insensitive
// matching against rubric criteria names. Returns the mean score.
func matchCriteriaScores(scores map[string]interface{}, rubric QualityRubric, summaryOverride string) (float64, string, error) {
	// Build a case-insensitive lookup of the scores map
	lowerScores := make(map[string]interface{}, len(scores))
	for k, v := range scores {
		lowerScores[strings.ToLower(k)] = v
	}

	var total float64
	var count int
	for _, c := range rubric.Criteria {
		key := strings.ToLower(c.Name)
		if v, ok := lowerScores[key]; ok {
			switch n := v.(type) {
			case float64:
				total += n
				count++
			case json.Number:
				f, _ := n.Float64()
				total += f
				count++
			}
		}
	}

	if count == 0 {
		return 0, "", fmt.Errorf("no matching criteria scores found in response")
	}

	avg := total / float64(count)
	summary := summaryOverride
	if summary == "" {
		summary = fmt.Sprintf("Auto-scored from flat response (%d/%d criteria matched)", count, len(rubric.Criteria))
	}
	return avg, summary, nil
}

// callJudgeEndpoint makes a simple chat completion call to a custom endpoint.
func callJudgeEndpoint(ctx context.Context, endpoint, userMessage, sysPrompt string) (string, error) {
	type Message struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type Request struct {
		Model       string    `json:"model"`
		Messages    []Message `json:"messages"`
		Temperature float64   `json:"temperature"`
	}

	reqBody := Request{
		Model: config.GetCloudModel(),
		Messages: []Message{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userMessage},
		},
		Temperature: 0.1,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("judge API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode judge response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("empty choices from judge API")
	}

	return result.Choices[0].Message.Content, nil
}
