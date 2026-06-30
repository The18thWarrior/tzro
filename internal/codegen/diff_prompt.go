package codegen

import (
	"fmt"
	"sort"
	"strings"
)

// BuildDiffPrompt assembles the prompt for the reason_code node in diff mode.
// Unlike BuildCodePrompt (which asks for complete file output), this prompt
// asks the model to output only the changed hunks in structured JSON format.
func BuildDiffPrompt(spec, filePath, language, existingContent string,
	siblings map[string]string) string {

	var b strings.Builder

	b.WriteString("You are a precise code editor. Apply the requested changes to an existing file\n")
	b.WriteString("using structured edit hunks.\n\n")

	b.WriteString("## Spec\n")
	b.WriteString(spec)
	b.WriteString("\n\n")

	b.WriteString("## Target File\n")
	b.WriteString(fmt.Sprintf("Path: %s\n", filePath))
	b.WriteString(fmt.Sprintf("Language: %s\n\n", language))

	b.WriteString("## Current File Content\n")
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

	b.WriteString("## Rules\n")
	b.WriteString("- Output a JSON object with a \"hunks\" array\n")
	b.WriteString("- Each hunk has \"searchContent\" (exact text to find) and \"replaceContent\" (replacement)\n")
	b.WriteString("- searchContent MUST be an exact substring of the current file content\n")
	b.WriteString("- Include enough context lines in searchContent to ensure uniqueness\n")
	b.WriteString("- Order hunks from top of file to bottom\n")
	b.WriteString("- For insertions, use a nearby line as searchContent and include it in replaceContent\n")
	b.WriteString("- For deletions, set replaceContent to empty string\n")

	return b.String()
}
