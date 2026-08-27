package comparison

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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

// JudgeRatings holds canonical 4-dimensional VTE-aligned evaluation scores (1-5 scale).
type JudgeRatings struct {
	GoalAlignment    float64 `json:"goalAlignment"`
	FactualGrounding float64 `json:"factualGrounding"`
	Coherence        float64 `json:"coherence"`
	Completeness     float64 `json:"completeness"`
}

// JudgeResponse is the structured response from the LLM-as-judge.
type JudgeResponse struct {
	Criteria         []JudgeCriterionScore `json:"criteria"`
	Ratings          *JudgeRatings         `json:"ratings,omitempty"`
	GoalAlignment    float64               `json:"goalAlignment,omitempty"`
	FactualGrounding float64               `json:"factualGrounding,omitempty"`
	Coherence        float64               `json:"coherence,omitempty"`
	Completeness     float64               `json:"completeness,omitempty"`
	OverallScore     float64               `json:"overallScore"`
	Summary          string                `json:"summary"`
}

const judgeSystemPrompt = `You are a documentation quality evaluator. You will receive a task prompt/goal, a generated documentation file, and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Missing/wrong
  2 = Minimal/mostly incorrect
  3 = Adequate but incomplete
  4 = Good, covers most requirements
  5 = Excellent, comprehensive and accurate

Focus your evaluation strictly on the technical content, accuracy, completeness, and structure of the provided documentation. Do NOT penalize the output for lack of tool-execution logs, file-saving confirmations, or execution metadata (tool calls and file persistence are verified separately by automated harness checks, not within the generated text).

Also rate the output on the 4 canonical evaluation dimensions (1.0 to 5.0 scale):
- goalAlignment: Does the output satisfy the exact technical intent, constraints, and target paths requested in the task prompt?
- factualGrounding: Are documented package names, functions, signatures, and types accurate and verified against the actual codebase?
- coherence: Is the output clean, well-structured, professional, and easy to navigate?
- completeness: Are all requested packages, files, symbols, and sections covered?

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "ratings": {
    "goalAlignment": 4.5,
    "factualGrounding": 4.5,
    "coherence": 5.0,
    "completeness": 4.0
  },
  "overallScore": 4.5,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const codeJudgeSystemPrompt = `You are a code quality evaluator. You will receive a task prompt/goal, generated source code, and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Does not compile/parse, or completely wrong
  2 = Compiles but major logic errors or missing requirements
  3 = Functional but incomplete or has style issues
  4 = Good, meets most requirements with minor issues
  5 = Excellent, correct, complete, idiomatic, and well-structured

Focus your evaluation strictly on the generated code quality and correctness. Do NOT penalize for lack of tool-execution logs or file saving metadata.

For "Preservation" criteria (update tasks): verify that existing code, types, method signatures, and imports that were not part of the spec remain unchanged.

Also rate the output on the 4 canonical evaluation dimensions (1.0 to 5.0 scale):
- goalAlignment: Does the code implement the exact spec, constraints, endpoints, and types requested in the task prompt?
- factualGrounding: Does the code compile, use valid imports/types, and preserve untouched methods?
- coherence: Is the code clean, idiomatic, and well-structured?
- completeness: Are all methods, error cases, validation rules, and defaults implemented?

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "ratings": {
    "goalAlignment": 4.5,
    "factualGrounding": 4.5,
    "coherence": 5.0,
    "completeness": 4.0
  },
  "overallScore": 4.5,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const datanalJudgeSystemPrompt = `You are a data analysis quality evaluator. You will receive a data analysis result
produced by an AI model, along with the expected correct answer and the original task prompt. Score each criterion
on a 1-5 scale:
  1 = Completely wrong or missing
  2 = Partially correct but major errors in values or groupings
  3 = Mostly correct but some missing data points or minor calculation errors
  4 = Correct values and groupings with only cosmetic issues
  5 = Exact match with expected answer, clearly formatted

Compare the model's output against the Expected Correct Answer section. Do NOT penalize for lack of tool-execution logs.

Also rate the output on the 4 canonical evaluation dimensions (1.0 to 5.0 scale):
- goalAlignment: Does the analysis answer the exact question, filter criteria, and columns requested in the prompt?
- factualGrounding: Are values, counts, percentages, and lists factually matching the expected answer?
- coherence: Is the output clearly formatted and structured?
- completeness: Are all requested groups, top-N ranks, and categories present without omissions?

Respond with ONLY a JSON object in this exact format:
{
  "criteria": [
    {"name": "Correctness", "score": 5, "reasoning": "Values match expected answer"},
    {"name": "Completeness", "score": 5, "reasoning": "All records included"}
  ],
  "ratings": {
    "goalAlignment": 5.0,
    "factualGrounding": 5.0,
    "coherence": 5.0,
    "completeness": 5.0
  },
  "overallScore": 5.0,
  "summary": "Brief overall assessment"
}

Do NOT wrap the JSON in code fences. Output raw JSON only.`

