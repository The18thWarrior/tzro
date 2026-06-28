// Package codegen provides the static DAG builder and context gathering
// for the tzro_code MCP tool. It constructs a hardcoded 3-node DAG
// (check_context → reason_code → write_code) that reads existing file
// context, generates code via the local model, and writes the result.
package codegen

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

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
func BuildCodePrompt(spec, filePath, language, action, existingContent string, siblings map[string]string, maxLines int) string {
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

	b.WriteString("## Rules\n")
	b.WriteString("- Output ONLY the complete file content\n")
	b.WriteString("- No markdown fences, no explanation, no commentary\n")
	b.WriteString("- If updating: output the COMPLETE updated file, not a diff\n")
	b.WriteString(fmt.Sprintf("- Maximum %d lines\n", maxLines))
	b.WriteString("- Follow the conventions visible in sibling files (naming, formatting, imports)\n")
	b.WriteString("- Include appropriate imports/package declarations\n")

	return b.String()
}
