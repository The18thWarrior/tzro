package skills

// corrective_skill.go — Corrective Micro-Skill Extraction (ADR-0020).
//
// When a local model fails to extract correct parameters but the cloud model
// succeeds, this module extracts a concise corrective instruction from the
// diff between the two outputs. The corrective skill is persisted to the
// synthesized_skills table and injected into the tactician's context pipeline
// on future executions involving the same tool.

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tzro/internal/embeddings"
	"tzro/internal/inference"
	"tzro/internal/memory"
)

// correctiveExtractionSchema constrains the cloud model's corrective skill output.
const correctiveExtractionSchema = `{
	"type": "object",
	"properties": {
		"correction": {
			"type": "string",
			"description": "A single imperative sentence describing the corrective instruction"
		},
		"error_category": {
			"type": "string",
			"description": "Brief category of the error (e.g. quoting, type_mismatch, missing_field, wrong_format)"
		}
	},
	"required": ["correction", "error_category"]
}`

// ExtractCorrectiveSkill analyzes the diff between a failed local model output
// and a successful cloud model output, and extracts a concise corrective instruction
// that can prevent the same class of error in the future.
//
// The corrective skill is persisted as a micro-skill in the database and will be
// available in the tactician's context pipeline for future executions.
func ExtractCorrectiveSkill(ctx context.Context, toolName string, localOutput string, cloudOutput string, instruction string) (*memory.Skill, error) {
	// Build the extraction prompt
	systemPrompt := "You are the Corrective Skill Extractor for the tzro execution engine. " +
		"Analyze the difference between a failed local model output and a successful cloud model output, " +
		"then extract a single, specific corrective instruction that would prevent this class of error."

	userPrompt := fmt.Sprintf(`The local model produced the following output for tool '%s':
%s

The correct output was:
%s

Original instruction: %s

Extract a concise, specific corrective instruction that would prevent this class of error.
The correction should be a single imperative sentence (e.g., 'Use double quotes for SOQL string values').
Focus on the SPECIFIC pattern that caused the failure — do not write generic advice.`, toolName, localOutput, cloudOutput, instruction)

	msgs := []inference.InferenceMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt},
	}

	result, err := inference.CallCloudModel(ctx, msgs, correctiveExtractionSchema)
	if err != nil {
		return nil, fmt.Errorf("corrective skill extraction failed: %w", err)
	}

	var extraction struct {
		Correction    string `json:"correction"`
		ErrorCategory string `json:"error_category"`
	}
	if err := json.Unmarshal([]byte(result), &extraction); err != nil {
		return nil, fmt.Errorf("failed to parse corrective extraction: %w", err)
	}

	if extraction.Correction == "" {
		return nil, fmt.Errorf("empty corrective instruction extracted")
	}

	skill := &memory.Skill{
		Name:               fmt.Sprintf("Corrective: %s — %s", toolName, truncateString(extraction.Correction, 60)),
		TriggerDescription: fmt.Sprintf("When extracting parameters for %s: %s", toolName, extraction.ErrorCategory),
		SOPContent:         fmt.Sprintf("## Corrective Instruction\n\n%s\n\n## Error Category\n\n%s\n\n## Context\n\nThis corrective skill was auto-extracted from a failed local model execution for tool `%s`. The local model's output did not match the expected schema, but the cloud model succeeded.\n\n**Apply this correction when extracting parameters for `%s`.**", extraction.Correction, extraction.ErrorCategory, toolName, toolName),
		CreatedAt:          time.Now().Unix(),
	}

	// Deduplicate via cosine similarity against existing skills
	existingSkills := memory.DB.GetSkills()
	for _, ext := range existingSkills {
		if embeddings.CosineSimilarity(skill.TriggerDescription, ext.TriggerDescription) >= 0.85 {
			// Near-duplicate found — skip insertion
			fmt.Printf("[CorrectiveSkill] Skipping duplicate corrective skill for %s (similar to existing skill: %s)\n", toolName, ext.Name)
			return &ext, nil
		}
	}

	if err := memory.DB.AddSkill(skill); err != nil {
		return nil, fmt.Errorf("failed to persist corrective skill: %w", err)
	}

	fmt.Printf("[CorrectiveSkill] Extracted and persisted corrective skill for %s: %s\n", toolName, extraction.Correction)
	return skill, nil
}
