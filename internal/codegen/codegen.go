// Package codegen provides context gathering, prompt construction, and file
// writing for the tzro_code MCP tool. Context gathering (GatherContext) and
// file writing (WriteCodeFile) are pure Go logic executed by the caller;
// only code generation (reason_code) runs through the DAG engine.
package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"tzro/internal/compiler"
	"tzro/internal/executor"
	"tzro/internal/tools"
)

// CodeContext holds the gathered context for code generation.
type CodeContext struct {
	Exists          bool              `json:"exists"`
	ExistingContent string            `json:"existingContent,omitempty"`
	Language        string            `json:"language"`
	Siblings        map[string]string `json:"siblings,omitempty"`
}

// maxSiblings is the maximum number of sibling files to include for context.
const maxSiblings = 5

// maxSiblingChars is the character budget per sibling file.
const maxSiblingChars = 6000

// maxExistingChars is the character budget for the target file's existing content.
const maxExistingChars = 15000

// languageMap maps file extensions to language identifiers.
var languageMap = map[string]string{
	".go":    "go",
	".ts":    "typescript",
	".tsx":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".py":    "python",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".rb":    "ruby",
	".c":     "c",
	".cpp":   "cpp",
	".h":     "c",
	".hpp":   "cpp",
	".cs":    "csharp",
	".swift": "swift",
	".lua":   "lua",
	".sh":    "bash",
	".bash":  "bash",
	".zsh":   "zsh",
	".sql":   "sql",
	".html":  "html",
	".css":   "css",
	".scss":  "scss",
	".yaml":  "yaml",
	".yml":   "yaml",
	".json":  "json",
	".toml":  "toml",
	".md":    "markdown",
	".proto": "protobuf",
}

// DetectLanguage infers a language identifier from a file extension.
// Returns the language string, or the raw extension (without dot) if unknown.
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	if lang, ok := languageMap[ext]; ok {
		return lang
	}
	if ext != "" {
		return ext[1:] // strip the dot
	}
	return "text"
}

// GatherContext reads the target file (if it exists) and up to 5 sibling files
// from the same directory for code generation context. Large files are truncated
// using content-aware truncation from the executor package.
func GatherContext(targetPath string, validator *tools.PathValidator) (*CodeContext, error) {
	// Validate path is within allowed roots (use write-path since file may not exist)
	resolvedPath, err := validator.ValidateWritePath(targetPath)
	if err != nil {
		return nil, fmt.Errorf("path validation failed: %w", err)
	}

	ctx := &CodeContext{
		Language: DetectLanguage(resolvedPath),
		Siblings: make(map[string]string),
	}

	// Check if target exists and read it
	info, statErr := os.Stat(resolvedPath)
	if statErr == nil {
		if info.IsDir() {
			return nil, fmt.Errorf("target path is a directory, not a file: %s", targetPath)
		}

		// Check for binary content
		data, err := os.ReadFile(resolvedPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read target file: %w", err)
		}
		if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
			return nil, fmt.Errorf("target file contains binary content: tzro_code only supports text files")
		}

		ctx.Exists = true
		content := string(data)
		// Apply content-aware truncation if large
		if len(content) > maxExistingChars {
			content = executor.TruncateToolOutput(content, maxExistingChars)
		}
		ctx.ExistingContent = content
	}

	// Read sibling files from the parent directory
	parentDir := filepath.Dir(resolvedPath)
	targetName := filepath.Base(resolvedPath)
	targetExt := strings.ToLower(filepath.Ext(resolvedPath))

	entries, err := os.ReadDir(parentDir)
	if err != nil {
		// Parent dir doesn't exist yet — no siblings, that's fine
		return ctx, nil
	}

	// Collect candidate siblings: regular files, not the target itself
	type siblingCandidate struct {
		name    string
		sameExt bool
	}
	var candidates []siblingCandidate
	for _, e := range entries {
		if e.IsDir() || e.Name() == targetName {
			continue
		}
		// Skip hidden files and common noise
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		candidates = append(candidates, siblingCandidate{
			name:    e.Name(),
			sameExt: ext == targetExt,
		})
	}

	// Sort: same extension first, then alphabetically within each group
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sameExt != candidates[j].sameExt {
			return candidates[i].sameExt // true sorts before false
		}
		return candidates[i].name < candidates[j].name
	})

	// Take up to maxSiblings
	if len(candidates) > maxSiblings {
		candidates = candidates[:maxSiblings]
	}

	// Read each sibling with truncation
	for _, c := range candidates {
		sibPath := filepath.Join(parentDir, c.name)
		data, err := os.ReadFile(sibPath)
		if err != nil {
			continue
		}
		if !utf8.Valid(data) {
			continue // skip binary siblings
		}
		content := string(data)
		if len(content) > maxSiblingChars {
			content = executor.TruncateToolOutput(content, maxSiblingChars)
		}
		ctx.Siblings[c.name] = content
	}

	return ctx, nil
}

