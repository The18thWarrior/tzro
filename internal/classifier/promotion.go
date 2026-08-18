package classifier

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"tzro/internal/inference"
	"tzro/internal/mcp"
	"tzro/internal/memory"
)

var (
	workflowTemporalPrototypes = []string{
		"run every monday at 9am",
		"schedule a recurring task every weekday",
		"execute periodically on schedule",
		"wait 3 hours before checking results",
		"delay execution until tomorrow",
		"sleep for 10 minutes then resume",
		"recurring cron trigger every week",
	}

	workflowHitlPrototypes = []string{
		"ask for my approval before executing",
		"require human confirmation before deleting",
		"dry run and wait for sign-off",
		"notify me and pause for my approval",
		"confirm with me before making changes",
		"wait for manual confirmation to proceed",
		"wait for confirmation before running",
	}

	workflowNgramKeywords = []string{
		"run every monday", "run every tuesday", "run every wednesday", "run every thursday",
		"run every friday", "run every saturday", "run every sunday",
		"every monday", "every tuesday", "every wednesday", "every thursday",
		"every friday", "every saturday", "every sunday", "every weekday",
		"dry run", "sign-off", "sign off",
		"ask me before", "confirm before", "wait for approval", "wait for my approval",
		"wait for confirmation", "wait for my confirmation", "wait for sign-off",
	}
)

// isTemporalDelay checks for temporal delay expressions (e.g. "wait 3 days", "sleep 10 minutes") without regex.
func isTemporalDelay(lower string) bool {
	delayUnits := []string{"second", "minute", "hour", "day", "week", "month", "year"}
	delayVerbs := []string{"wait ", "delay ", "defer ", "sleep ", "after "}
	for _, verb := range delayVerbs {
		if idx := strings.Index(lower, verb); idx >= 0 {
			window := lower[idx+len(verb):]
			if len(window) > 20 {
				window = window[:20]
			}
			for _, unit := range delayUnits {
				if strings.Contains(window, unit) {
					return true
				}
			}
		}
	}
	return false
}

// ShouldPromoteToWorkflow checks neural prototypes and multi-token n-gram keywords to trigger Workflow promotion.
// Uses a strict > 0.70 cosine similarity threshold to eliminate false positive workflow promotions (ADR-0082).
func ShouldPromoteToWorkflow(prompt string) bool {
	lower := strings.ToLower(prompt)
	if isTemporalDelay(lower) {
		return true
	}
	for _, kw := range workflowNgramKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}

	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		allPrototypes := append(workflowTemporalPrototypes, workflowHitlPrototypes...)
		vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(context.Background(), append([]string{prompt}, allPrototypes...))
		if err == nil && len(vecs) == len(allPrototypes)+1 {
			promptVec := vecs[0]
			for _, protoVec := range vecs[1:] {
				sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(promptVec, protoVec)
				if sim > 0.70 {
					return true
				}
			}
		}
	}

	return false
}

// FindMatchedToolsAndSkills scans the prompt case-insensitively using isWordMatch word boundary matching.
func FindMatchedToolsAndSkills(prompt string) []string {
	var matched []string

	// 1. Hardcoded standard tools
	hardcodedTools := []string{
		"fetch_sheet_records", "dedup_contacts", "slack_confirm", "cron_trigger", "metrics_slack",
	}
	for _, t := range hardcodedTools {
		if isWordMatch(prompt, t) {
			matched = append(matched, t)
		}
	}

	// 2. MCP daemon tools
	daemons := mcp.GlobalRegistry.GetList()
	for name := range daemons {
		if isWordMatch(prompt, name) {
			matched = append(matched, name)
		}
	}

	// 3. Skills from database
	skills := memory.DB.GetSkills()
	for _, s := range skills {
		if isWordMatch(prompt, s.ID) || isWordMatch(prompt, s.Name) {
			matched = append(matched, s.ID)
		}
	}

	return matched
}

// CalculateBFSNeighborhoodToolCount retrieves the 2-hop neighborhood of all matched tools.
func CalculateBFSNeighborhoodToolCount(matchedTools []string) int {
	uniqueTools := make(map[string]bool)
	for _, toolID := range matchedTools {
		sub := memory.DB.GetEntityNeighborhood(toolID, 2)
		for _, n := range sub.Nodes {
			if n.NodeType == "tool" || n.NodeType == "skill" {
				uniqueTools[n.ID] = true
			}
		}
	}
	return len(uniqueTools)
}

