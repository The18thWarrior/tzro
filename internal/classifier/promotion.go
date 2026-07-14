package classifier

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"tzro/internal/mcp"
	"tzro/internal/memory"
)

var (
	temporalRegexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(wait|delay|defer|sleep|after)\s+(\d+|a|an)\s+(second|minute|hour|day|week|month|year)s?`),
		regexp.MustCompile(`(?i)(run\s+every\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday)|every\s+(monday|tuesday|wednesday|thursday|friday|saturday|sunday))`),
		regexp.MustCompile(`(?i)(wait\s+until|until\s+the|wait\s+for\s+the)`),
	}
	temporalKeywords = []string{
		"every tuesday", "every monday", "every wednesday", "every thursday", "every friday", "every saturday", "every sunday",
	}

	hitlRegexes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)dry\s*run`),
		regexp.MustCompile(`(?i)approval|sign\s*off|sign-off`),
		regexp.MustCompile(`(?i)confirm\s+before|ask\s+me\s+before`),
		regexp.MustCompile(`(?i)wait\s+for\s+(my\s+)?(approval|sign\s*off)`),
		regexp.MustCompile(`(?i)notify\s+me\s+and\s+wait`),
		regexp.MustCompile(`(?i)wait\s+for\s+my\s+(confirmation|sign-off|signoff|approval)`),
	}
)

// ShouldPromoteToWorkflow checks regex and keyword heuristics to trigger Workflow promotion.
func ShouldPromoteToWorkflow(prompt string) bool {
	lower := strings.ToLower(prompt)
	for _, r := range temporalRegexes {
		if r.MatchString(prompt) {
			return true
		}
	}
	for _, kw := range temporalKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	for _, r := range hitlRegexes {
		if r.MatchString(prompt) {
			return true
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