// BuildCodePrompt assembles the structured prompt for the reason_code node.
func BuildCodePrompt(spec, filePath, language, action, existingContent string, siblings map[string]string, maxLines int, moduleContext string) string {
	var b strings.Builder

	b.WriteString("You are a code generator. Write code for a single file based on the spec.\n\n")

	b.WriteString("## Spec\n")
	b.WriteString(spec)
	b.WriteString("\n\n")

	b.WriteString("## Target File\n")
	b.WriteString(fmt.Sprintf("Path: %s\n", filePath))
	b.WriteString(fmt.Sprintf("Language: %s\n", language))
	b.WriteString(fmt.Sprintf("Action: %s\n\n", action))

	if action == "update" && existingContent != "" {
		b.WriteString("## Existing Content\n")
		b.WriteString("```\n")
		b.WriteString(existingContent)
		if !strings.HasSuffix(existingContent, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("```\n\n")
	}

	if len(siblings) > 0 {
		b.WriteString("## Sibling Files (for context — follow their conventions)\n")
		// Sort sibling names for deterministic output
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
		if !strings.HasSuffix(moduleContext, "\n") {
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// Inject reference patterns for concurrency/generics when the spec needs them
	if exemplars := LanguageExemplars(language, spec); exemplars != "" {
		b.WriteString(exemplars)
		b.WriteString("\n")
	}

	b.WriteString("## Rules\n")
	b.WriteString("- Output ONLY the complete file content\n")
	b.WriteString("- No markdown fences, no explanation, no commentary\n")
	b.WriteString("- If updating: output the COMPLETE updated file, not a diff\n")
	b.WriteString(fmt.Sprintf("- Maximum %d lines\n", maxLines))
	b.WriteString("- Follow the conventions visible in sibling files (naming, formatting, imports)\n")
	b.WriteString("- Include appropriate imports/package declarations\n")

	return b.String()
}

// StripMarkdownFences removes markdown code fences from LLM output.
// Handles both ```lang and ``` patterns. If the content starts with a fence
// and ends with a fence, only the inner content is returned.
func StripMarkdownFences(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 {
		return content
	}

	firstLine := strings.TrimSpace(lines[0])
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	// Check for opening fence
	hasOpenFence := strings.HasPrefix(firstLine, "```")
	hasCloseFence := lastLine == "```"

	if hasOpenFence && hasCloseFence {
		// Strip first and last lines
		inner := lines[1 : len(lines)-1]
		return strings.Join(inner, "\n") + "\n"
	}

	// If only the opening fence is present (model forgot to close)
	if hasOpenFence && !hasCloseFence {
		inner := lines[1:]
		return strings.Join(inner, "\n") + "\n"
	}

	return content
}

// StripExecutionTierPrefix removes known execution tier prefixes from content.
// These prefixes are observability metadata added by the executor strategies
// (e.g., [Local Tactician], [Cloud Fallback]) and must not appear in
// consumer-facing output or generated code files.
func StripExecutionTierPrefix(content string) string {
	prefixes := []string{
		"[Local Tactician] ",
		"[Cloud Fallback] ",
		"[Recall] ",
		"[Local] ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(content, p) {
			return content[len(p):]
		}
	}
	return content
}

// CleanGeneratedCode strips execution tier prefixes, markdown fences, and
// validates line count. Returns the cleaned content and an error if the line
// count exceeds maxLines.
func CleanGeneratedCode(rawContent string, maxLines int) (string, error) {
	// Strip execution tier prefix before fence stripping — the prefix
	// appears at position 0, before any markdown fences or code content.
	content := StripExecutionTierPrefix(rawContent)
	content = StripMarkdownFences(content)

	// Count lines
	lineCount := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lineCount++
	}

	if maxLines > 0 && lineCount > maxLines {
		return "", fmt.Errorf(
			"generated code exceeds maximum line count (%d lines > %d max). "+
				"Consider breaking this file into smaller, single-responsibility files",
			lineCount, maxLines,
		)
	}

	return content, nil
}

// BuildCodeDAG constructs the execution graph for code generation.
//
// When codeCtx is provided (non-nil), the context has been pre-computed by the
// caller via GatherContext. The DAG is a single reason_code node with the full
// prompt baked in. The caller handles file writing post-execution via WriteCodeFile.
//
// When codeCtx is nil, the legacy 3-node DAG is built (check_context → reason_code
// → write_code) where the executor uses inference to extract tool arguments.
// This path is deprecated and should not be used for new code.
func BuildCodeDAG(taskID, spec, filePath, language string, maxLines int, codeCtx *CodeContext) *compiler.ExecutionGraph {
	// Pre-computed context path: single reason_code node with full prompt
	if codeCtx != nil {
		action := "create"
		if codeCtx.Exists {
			action = "update"
		}
		if codeCtx.Language != "" {
			language = codeCtx.Language
		}

		moduleContext := DiscoverModuleContext(filePath, language)
		fullPrompt := BuildCodePrompt(spec, filePath, language, action,
			codeCtx.ExistingContent, codeCtx.Siblings, maxLines, moduleContext)

		return &compiler.ExecutionGraph{
			TaskID:     taskID,
			CreatedAt:  time.Now().Unix(),
			MaxCycles:  1,
			GoalPrompt: fmt.Sprintf("Generate compilable %s code for %s: %s", language, filePath, spec),
			MutationBudget: &compiler.MutationBudget{
				MaxSpawns:       1,
				RemainingSpawns: 1,
			},
			Nodes: []compiler.GraphNode{
				{
					ID:             "reason_code",
					Type:           "synthesis",
					Instructions:   fullPrompt,
					AllowedTools:   []string{},
					Status:         "pending",
					OutputFormat:   "source_code",
					OutputLanguage: language,
				},
				{
					ID:                  "validate_code",
					Type:                "synthesis",
					Instructions:        fmt.Sprintf("Validate that the generated %s code compiles successfully.", language),
					AllowedTools:        []string{},
					Status:              "pending",
					ActivationThreshold: 0.9,
				},
			},
			Edges: []compiler.GraphEdge{
				{SourceID: "reason_code", TargetID: "validate_code"},
			},
		}
	}

	// Legacy 3-node path (deprecated): inference-based argument extraction.
	action := "create"

	checkInstructions := fmt.Sprintf(
		"Read the file at %s if it exists. Also read up to 5 sibling files in the same directory for context. "+
			"Return the file content and language information. If the file doesn't exist, note that this is a new file creation.",
		filePath,
	)

	reasonInstructions := fmt.Sprintf(
		"Based on the context from check_context, generate code for the following spec.\n\n"+
			"Spec: %s\n\n"+
			"Target: %s\nLanguage: %s\nAction: %s\nMax Lines: %d\n\n"+
			"Output ONLY the raw file content. No markdown fences, no explanation.",
		spec, filePath, language, action, maxLines,
	)

	writeInstructions := fmt.Sprintf(
		"Write the generated code from reason_code to %s. "+
			"Strip any markdown code fences (```...```) if present in the output. "+
			"Verify the output does not exceed %d lines. If it does, fail with an error.",
		filePath, maxLines,
	)

	return &compiler.ExecutionGraph{
		TaskID:     taskID,
		CreatedAt:  time.Now().Unix(),
		MaxCycles:  1,
		GoalPrompt: fmt.Sprintf("Generate code for %s: %s", filePath, spec),
		Nodes: []compiler.GraphNode{
			{
				ID:           "check_context",
				Type:         "deterministic",
				Action:       "read_file",
				Instructions: checkInstructions,
				AllowedTools: []string{"read_file", "list_dir"},
				Status:       "pending",
			},
			{
				ID:           "reason_code",
				Type:         "action",
				Instructions: reasonInstructions,
				AllowedTools: []string{},
				Status:       "pending",
			},
			{
				ID:           "write_code",
				Type:         "deterministic",
				Action:       "write_file",
				Instructions: writeInstructions,
				AllowedTools: []string{"write_file"},
				Status:       "pending",
			},
		},
		Edges: []compiler.GraphEdge{
			{SourceID: "check_context", TargetID: "reason_code"},
			{SourceID: "reason_code", TargetID: "write_code"},
		},
	}
}

// WriteCodeFile handles post-DAG file writing: strips markdown fences,
// validates line count, backs up existing files, and writes to disk.
// Returns the action taken ("created" or "updated") and lines written.
func WriteCodeFile(filePath, rawContent string, maxLines int) (action string, linesWritten int, err error) {
	content, err := CleanGeneratedCode(rawContent, maxLines)
	if err != nil {
		return "", 0, err
	}

	if content == "" {
		return "", 0, fmt.Errorf("model produced no code output")
	}

	// Determine action and backup if overwriting
	action = "created"
	if _, statErr := os.Stat(filePath); statErr == nil {
		action = "updated"
		if backupErr := tools.BackupFile(filePath); backupErr != nil {
			fmt.Fprintf(os.Stderr, "[codegen] Backup failed (non-fatal): %v\n", backupErr)
		}
	}

	// Create parent directories
	if mkdirErr := os.MkdirAll(filepath.Dir(filePath), 0755); mkdirErr != nil {
		return "", 0, fmt.Errorf("failed to create parent directories: %w", mkdirErr)
	}

	// Write file
	if writeErr := os.WriteFile(filePath, []byte(content), 0644); writeErr != nil {
		return "", 0, fmt.Errorf("failed to write file: %w", writeErr)
	}

	lines := strings.Count(content, "\n")
	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		lines++
	}

	return action, lines, nil
}

