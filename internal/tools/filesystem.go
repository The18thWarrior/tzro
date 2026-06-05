package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// NewReadFileTool creates the read_file tool.
// Reads file content with optional startLine/endLine parameters.
// Caps at 200 lines per call. Bypasses the Compaction Pipeline — source code
// is injected raw per ADR-0019.
func NewReadFileTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "read_file",
		description: "Read file content with optional line range. Returns raw content (max 200 lines per call).",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"path":      map[string]interface{}{"type": "string"},
			"startLine": map[string]interface{}{"type": "integer"},
			"endLine":   map[string]interface{}{"type": "integer"},
		}, []string{"path"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Path      string `json:"path"`
				StartLine *int   `json:"startLine"`
				EndLine   *int   `json:"endLine"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			resolvedPath, err := validator.ValidatePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			// Read the file
			file, err := os.Open(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to open file: %v", err)), nil
			}
			defer file.Close()

			scanner := bufio.NewScanner(file)
			var allLines []string
			for scanner.Scan() {
				allLines = append(allLines, scanner.Text())
			}
			if err := scanner.Err(); err != nil {
				return ToolError(fmt.Sprintf("failed to read file: %v", err)), nil
			}

			totalLines := len(allLines)

			// Apply line range
			startIdx := 0
			endIdx := totalLines
			if in.StartLine != nil && *in.StartLine > 0 {
				startIdx = *in.StartLine - 1 // 1-indexed to 0-indexed
			}
			if in.EndLine != nil && *in.EndLine > 0 {
				endIdx = *in.EndLine
			}
			if startIdx >= totalLines {
				startIdx = totalLines
			}
			if endIdx > totalLines {
				endIdx = totalLines
			}
			if startIdx > endIdx {
				startIdx = endIdx
			}

			selectedLines := allLines[startIdx:endIdx]

			// Cap at 200 lines
			const maxLines = 200
			truncated := false
			if len(selectedLines) > maxLines {
				selectedLines = selectedLines[:maxLines]
				truncated = true
			}

			content := strings.Join(selectedLines, "\n")
			if len(selectedLines) > 0 {
				content += "\n"
			}

			result := ToolSuccess(map[string]interface{}{
				"content":    content,
				"path":       resolvedPath,
				"lineCount":  len(selectedLines),
				"totalLines": totalLines,
				"startLine":  startIdx + 1,
				"endLine":    startIdx + len(selectedLines),
			})

			if truncated {
				result.Hint = fmt.Sprintf("Output truncated at %d lines. Use startLine/endLine to read remaining content (total: %d lines).", maxLines, totalLines)
			}

			return result, nil
		},
	}
}

// NewListDirTool creates the list_dir tool.
// Lists directory contents with metadata (name, size, type).
func NewListDirTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "list_dir",
		description: "List directory contents with metadata (name, size, type).",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		}, []string{"path"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			resolvedPath, err := validator.ValidatePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			entries, err := os.ReadDir(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to read directory: %v", err)), nil
			}

			var items []map[string]interface{}
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil {
					continue
				}

				entryType := "file"
				if entry.IsDir() {
					entryType = "directory"
				} else if info.Mode()&fs.ModeSymlink != 0 {
					entryType = "symlink"
				}

				items = append(items, map[string]interface{}{
					"name":     entry.Name(),
					"type":     entryType,
					"size":     info.Size(),
					"modified": info.ModTime().Unix(),
				})
			}

			if items == nil {
				items = []map[string]interface{}{}
			}

			return ToolSuccess(map[string]interface{}{
				"path":       resolvedPath,
				"entries":    items,
				"entryCount": len(items),
			}), nil
		},
	}
}

// NewSearchFilesTool creates the search_files tool.
// Grep-like pattern search across files. Returns file paths, line numbers, and matching context.
func NewSearchFilesTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "search_files",
		description: "Search for a text pattern across files in a directory. Returns matching file paths, line numbers, and context.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"path":       map[string]interface{}{"type": "string"},
			"pattern":    map[string]interface{}{"type": "string"},
			"maxResults": map[string]interface{}{"type": "integer"},
		}, []string{"path", "pattern"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Path       string `json:"path"`
				Pattern    string `json:"pattern"`
				MaxResults *int   `json:"maxResults"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			resolvedPath, err := validator.ValidatePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			maxResults := 50
			if in.MaxResults != nil && *in.MaxResults > 0 {
				maxResults = *in.MaxResults
			}

			var matches []interface{}
			_ = filepath.WalkDir(resolvedPath, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return nil
				}
				if len(matches) >= maxResults {
					return filepath.SkipAll
				}

				// Skip binary files by checking extension
				ext := strings.ToLower(filepath.Ext(path))
				if isBinaryExtension(ext) {
					return nil
				}

				file, err := os.Open(path)
				if err != nil {
					return nil
				}
				defer file.Close()

				scanner := bufio.NewScanner(file)
				lineNum := 0
				for scanner.Scan() {
					lineNum++
					line := scanner.Text()
					if strings.Contains(line, in.Pattern) {
						// Make path relative to search root for readability
						relPath, _ := filepath.Rel(resolvedPath, path)
						if relPath == "" {
							relPath = path
						}

						matches = append(matches, map[string]interface{}{
							"file":    relPath,
							"line":    lineNum,
							"content": line,
						})

						if len(matches) >= maxResults {
							return filepath.SkipAll
						}
					}
				}
				return nil
			})

			if matches == nil {
				matches = []interface{}{}
			}

			return ToolSuccess(map[string]interface{}{
				"pattern":    in.Pattern,
				"path":       resolvedPath,
				"matches":    matches,
				"matchCount": len(matches),
			}), nil
		},
	}
}

// isBinaryExtension returns true for common binary file extensions that should not be searched.
func isBinaryExtension(ext string) bool {
	switch ext {
	case ".exe", ".dll", ".so", ".dylib", ".bin", ".obj", ".o",
		".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp",
		".mp3", ".mp4", ".avi", ".mov", ".wav", ".flac",
		".zip", ".tar", ".gz", ".bz2", ".xz", ".7z", ".rar",
		".pdf", ".doc", ".docx", ".xls", ".xlsx",
		".wasm", ".db", ".sqlite":
		return true
	}
	return false
}
