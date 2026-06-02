package skills

import (
	"fmt"
	"strings"
	"time"
	"tzro/internal/embeddings"
	"tzro/internal/memory"
)

// SynthesizeSOP processes a completed workflow execution graph and commits an SOP Skill to the database.
func SynthesizeSOP(taskID string, goal string, nodes []memory.NodeState) (memory.Skill, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Standard Operating Procedure: %s\n\n", goal))
	sb.WriteString("## Trigger Context\n")
	sb.WriteString(fmt.Sprintf("This SOP was automatically synthesized by the **tzro** engine from task execution `%s` on %s.\n\n", taskID, time.Now().Format(time.RFC822)))

	sb.WriteString("## Sequence of Operations (SOP)\n")
	for i, node := range nodes {
		sb.WriteString(fmt.Sprintf("### Step %d: Action `%s`\n", i+1, node.NodeID))
		sb.WriteString(fmt.Sprintf("- **Final Status**: %s\n", node.Status))
		sb.WriteString("- **Observed Output Trace**:\n")

		outputLines := strings.Split(node.Output, "\n")
		hasLines := false
		for _, line := range outputLines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				sb.WriteString(fmt.Sprintf("  > %s\n", trimmed))
				hasLines = true
			}
		}
		if !hasLines {
			sb.WriteString("  > [No raw output logged]\n")
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## Synthesis Recommendations\n")
	sb.WriteString("1. **Automatic Recall**: This SOP can be queried via Graph-RAG neighborhood searches next time a similar query is triggered.\n")
	sb.WriteString("2. **Compaction Guardrails**: Large data blocks passing through these nodes should be compacted using the 5-layer pipeline.\n")

	skill := memory.Skill{
		Name:               fmt.Sprintf("Automated SOP: %s", truncateString(goal, 40)),
		TriggerDescription: fmt.Sprintf("Submitting requests related to: %s", goal),
		SOPContent:         sb.String(),
		CreatedAt:          time.Now().Unix(),
	}

	// Retrieve existing skills and check for semantic duplicates
	existingSkills := memory.DB.GetSkills()
	for _, ext := range existingSkills {
		if embeddings.CosineSimilarity(skill.TriggerDescription, ext.TriggerDescription) >= 0.8 {
			// Abort insertion and return existing skill to avoid duplicates
			return ext, nil
		}
	}

	err := memory.DB.AddSkill(&skill)
	return skill, err
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