// SanitizeSourceCode strips leading conversational preamble and markdown artifacts
// before the code begins, ensuring that generated files start immediately with valid code tokens.
func SanitizeSourceCode(raw string, language string) string {
	cleaned := StripMarkdownFences(raw)
	lines := strings.Split(cleaned, "\n")

	lang := strings.ToLower(language)
	startIdx := 0

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		switch lang {
		case "go":
			if strings.HasPrefix(trimmed, "package ") {
				startIdx = i
				goto found
			}
			if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
				// Look ahead to check if package declaration follows
				for j := i + 1; j < len(lines); j++ {
					t := strings.TrimSpace(lines[j])
					if strings.HasPrefix(t, "package ") {
						startIdx = i
						goto found
					}
				}
			}

		case "typescript", "javascript", "ts", "js":
			if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "export ") ||
				strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "interface ") ||
				strings.HasPrefix(trimmed, "const ") || strings.HasPrefix(trimmed, "let ") ||
				strings.HasPrefix(trimmed, "var ") || strings.HasPrefix(trimmed, "class ") ||
				strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "//") ||
				strings.HasPrefix(trimmed, "/*") {
				startIdx = i
				goto found
			}

		case "python", "py":
			if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "from ") ||
				strings.HasPrefix(trimmed, "def ") || strings.HasPrefix(trimmed, "class ") ||
				strings.HasPrefix(trimmed, "\"\"\"") || strings.HasPrefix(trimmed, "'''") ||
				strings.HasPrefix(trimmed, "#") {
				startIdx = i
				goto found
			}

		case "rust", "rs":
			if strings.HasPrefix(trimmed, "use ") || strings.HasPrefix(trimmed, "pub ") ||
				strings.HasPrefix(trimmed, "fn ") || strings.HasPrefix(trimmed, "struct ") ||
				strings.HasPrefix(trimmed, "enum ") || strings.HasPrefix(trimmed, "mod ") ||
				strings.HasPrefix(trimmed, "//") {
				startIdx = i
				goto found
			}

		default:
			// For generic languages, strip common conversational intro phrases
			lower := strings.ToLower(trimmed)
			if !strings.HasPrefix(lower, "here is") &&
				!strings.HasPrefix(lower, "sure") &&
				!strings.HasPrefix(lower, "below is") &&
				!strings.HasPrefix(lower, "certainly") {
				startIdx = i
				goto found
			}
		}
	}

found:
	if startIdx > 0 && startIdx < len(lines) {
		return strings.Join(lines[startIdx:], "\n")
	}
	return cleaned
}

