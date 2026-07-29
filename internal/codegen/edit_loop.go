package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"tzro/internal/inference"
)

// EditLoopEngine abstracts inference for testability.
// In production, wraps CallWorker. In tests, returns canned JSON.
type EditLoopEngine interface {
	Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error)
}

// DefaultEditLoopEngine wraps the worker sidecar for production Edit Loop steps.
// Uses the worker (not router) because code editing requires the larger model's
// quality for accurate searchContent generation and code generation.
type DefaultEditLoopEngine struct{}

func (d *DefaultEditLoopEngine) Infer(ctx context.Context, systemPrompt, userPrompt, jsonSchema string) (string, error) {
	result, err := inference.CallWorker(ctx, []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}, jsonSchema)
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

// EditHunkStep is the GBNF-constrained JSON output for each hunk step.
type EditHunkStep struct {
	SearchContent  string `json:"searchContent"`
	ReplaceContent string `json:"replaceContent"`
	Done           bool   `json:"done"`
}

// editHunkSchema is the JSON schema for GBNF grammar constraint on each hunk step.
const editHunkSchema = `{
  "type": "object",
  "properties": {
    "searchContent": { "type": "string" },
    "replaceContent": { "type": "string" },
    "done": { "type": "boolean" }
  },
  "required": ["searchContent", "replaceContent", "done"]
}`

// maxEditSteps is the budget guard for the Edit Loop.
// If the model hasn't signaled done by this step, force-stop.
const maxEditSteps = 15

// RunEditLoop replaces monolithic full-file generation with a bounded
// plan-then-edit loop that produces one structured hunk per inference step.
//
// Step 0: PLAN — model reads spec + file and lists discrete changes (unconstrained prose).
// Steps 1..N (max 15): model produces one hunk per step with GBNF constraint.
// Each hunk is applied via applyOneHunk(), updating the working content.
// When the model sets done=true, the loop exits.
//
// Returns the final patched file content.
func RunEditLoop(
	ctx context.Context,
	engine EditLoopEngine,
	spec, filePath, existingContent, language string,
	siblings map[string]string,
	moduleContext string,
) (string, error) {
	if existingContent == "" {
		return "", fmt.Errorf("edit loop requires existing file content; use full mode for new files")
	}

	// Step 0: PLAN
	planSystemPrompt := buildPlanSystemPrompt()
	planUserPrompt := buildPlanUserPrompt(spec, filePath, language, existingContent, siblings, moduleContext)

	plan, err := engine.Infer(ctx, planSystemPrompt, planUserPrompt, "")
	if err != nil {
		return "", fmt.Errorf("edit loop plan step failed: %w", err)
	}

	// Steps 1..N: HUNK APPLICATION
	workingContent := existingContent

	for step := 1; step <= maxEditSteps; step++ {
		hunkSystemPrompt := buildHunkSystemPrompt(step)
		hunkUserPrompt := buildHunkUserPrompt(plan, workingContent)

		raw, err := engine.Infer(ctx, hunkSystemPrompt, hunkUserPrompt, editHunkSchema)
		if err != nil {
			return "", fmt.Errorf("edit loop step %d failed: %w", step, err)
		}

		var hunkStep EditHunkStep
		if err := json.Unmarshal([]byte(raw), &hunkStep); err != nil {
			return "", fmt.Errorf("edit loop step %d: invalid JSON: %w", step, err)
		}

		// Apply the hunk
		hunk := DiffHunk{
			SearchContent:  hunkStep.SearchContent,
			ReplaceContent: hunkStep.ReplaceContent,
		}
		patched, err := applyOneHunk(workingContent, hunk, step-1)
		if err != nil {
			return "", fmt.Errorf("edit loop step %d: hunk application failed: %w", step, err)
		}
		workingContent = patched

		if hunkStep.Done {
			break
		}
	}

	return workingContent, nil
}

// --- Prompt builders (private) ---

func buildPlanSystemPrompt() string {
	return "You are a code editor. Read the spec and the existing file. " +
		"List each discrete change needed as a numbered plan. " +
		"Be specific about what to search for and what to replace it with. " +
		"Output only the plan as prose — no code, no JSON."
}

func buildPlanUserPrompt(spec, filePath, language, existingContent string, siblings map[string]string, moduleContext string) string {
	var b strings.Builder

	b.WriteString("## Spec\n")
	b.WriteString(spec)
	b.WriteString("\n\n")

	b.WriteString(fmt.Sprintf("## Target File: %s (%s)\n", filePath, language))
	b.WriteString("```\n")
	b.WriteString(existingContent)
	if !strings.HasSuffix(existingContent, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n\n")

	if len(siblings) > 0 {
		b.WriteString("## Sibling Files (for context)\n")
		names := make([]string, 0, len(siblings))
		for name := range siblings {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			b.WriteString(fmt.Sprintf("### %s\n```\n%s", name, siblings[name]))
			if !strings.HasSuffix(siblings[name], "\n") {
				b.WriteString("\n")
			}
			b.WriteString("```\n\n")
		}
	}

	if moduleContext != "" {
		b.WriteString("## Available Packages\n")
		b.WriteString(moduleContext)
		b.WriteString("\n\n")
	}

	return b.String()
}

func buildHunkSystemPrompt(step int) string {
	return fmt.Sprintf(
		"You are applying change step %d. Produce a single edit hunk as JSON. "+
			"searchContent must be an exact substring of the current file content. "+
			"replaceContent is the replacement text. "+
			"Set done=true if this is the last change needed, false otherwise.",
		step,
	)
}

func buildHunkUserPrompt(plan, currentContent string) string {
	var b strings.Builder

	b.WriteString("## Plan\n")
	b.WriteString(plan)
	b.WriteString("\n\n")

	b.WriteString("## Current File Content\n")
	b.WriteString("```\n")
	b.WriteString(currentContent)
	if !strings.HasSuffix(currentContent, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("```\n")

	return b.String()
}
