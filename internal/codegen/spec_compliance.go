package codegen

import (
	"fmt"
	"strings"
)

// SpecComplianceResult holds the result of a spec compliance evaluation.
type SpecComplianceResult struct {
	// Pass is true if all spec requirements are implemented.
	Pass bool
	// MissingRequirements lists the requirements that are not implemented.
	MissingRequirements []string
	// Checklist is the full structured checklist from the evaluator.
	Checklist string
}

// BuildComplianceEvalPrompt constructs a prompt for the Local Model to evaluate
// whether generated code implements all requirements from the spec.
// The model produces a structured IMPLEMENTED/MISSING checklist per requirement.
func BuildComplianceEvalPrompt(generatedCode, spec, language string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are a %s code compliance evaluator. Evaluate whether the code below implements ALL requirements from the spec.\n\n", language))

	sb.WriteString("## Rules\n")
	sb.WriteString("1. For each numbered requirement in the spec, classify as IMPLEMENTED or MISSING\n")
	sb.WriteString("2. Follow each classification with a one-line reason\n")
	sb.WriteString("3. Use the exact format: 'N. REQUIREMENT: IMPLEMENTED|MISSING - reason'\n")
	sb.WriteString("4. Be thorough — check for both presence AND correctness of implementation\n\n")

	sb.WriteString("## Spec\n")
	sb.WriteString(spec)
	if !strings.HasSuffix(spec, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	sb.WriteString(fmt.Sprintf("## Generated Code\n```%s\n", language))
	sb.WriteString(generatedCode)
	if !strings.HasSuffix(generatedCode, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("```\n\n")

	sb.WriteString("## Output\n")
	sb.WriteString("Evaluate each spec requirement:\n")

	return sb.String()
}

// BuildRegenerationPrompt constructs a prompt for full code regeneration after
// spec compliance failure. Unlike BuildRepairPrompt (which says "don't add new
// features"), this prompt instructs the model to implement ALL requirements.
//
// ADR-0061: Used when the Spec Compliance Gate detects missing requirements.
func BuildRegenerationPrompt(spec, checklist, language string, maxLines int, moduleContext string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("You are a %s code generator. Generate a complete implementation that satisfies ALL requirements from the spec.\n\n", language))

	sb.WriteString("## Rules\n")
	sb.WriteString(fmt.Sprintf("1. Output ONLY the complete %s source file — no markdown fences, no explanations\n", language))
	sb.WriteString("2. Implement ALL requirements listed in the spec below\n")
	sb.WriteString("3. Pay special attention to the MISSING requirements from the compliance checklist\n")
	sb.WriteString(fmt.Sprintf("4. Keep the output under %d lines\n", maxLines))
	sb.WriteString("5. Use only the standard library unless the spec explicitly requires external packages\n\n")

	sb.WriteString("## Spec (implement ALL requirements)\n")
	sb.WriteString(spec)
	if !strings.HasSuffix(spec, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	sb.WriteString("## Compliance Checklist (previous attempt)\n")
	sb.WriteString("The following checklist shows which requirements were implemented and which were MISSING in a previous attempt. Ensure ALL MISSING requirements are implemented:\n\n")
	sb.WriteString(checklist)
	if !strings.HasSuffix(checklist, "\n") {
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	if moduleContext != "" {
		sb.WriteString("## Available Packages\n")
		sb.WriteString(moduleContext)
		if !strings.HasSuffix(moduleContext, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// ParseComplianceChecklist parses a structured compliance checklist from model
// output. Each line should follow the format:
//
//	N. REQUIREMENT: IMPLEMENTED|MISSING - reason
//
// Returns a SpecComplianceResult with Pass=true only if all items are IMPLEMENTED.
func ParseComplianceChecklist(output string) *SpecComplianceResult {
	result := &SpecComplianceResult{
		Pass:      true,
		Checklist: output,
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		upper := strings.ToUpper(line)

		// Check for MISSING markers
		if strings.Contains(upper, "MISSING") {
			result.Pass = false
			// Extract the requirement description
			// Format: "N. REQUIREMENT: MISSING - reason"
			requirement := extractRequirementFromLine(line)
			if requirement != "" {
				result.MissingRequirements = append(result.MissingRequirements, requirement)
			}
		}
	}

	return result
}

// extractRequirementFromLine extracts the requirement name from a compliance
// checklist line. Handles formats like:
//   - "1. SELECT: MISSING - Build() does not emit SELECT"
//   - "SELECT: MISSING"
//   - "2. MISSING - no JOIN support"
func extractRequirementFromLine(line string) string {
	// Try to extract text between number prefix and colon
	// Remove leading number + dot
	cleaned := line
	for i, c := range cleaned {
		if c == '.' && i > 0 {
			cleaned = strings.TrimSpace(cleaned[i+1:])
			break
		}
		if c < '0' || c > '9' {
			break
		}
	}

	// Find the first occurrence of MISSING or IMPLEMENTED and take everything before it
	upper := strings.ToUpper(cleaned)
	if idx := strings.Index(upper, "MISSING"); idx > 0 {
		before := strings.TrimSpace(cleaned[:idx])
		// Remove trailing colon or dash
		before = strings.TrimRight(before, ":- ")
		if before != "" {
			return before
		}
	}

	return cleaned
}