const researchJudgeSystemPrompt = `You are a research quality evaluator. You will receive a task prompt/goal, a web research synthesis generated to answer that prompt, and a quality rubric. Score each criterion on a 1-5 scale:
  1 = Missing, wrong, or no sources cited
  2 = Minimal research with unreliable or fabricated sources
  3 = Adequate research but incomplete coverage or weak sourcing
  4 = Good research with multiple real sources and solid analysis fulfilling the goal
  5 = Excellent, comprehensive research with authoritative sources, structured comparison tables/data, and insightful synthesis

Focus evaluation on research synthesis quality and citations. Do NOT penalize for lack of tool-execution logs.

Evaluation dimensions:
- goalAlignment (1.0 - 5.0): Whether the synthesis directly addresses all aspects requested in the Task Prompt (e.g. top N selection, comparison matrix).
- factualGrounding (1.0 - 5.0): Whether cited URLs are real, accessible, and reputable (official docs, GitHub repos, CVE databases, recognized tech publications) with verified metrics.
- coherence (1.0 - 5.0): Structured comparative presentation (tables, trade-off analysis) rather than unorganized snippet dumps.
- completeness (1.0 - 5.0): Whether all requested frameworks, versions, metrics, and comparisons are covered.

Respond ONLY with valid JSON (no markdown fences) matching this exact schema:
{
  "criteria": [
    {"name": "CriterionName", "score": 4, "reasoning": "Brief explanation"}
  ],
  "ratings": {
    "goalAlignment": 4.0,
    "factualGrounding": 4.0,
    "coherence": 4.0,
    "completeness": 4.0
  },
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
	return JudgeOutputWithOptions(ctx, outputText, rubric, JudgeOptions{})
}

// JudgeOutputWithEndpoint is like JudgeOutput but allows overriding the API endpoint (for testing).
func JudgeOutputWithEndpoint(ctx context.Context, outputText string, rubric QualityRubric, endpoint string) (float64, string, error) {
	return JudgeOutputWithOptions(ctx, outputText, rubric, JudgeOptions{Endpoint: endpoint})
}

// JudgeOutputWithOptions is the full-featured judge function returning overall score and summary.
func JudgeOutputWithOptions(ctx context.Context, outputText string, rubric QualityRubric, opts JudgeOptions) (float64, string, error) {
	resp, err := JudgeOutputDetailed(ctx, outputText, rubric, opts)
	if err != nil {
		return 0, "", err
	}
	return resp.OverallScore, resp.Summary, nil
}

// JudgeOutputDetailed sends the generated output and rubric to the cloud model for quality scoring.
// Returns the full JudgeResponse containing criteria, ratings (goal/fact/cohr/comp), overall score, and summary.
func JudgeOutputDetailed(ctx context.Context, outputText string, rubric QualityRubric, opts JudgeOptions) (*JudgeResponse, error) {
	// Select the appropriate system prompt
	sysPrompt := JudgeSystemPromptForCategory(opts.Category)

	// Build the rubric description
	rubricText := "Quality Rubric (score each 1-5):\n"
	for _, c := range rubric.Criteria {
		rubricText += fmt.Sprintf("- %s: %s\n", c.Name, c.Description)
	}

	contentLabel := "Generated Output"
	switch opts.Category {
	case CategoryCodegen:
		contentLabel = "Generated Code"
	case CategoryDatanal:
		contentLabel = "Data Analysis Result"
	case CategoryResearch:
		contentLabel = "Research Synthesis"
	default:
		contentLabel = "Generated Documentation"
	}

	var userMessage string
	if opts.Prompt != "" {
		userMessage = fmt.Sprintf("## Task Prompt / Goal\n\n%s\n\n## %s\n\n%s\n\n## Evaluation Rubric\n\n%s", opts.Prompt, contentLabel, outputText, rubricText)
	} else {
		userMessage = fmt.Sprintf("## %s\n\n%s\n\n## Evaluation Rubric\n\n%s", contentLabel, outputText, rubricText)
	}

	var responseText string
	var err error

	if opts.Model != "" {
		// OpenRouter path: configurable judge model
		responseText, err = callOpenRouterJudge(ctx, opts.Model, userMessage, sysPrompt)
	} else if opts.Endpoint != "" {
		// Testing path: direct HTTP call to the provided endpoint
		responseText, err = callJudgeEndpoint(ctx, opts.Endpoint, userMessage, sysPrompt)
	} else {
		// Production path: use the standard cloud model
		messages := []inference.InferenceMessage{
			{Role: "system", Content: sysPrompt},
			{Role: "user", Content: userMessage},
		}
		responseText, err = inference.CallCloudModel(ctx, messages, "")
	}
	if err != nil {
		return nil, fmt.Errorf("judge API call failed: %w", err)
	}

	responseText = stripCodeFences(responseText)

	// Try structured JudgeResponse first
	var judgeResp JudgeResponse
	if err := json.Unmarshal([]byte(responseText), &judgeResp); err == nil && (judgeResp.OverallScore > 0 || judgeResp.Ratings != nil) {
		normalizeJudgeResponse(&judgeResp, rubric)
		return &judgeResp, nil
	}

	// Fallback: handle flat {"criterionName": score, ...} format that models often produce
	resp, fallbackErr := parseFlatJudgeResponseDetailed(responseText, rubric)
	if fallbackErr != nil {
		return nil, fmt.Errorf("failed to parse judge response in any format (raw: %s)", responseText)
	}
	return resp, nil
}

// judgeRetryBackoffs defines the exponential backoff durations between retry attempts.
// Production: 2s, 4s, 8s. Tests override this via judgeRetryBackoffsOverride.
var judgeRetryBackoffs = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}

// judgeRetryBackoffsOverride allows tests to use zero-duration backoffs.
// When non-nil, overrides judgeRetryBackoffs.
var judgeRetryBackoffsOverride []time.Duration

func getJudgeRetryBackoffs() []time.Duration {
	if judgeRetryBackoffsOverride != nil {
		return judgeRetryBackoffsOverride
	}
	return judgeRetryBackoffs
}

// JudgeOutputDetailedWithRetry wraps JudgeOutputDetailed with 3x exponential
// backoff retries (2s, 4s, 8s) for transient API failures (HTTP 500, rate limits,
// network errors). Returns the first successful response, or the final error
// after all attempts are exhausted.
func JudgeOutputDetailedWithRetry(ctx context.Context, outputText string, rubric QualityRubric, opts JudgeOptions) (*JudgeResponse, error) {
	backoffs := getJudgeRetryBackoffs()
	maxAttempts := len(backoffs) + 1 // 1 initial + N retries

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := backoffs[attempt-1]
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			fmt.Fprintf(os.Stderr, "[Judge] Retry attempt %d/%d after %v for judge API call\n", attempt+1, maxAttempts, backoff)
		}

		resp, err := JudgeOutputDetailed(ctx, outputText, rubric, opts)
		if err == nil {
			return resp, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("judge API failed after %d attempts: %w", maxAttempts, lastErr)
}

func normalizeJudgeResponse(resp *JudgeResponse, rubric QualityRubric) {
	if resp.Ratings != nil {
		if resp.GoalAlignment == 0 {
			resp.GoalAlignment = resp.Ratings.GoalAlignment
		}
		if resp.FactualGrounding == 0 {
			resp.FactualGrounding = resp.Ratings.FactualGrounding
		}
		if resp.Coherence == 0 {
			resp.Coherence = resp.Ratings.Coherence
		}
		if resp.Completeness == 0 {
			resp.Completeness = resp.Ratings.Completeness
		}
	}

	if resp.OverallScore == 0 && len(resp.Criteria) > 0 {
		var total float64
		for _, c := range resp.Criteria {
			total += c.Score
		}
		resp.OverallScore = total / float64(len(resp.Criteria))
	}

	if resp.OverallScore == 0 && (resp.GoalAlignment > 0 || resp.FactualGrounding > 0 || resp.Coherence > 0 || resp.Completeness > 0) {
		var sum float64
		var count int
		for _, val := range []float64{resp.GoalAlignment, resp.FactualGrounding, resp.Coherence, resp.Completeness} {
			if val > 0 {
				sum += val
				count++
			}
		}
		if count > 0 {
			resp.OverallScore = sum / float64(count)
		}
	}

	// If ratings were missing, backfill from criteria or overall score
	if resp.GoalAlignment == 0 {
		resp.GoalAlignment = resp.OverallScore
	}
	if resp.FactualGrounding == 0 {
		resp.FactualGrounding = resp.OverallScore
	}
	if resp.Coherence == 0 {
		resp.Coherence = resp.OverallScore
	}
	if resp.Completeness == 0 {
		resp.Completeness = resp.OverallScore
	}
}

// parseFlatJudgeResponseDetailed handles responses where the model returns a flat map
// of criterion names to scores, e.g. {"completeness": 5, "accuracy": 4}.
func parseFlatJudgeResponseDetailed(responseText string, rubric QualityRubric) (*JudgeResponse, error) {
	var flat map[string]interface{}
	if err := json.Unmarshal([]byte(responseText), &flat); err != nil {
		return nil, err
	}

	summaryText := ""
	if s, ok := flat["summary"].(string); ok {
		summaryText = s
	}

	var resp JudgeResponse
	resp.Summary = summaryText

	// Try nested {"scores": {"Correctness": 1, ...}} format or "criteria" array
	scoresMap := flat
	if scoresObj, ok := flat["scores"]; ok {
		if sm, ok := scoresObj.(map[string]interface{}); ok {
			scoresMap = sm
		}
	} else if critObj, ok := flat["criteria"]; ok {
		if critArr, ok := critObj.([]interface{}); ok {
			scoresMap = make(map[string]interface{}, len(critArr))
			for _, item := range critArr {
				if cMap, ok := item.(map[string]interface{}); ok {
					if name, ok := cMap["name"].(string); ok {
						if score, ok := cMap["score"]; ok {
							scoresMap[name] = score
						}
					}
				}
			}
		}
	}

	score, summary, err := matchCriteriaScores(scoresMap, rubric, summaryText)
	if err != nil {
		return nil, err
	}

	if osVal := extractFloat(flat, "overallScore", "overall_score", "overall"); osVal > 0 {
		resp.OverallScore = osVal
	} else {
		resp.OverallScore = score
	}
	if resp.Summary == "" {
		resp.Summary = summary
	}

	// Check if ratings object is present
	if rObj, ok := flat["ratings"]; ok {
		if rMap, ok := rObj.(map[string]interface{}); ok {
			resp.GoalAlignment = extractFloat(rMap, "goalAlignment", "goal_alignment")
			resp.FactualGrounding = extractFloat(rMap, "factualGrounding", "factual_grounding")
			resp.Coherence = extractFloat(rMap, "coherence")
			resp.Completeness = extractFloat(rMap, "completeness")
		}
	}

	normalizeJudgeResponse(&resp, rubric)
	return &resp, nil
}

func extractFloat(m map[string]interface{}, keys ...string) float64 {
	lowerMap := make(map[string]interface{}, len(m))
	for k, v := range m {
		lowerMap[strings.ToLower(k)] = v
	}
	for _, key := range keys {
		if val, ok := lowerMap[strings.ToLower(key)]; ok {
			switch n := val.(type) {
			case float64:
				return n
			case json.Number:
				f, _ := n.Float64()
				return f
			}
		}
	}
	return 0
}

// parseFlatJudgeResponse handles legacy callers.
func parseFlatJudgeResponse(responseText string, rubric QualityRubric) (float64, string, error) {
	resp, err := parseFlatJudgeResponseDetailed(responseText, rubric)
	if err != nil {
		return 0, "", err
	}
	return resp.OverallScore, resp.Summary, nil
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

// callOpenRouterJudge sends a judge request via OpenRouter's OpenAI-compatible API.
// Supports any model available on OpenRouter (e.g. "anthropic/claude-sonnet-4").
// Includes a single retry with 5s backoff for robustness during benchmark runs.
func callOpenRouterJudge(ctx context.Context, model, userMessage, sysPrompt string) (string, error) {
	apiKey := config.GetOpenRouterAPIKey()
	if apiKey == "" {
		return "", fmt.Errorf("openRouterApiKey not set in config.json and OPENROUTER_API_KEY env var is empty")
	}

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
		Model: model,
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

	const endpoint = "https://openrouter.ai/api/v1/chat/completions"
	client := &http.Client{Timeout: 120 * time.Second}

	// Single retry with 5s backoff
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			time.Sleep(5 * time.Second)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return "", err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		respBody, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("failed to read response body: %w", readErr)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("OpenRouter API returned status %d: %s", resp.StatusCode, string(respBody))
			continue
		}

		var result struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}

		if err := json.Unmarshal(respBody, &result); err != nil {
			return "", fmt.Errorf("failed to decode OpenRouter response: %w", err)
		}

		if len(result.Choices) == 0 {
			return "", fmt.Errorf("empty choices from OpenRouter API")
		}

		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("OpenRouter judge failed after retry: %w", lastErr)
}
