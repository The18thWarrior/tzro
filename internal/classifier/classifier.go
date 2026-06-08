package classifier

import (
	"context"
	"encoding/json"

	"tzro/internal/inference"
)

type IntentResult struct {
	Type       string                 `json:"type"` // "chat" | "workflow" | "research" | "heartbeat" | "mission"
	Confidence float64                `json:"confidence"`
	Summary    string                 `json:"summary"`
	Params     map[string]interface{} `json:"params"`
}

const IntentSystemPrompt = `You are an intent classification agent for the X platform. Your job is to classify a user's natural language request into exactly one of five entity types and extract the necessary parameters to create that entity.

## Entity Types
1. chat — Conversational AI session for questions, data queries, or general assistance. Default fallback.
   Params: { "title": "<short descriptive title>", "firstMessage": "<user text>" }
2. workflow — Multi-agent orchestrated task with a goal, objective, and optional schedule.
   Params: { "name": "<short name>", "goal": "<high-level goal>", "objective": "<optional detailed objective>" }
3. research — Deep research session for comprehensive, multi-source analysis.
   Params: { "query": "<research question>", "depth": "shallow|standard|deep" }
4. heartbeat — Scheduled recurring task running on a cron schedule.
   Params: { "name": "<task name>", "cronExpression": "<5-field cron>", "prompt": "<instructions>", "taskType": "prompt|prompt_tool" }
5. mission — Persistent, long-running business goal coordinating sub-agents over weeks.
   Params: { "name": "<short name>", "goal": "<the high-level goal in full detail>" }

## Rules
- Respond with ONLY valid JSON matching the schema below. No markdown fences.
- If intent is ambiguous, default to "chat".

## Response Schema
{
  "type": "chat" | "workflow" | "research" | "heartbeat" | "mission",
  "confidence": 0.0-1.0,
  "summary": "Plain English summary of what will be created",
  "params": { ... type-specific parameters ... }
}`

const IntentResultSchema = `{
  "type": "object",
  "properties": {
    "type": {
      "type": "string",
      "enum": ["chat", "workflow", "research", "heartbeat", "mission"]
    },
    "confidence": {
      "type": "number",
      "minimum": 0.0,
      "maximum": 1.0
    },
    "summary": {
      "type": "string"
    },
    "params": {
      "type": "object"
    }
  },
  "required": ["type", "confidence", "summary", "params"]
}`

const ComplexitySystemPrompt = `You are a complexity classification agent for the X platform. Your job is to classify the complexity of the user's natural language request.

## Complexity Tiers
- T0: Direct. Simple creative, conversational, or direct tool queries requiring zero or one tool call.
- T1: Planned. Sequential, multi-step queries, or operations referencing multiple tools.
- T2: Supervised. High-risk writes, bulk edits, deletions, or system migrations.

Respond with ONLY valid JSON matching the schema below:
{
  "complexity": "T0" | "T1" | "T2"
}`

const ComplexitySchema = `{
  "type": "object",
  "properties": {
    "complexity": {
      "type": "string",
      "enum": ["T0", "T1", "T2"]
    }
  },
  "required": ["complexity"]
}`

// Classify delegating to unified ExecuteStructured seam in inference package
func Classify(ctx context.Context, prompt string, localModel *inference.LocalModelManager) IntentResult {
	// First, check if the prompt triggers Workflow Promotion
	matched := FindMatchedToolsAndSkills(prompt)
	toolCapTriggered := CalculateBFSNeighborhoodToolCount(matched) > 12
	semanticTriggered := ShouldPromoteToWorkflow(prompt)

	if toolCapTriggered || semanticTriggered {
		wfDef, tasks := DecomposeWorkflow(prompt)
		tasksJSON, _ := json.Marshal(tasks)
		return IntentResult{
			Type:       "workflow",
			Confidence: 1.0,
			Summary:    "Automatically promoted to durable Multi-Task Workflow based on boundary triggers",
			Params: map[string]interface{}{
				"id":            wfDef.ID,
				"name":          wfDef.Name,
				"description":   wfDef.Description,
				"triggerType":   wfDef.TriggerType,
				"triggerConfig": wfDef.TriggerConfig,
				"status":        wfDef.Status,
				"tasks":         string(tasksJSON),
				"promoted":      true,
			},
		}
	}

	req := inference.NewSimpleRequest(IntentSystemPrompt, prompt, IntentResultSchema)

	resContent, err := localModel.ExecuteStructured(ctx, req)
	if err == nil {
		var result IntentResult
		if json.Unmarshal([]byte(resContent), &result) == nil {
			return result
		}
	}

	// Ultimate fallback if ExecuteStructured or unmarshaling fails
	return IntentResult{
		Type:       "chat",
		Confidence: 0.99,
		Summary:    "Standard conversational messaging lookup",
		Params: map[string]interface{}{
			"title":        "Conversational Query",
			"firstMessage": prompt,
		},
	}
}

// ClassifyComplexity delegating to unified ExecuteStructured seam in inference package
func ClassifyComplexity(ctx context.Context, prompt string, toolNames []string, localModel *inference.LocalModelManager) string {
	// Check if the prompt triggers Workflow Promotion
	matched := FindMatchedToolsAndSkills(prompt)
	toolCapTriggered := CalculateBFSNeighborhoodToolCount(matched) > 12 || CalculateBFSNeighborhoodToolCount(toolNames) > 12
	semanticTriggered := ShouldPromoteToWorkflow(prompt)

	if toolCapTriggered || semanticTriggered {
		return "T2"
	}

	req := inference.NewSimpleRequest(ComplexitySystemPrompt, prompt, ComplexitySchema)
	req.ToolNames = toolNames

	resContent, err := localModel.ExecuteStructured(ctx, req)
	if err == nil {
		var result struct {
			Complexity string `json:"complexity"`
		}
		if json.Unmarshal([]byte(resContent), &result) == nil {
			if result.Complexity == "T0" || result.Complexity == "T1" || result.Complexity == "T2" {
				return result.Complexity
			}
		}
	}

	// Default fallback
	return "T0"
}