// DecomposeWorkflow splits a promoted user request into WorkflowDefinition and multiple WorkflowTasks.
func DecomposeWorkflow(prompt string) (memory.WorkflowDefinition, []memory.WorkflowTask) {
	wfID := fmt.Sprintf("wf_promoted_%d", time.Now().UnixNano())
	wfDef := memory.WorkflowDefinition{
		ID:          wfID,
		Name:        "Automatically Promoted Workflow",
		Description: fmt.Sprintf("Decomposed multi-task workflow plan for: %s", prompt),
		TriggerType: "manual",
		Status:      "active",
		CreatedAt:   time.Now().Unix(),
		UpdatedAt:   time.Now().Unix(),
	}

	lower := strings.ToLower(prompt)

	var tasks []memory.WorkflowTask
	if strings.Contains(lower, "cron") || strings.Contains(lower, "every") || strings.Contains(lower, "scheduled") {
		wfDef.TriggerType = "cron"
		wfDef.TriggerConfig = "*/5 * * * *" // default 5 minutes
		tasks = []memory.WorkflowTask{
			{
				WorkflowID:     wfID,
				TaskTemplateID: "database_pulse",
				Name:           "Database Pulse Ticker",
				Instructions:   "Initialize database sync tick pulse",
				Dependencies:   "",
			},
			{
				WorkflowID:     wfID,
				TaskTemplateID: "slack_metrics",
				Name:           "Metrics Notification Alert",
				Instructions:   "Push system health heartbeats check alert",
				Dependencies:   "database_pulse",
			},
		}
	} else if strings.Contains(lower, "approval") || strings.Contains(lower, "sign-off") || strings.Contains(lower, "signoff") || strings.Contains(lower, "confirm") ||
		strings.Contains(lower, "wait") || strings.Contains(lower, "delay") || strings.Contains(lower, "sleep") || strings.Contains(lower, "defer") {
		tasks = []memory.WorkflowTask{
			{
				WorkflowID:     wfID,
				TaskTemplateID: "prepare_sync",
				Name:           "Prepare and Sync to Staging",
				Instructions:   "Run dry run preparation of data and save to temporary staging database",
				Dependencies:   "",
			},
			{
				WorkflowID:     wfID,
				TaskTemplateID: "user_approval",
				Name:           "User Approval Checkpoint",
				Instructions:   "Create a Human-in-the-Loop notification and block until the user grants explicit sign-off to proceed",
				Dependencies:   "prepare_sync",
			},
			{
				WorkflowID:     wfID,
				TaskTemplateID: "commit_sync",
				Name:           "Commit Sync and Execute Final Actions",
				Instructions:   "Upon approval, commit the data sync and run final system writes",
				Dependencies:   "user_approval",
			},
		}
	} else {
		// Default multi-step tool cap promotion
		tasks = []memory.WorkflowTask{
			{
				WorkflowID:     wfID,
				TaskTemplateID: "fetch_and_normalize",
				Name:           "Fetch and Normalize Lead Sources",
				Instructions:   "Retrieve lead list from local spreadsheets and apply sandboxed WASM normalizer rules to phone numbers",
				Dependencies:   "",
			},
			{
				WorkflowID:     wfID,
				TaskTemplateID: "multi_system_query",
				Name:           "Query and Cross-Reference System Records",
				Instructions:   "Query Salesforce CRM, Postgres backend, and publish presence reports over Slack",
				Dependencies:   "fetch_and_normalize",
			},
		}
	}

	return wfDef, tasks
}

// PromoteAndDecompose evaluates boundary heuristics and decomposes if triggered.
func PromoteAndDecompose(prompt string) (bool, memory.WorkflowDefinition, []memory.WorkflowTask) {
	matched := FindMatchedToolsAndSkills(prompt)
	toolCapTriggered := CalculateBFSNeighborhoodToolCount(matched) > 12
	semanticTriggered := ShouldPromoteToWorkflow(prompt)

	if toolCapTriggered || semanticTriggered {
		wfDef, tasks := DecomposeWorkflow(prompt)
		return true, wfDef, tasks
	}
	return false, memory.WorkflowDefinition{}, nil
}

// isWordMatch helper to perform a precise, case-insensitive word-boundary search
func isWordMatch(text, word string) bool {
	if len(word) == 0 {
		return false
	}
	textLower := strings.ToLower(text)
	wordLower := strings.ToLower(word)

	start := 0
	for {
		idx := strings.Index(textLower[start:], wordLower)
		if idx == -1 {
			return false
		}
		pos := start + idx

		// Check boundary before
		beforeWord := true
		if pos > 0 {
			rBefore := rune(textLower[pos-1])
			if unicode.IsLetter(rBefore) || unicode.IsDigit(rBefore) || rBefore == '_' {
				beforeWord = false
			}
		}

		// Check boundary after
		afterWord := true
		endPos := pos + len(wordLower)
		if endPos < len(textLower) {
			rAfter := rune(textLower[endPos])
			if unicode.IsLetter(rAfter) || unicode.IsDigit(rAfter) || rAfter == '_' {
				afterWord = false
			}
		}

		if beforeWord && afterWord {
			return true
		}

		start = pos + 1
	}
}
