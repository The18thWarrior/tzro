package tools

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/internal/cache"
	"tzro/internal/inference"
)

// fileReadGoalContextKey is the context key type for probe goal propagation.
// When the probe executor sets this before tool calls, read_file will
// goal-compress large outputs to prevent context window overflow.
type fileReadGoalContextKey struct{}

// FileReadGoalKey is set by the probe executor before tool calls.
// When present and the read_file output exceeds fileCompactionThreshold,
// the content is chunked and each chunk compressed via the router model
// against the probe's goal.
var FileReadGoalKey = fileReadGoalContextKey{}

// fileCompactionThreshold is the minimum line count that triggers
// goal-directed compaction. Files at or below this are returned raw.
// The 500-line read_file cap means at most 5 chunks for any single call.
const fileCompactionThreshold = 100

// fileCompactionChunkSize is the number of lines per chunk when splitting
// file content for goal-directed compression.
const fileCompactionChunkSize = 100

// NewReadFileTool creates the read_file tool.
// Reads file content with optional startLine/endLine parameters.
// Caps at 500 lines per call (200 for PDFs). Bypasses the Compaction Pipeline —
// source code is injected raw per ADR-0019.
func NewReadFileTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "read_file",
		description: "Read file content with optional line range. Returns raw content (max 500 lines per call).",
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

			if in.Path == "" {
				return ToolError("path is required: specify the file path to read"), nil
			}

			resolvedPath, err := validator.ValidatePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			// Reject directories — read_file is only for files
			info, err := os.Stat(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to stat path: %v", err)), nil
			}
			if info.IsDir() {
				return ToolError(fmt.Sprintf("path '%s' is a directory, not a file. Use list_dir to explore directories.", in.Path)), nil
			}

			// Route tabular files (CSV, TSV, Excel) through the Data Profiler
			ext := strings.ToLower(filepath.Ext(resolvedPath))
			if IsTabularExtension(ext) {
				profile, profErr := ProfileTabularFile(resolvedPath)
				if profErr != nil {
					return ToolError(fmt.Sprintf("failed to profile tabular file: %v", profErr)), nil
				}

				// Store file reference in cache (path only, no content copy)
				envelopeJSON, _ := json.MarshalIndent(profile, "", "  ")
				storeCacheID, storeErr := cache.DefaultStore.StoreFileRef(ctx, resolvedPath, string(envelopeJSON))
				if storeErr == nil {
					profile.CacheID = storeCacheID
				}

				result := ToolSuccess(map[string]interface{}{
					"dataProfile": profile,
					"path":        resolvedPath,
					"cacheId":     profile.CacheID,
				})
				result.Hint = "This is a tabular data file. Use introspect_cache and sql_cached_data with the cacheId to explore the data."
				result.RelatedTools = []string{"introspect_cache", "sql_cached_data"}
				return result, nil
			}

			// Route large JSON through the Data Profiler
			if ext == ".json" && ShouldProfileJSON(resolvedPath) {
				profile, profErr := ProfileJSONFile(resolvedPath)
				if profErr == nil && profile != nil {
					envelopeJSON, _ := json.MarshalIndent(profile, "", "  ")
					storeCacheID, storeErr := cache.DefaultStore.StoreFileRef(ctx, resolvedPath, string(envelopeJSON))
					if storeErr == nil {
						profile.CacheID = storeCacheID
					}

					result := ToolSuccess(map[string]interface{}{
						"dataProfile": profile,
						"path":        resolvedPath,
						"cacheId":     profile.CacheID,
					})
					result.Hint = "This is a large JSON data file. Use introspect_cache and sql_cached_data with the cacheId to explore the data."
					result.RelatedTools = []string{"introspect_cache", "sql_cached_data"}
					return result, nil
				}
				// Falls through to raw read if JSON profiling fails
			}

			// If it's a PDF, parse using the ParsePDF utility
			if ext == ".pdf" {
				content, err := ParsePDF(ctx, resolvedPath)
				if err != nil {
					return ToolError(fmt.Sprintf("failed to parse PDF file '%s': %v", in.Path, err)), nil
				}

				lines := strings.Split(content, "\n")
				totalLines := len(lines)
				startIdx := 0
				endIdx := totalLines
				if in.StartLine != nil && *in.StartLine > 0 {
					startIdx = *in.StartLine - 1
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

				selectedLines := lines[startIdx:endIdx]
				const maxLines = 200
				truncated := false
				if len(selectedLines) > maxLines {
					selectedLines = selectedLines[:maxLines]
					truncated = true
				}

				finalContent := strings.Join(selectedLines, "\n")
				if len(selectedLines) > 0 {
					finalContent += "\n"
				}

				result := ToolSuccess(map[string]interface{}{
					"content":    finalContent,
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
			}

			// Read the file
			file, err := os.Open(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to open file '%s': %v", in.Path, err)), nil
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

			// Cap at 500 lines for tool output to prevent context window overflow
			const maxLines = 500
			truncated := false
			if len(selectedLines) > maxLines {
				selectedLines = selectedLines[:maxLines]
				truncated = true
			}

			content := strings.Join(selectedLines, "\n")
			if len(selectedLines) > 0 {
				content += "\n"
			}

			// Goal-directed file compaction: if the output exceeds the threshold
			// and a probe goal is present in context, compress via router model.
			// The probe is unaware this happens — it gets back content that is
			// pre-compressed for its goal instead of raw source.
			if goal, ok := ctx.Value(FileReadGoalKey).(string); ok && goal != "" && len(selectedLines) > fileCompactionThreshold {
				compressed, compErr := compressFileForGoal(ctx, content, goal, resolvedPath, totalLines)
				if compErr != nil {
					// Fallback: deterministic truncation (first/last 20 lines)
					fmt.Fprintf(os.Stderr, "[FileCompaction] Router failed for %s, falling back to truncation: %v\n", in.Path, compErr)
					compressed = deterministicTruncate(selectedLines)
				}
				compResult := ToolSuccess(map[string]interface{}{
					"content":    compressed,
					"path":       resolvedPath,
					"lineCount":  len(selectedLines),
					"totalLines": totalLines,
					"startLine":  startIdx + 1,
					"endLine":    startIdx + len(selectedLines),
				})
				compResult.Hint = fmt.Sprintf(
					"File was %d lines (est. ~%d tokens). Goal-compressed for probe goal: '%s'.",
					len(selectedLines), len(selectedLines)*8, truncateGoalStr(goal, 80),
				)
				return compResult, nil
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

// compressFileForGoal chunks file content and compresses each chunk via the
// router model against the probe's goal. This is the core of goal-directed
// file compaction (ADR-0019 extension): large read_file outputs are
// transparently replaced with goal-relevant summaries to prevent probe
// context window overflow.
//
// Cost model: for a 500-line file (the read_file maximum), this produces
// up to 5 chunks × 1 router call each ≈ ~3,280 tokens total, ~3-5 seconds.
// Output: ~1,000-2,500 chars vs ~20,000 chars raw — 8-20× reduction.
func compressFileForGoal(ctx context.Context, content, goal, path string, totalLines int) (string, error) {
	lines := strings.Split(content, "\n")

	// Remove trailing empty line from strings.Split
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	// Build system prompt — goal-directed extraction
	systemPrompt := "Given the goal: '" + goal + "', extract only the relevant information from this code. Output: relevant function signatures, types, constants, and key logic. Skip irrelevant implementation details. Be concise."
	if goal == "" {
		// Fallback for empty goal: generic structural extraction
		systemPrompt = "Extract the key structural elements: function signatures, type definitions, constants, and important comments. Be concise."
	}

	// Chunk and compress
	var summaries []string
	for i := 0; i < len(lines); i += fileCompactionChunkSize {
		end := i + fileCompactionChunkSize
		if end > len(lines) {
			end = len(lines)
		}
		chunk := strings.Join(lines[i:end], "\n")

		messages := []inference.InferenceMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: chunk},
		}

		// Cap generation at 256 tokens to prevent inflation —
		// matching the compaction cap from the compactor engine.
		cappedCtx := context.WithValue(ctx, inference.MaxTokensKey, 256)
		result, err := inference.CallRouter(cappedCtx, messages, "")
		if err != nil {
			// Return error to trigger deterministicTruncate fallback in caller
			return "", fmt.Errorf("chunk %d-%d compression failed: %w", i+1, end, err)
		}
		if result != nil && strings.TrimSpace(result.Content) != "" {
			summaries = append(summaries, strings.TrimSpace(result.Content))
		}
	}

	// Build output with header
	header := fmt.Sprintf("[File: %s (%d lines, goal-compressed)]", filepath.Base(path), totalLines)
	compacted := header + "\n" + strings.Join(summaries, "\n")

	fmt.Fprintf(os.Stderr, "[FileCompaction] %s: %d lines → %d chars (from %d chars raw)\n",
		filepath.Base(path), totalLines, len(compacted), len(content))

	return compacted, nil
}

// deterministicTruncate returns the first and last 20 lines of the input
// with an omission marker in between. Used as a fallback when the router
// model fails during goal-directed compression.
func deterministicTruncate(lines []string) string {
	const keepLines = 20
	if len(lines) <= keepLines*2 {
		return strings.Join(lines, "\n")
	}
	head := strings.Join(lines[:keepLines], "\n")
	tail := strings.Join(lines[len(lines)-keepLines:], "\n")
	return head + fmt.Sprintf("\n[... %d lines omitted ...]\n", len(lines)-keepLines*2) + tail
}

// truncateGoalStr truncates a string to maxLen chars with "..." suffix.
func truncateGoalStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// NewListDirTool creates the list_dir tool.
// Lists directory contents with noise filtering, statistical profiling, and pagination.
// Noisy entries (node_modules, .git, OS clutter) are hidden to prevent the local model
// from premature anchoring on misleading signals. A file-type profile is computed from
// all visible entries before truncation so the model gets mathematical grounding.
func NewListDirTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "list_dir",
		description: "List directory contents with file-type profile and metadata. Noisy entries (node_modules, .git, OS files) are hidden automatically.",
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
				// Fallback: if path is empty or ".", default to first allowed root (project dir).
				// The local model commonly emits "." or "" to mean "list the project root".
				if in.Path == "" || in.Path == "." || in.Path == "./" {
					roots := validator.resolveRoots()
					if len(roots) > 0 {
						resolvedPath = roots[0]
						err = nil
					}
				}
				if err != nil {
					if strings.Contains(err.Error(), "does not exist") {
						return ToolError(fmt.Sprintf("path '%s' does not exist. Do not guess directory names. Use search_files to find what you are looking for.", in.Path)), nil
					}
					return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
				}
			}

			rawEntries, err := os.ReadDir(resolvedPath)
			if err != nil {
				info, statErr := os.Stat(resolvedPath)
				if statErr == nil && !info.IsDir() {
					contentBytes, readErr := os.ReadFile(resolvedPath)
					if readErr != nil {
						return ToolError(fmt.Sprintf("path is a file, but failed to read: %v", readErr)), nil
					}

					if !utf8.Valid(contentBytes) {
						return ToolError("path is a binary file. Do not use list_dir on files. Use peek_file for text files."), nil
					}

					allLines := strings.Split(string(contentBytes), "\n")
					totalLines := len(allLines)
					const maxLines = 200
					truncated := false
					if len(allLines) > maxLines {
						allLines = allLines[:maxLines]
						truncated = true
					}
					content := strings.Join(allLines, "\n")
					if len(allLines) > 0 && !strings.HasSuffix(content, "\n") {
						content += "\n"
					}

					result := ToolSuccess(map[string]interface{}{
						"content":    content,
						"path":       resolvedPath,
						"lineCount":  len(allLines),
						"totalLines": totalLines,
						"startLine":  1,
						"endLine":    len(allLines),
					})
					result.Hint = "Path was a file instead of a directory. Gracefully degraded to read_file behavior."
					if truncated {
						result.Hint += fmt.Sprintf(" Output truncated at %d lines.", maxLines)
					}
					return result, nil
				}
				return ToolError(fmt.Sprintf("failed to read directory: %v", err)), nil
			}

			// Load gitignore if available
			var matcher *ignore.GitIgnore
			roots := validator.resolveRoots()
			if len(roots) > 0 {
				if m, err := ignore.CompileIgnoreFile(filepath.Join(roots[0], ".gitignore")); err == nil {
					matcher = m
				}
			}

			// L0: Filter noisy entries to prevent premature anchoring
			var items []map[string]interface{}
			hiddenCount := 0
			for _, entry := range rawEntries {
				entryPath := filepath.Join(resolvedPath, entry.Name())
				if isNoisyEntry(entry.Name(), entryPath, matcher) {
					hiddenCount++
					continue
				}

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
					"name":   filepath.ToSlash(entryPath),
					"type":   entryType,
					"isDir":  entry.IsDir(),
					"isFile": !entry.IsDir(),
				})
			}

			if items == nil {
				items = []map[string]interface{}{}
			}

			// L2: Compute directory profile from all visible entries (before truncation)
			profile := computeDirProfile(items)
			totalVisible := len(items)

			// L1: Pagination — truncate if too many entries
			const maxDirEntries = 100
			truncated := false
			if len(items) > maxDirEntries {
				items = items[:maxDirEntries]
				truncated = true
			}

			result := ToolSuccess(map[string]interface{}{
				"path":        resolvedPath,
				"profile":     profile,
				"entries":     items,
				"entryCount":  len(items),
				"totalCount":  totalVisible,
				"hiddenCount": hiddenCount,
			})

			// Build combined hint from active filters
			var hints []string
			if hiddenCount > 0 {
				hints = append(hints, fmt.Sprintf("%d noisy entries hidden (node_modules, .git, OS files, etc).", hiddenCount))
			}
			if truncated {
				hints = append(hints, fmt.Sprintf("Showing first %d of %d entries. Use search_files to find specific files, or list_dir on a subdirectory.", maxDirEntries, totalVisible))
			}
			if len(hints) > 0 {
				result.Hint = strings.Join(hints, " ")
			}

			return result, nil
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
				// The local model commonly puts its search term in the path field.
				if in.Pattern == "" && in.Path != "" && in.Path != "." && in.Path != "./" {
					in.Pattern = filepath.Base(in.Path)
				}

				// Rescue to project root
				roots := validator.resolveRoots()
				if len(roots) > 0 {
					resolvedPath = roots[0]
					err = nil
				}

				if err != nil {
					return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
				}
			}

			if in.Pattern == "" {
				return ToolError("The 'pattern' parameter is missing. You must provide a 'pattern' (string) to search for."), nil
			}

			maxResults := 15
			if in.MaxResults != nil && *in.MaxResults > 0 {
				maxResults = *in.MaxResults
			}

			// Load gitignore if available
			var matcher *ignore.GitIgnore
			roots := validator.resolveRoots()
			if len(roots) > 0 {
				if m, err := ignore.CompileIgnoreFile(filepath.Join(roots[0], ".gitignore")); err == nil {
					matcher = m
				}
			}

			re, err := regexp.Compile(in.Pattern)
			if err != nil {
				return ToolError(fmt.Sprintf("invalid regex pattern '%s': %v", in.Pattern, err)), nil
			}

			var matches []interface{}
			_ = filepath.WalkDir(resolvedPath, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				// L0: Skip noisy files and directories
				if isNoisyEntry(d.Name(), path, matcher) {
					if d.IsDir() {
						return filepath.SkipDir
					}
					return nil
				}

				if d.IsDir() {
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
					if re.MatchString(line) {
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
				if err := scanner.Err(); err != nil {
					return nil // Skip files that fail to read fully
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

// isNoisyEntry returns true for filesystem entries that are OS clutter,
// build artifacts, or vendored dependency directories that should be
// hidden from the local model to prevent premature anchoring.
// Used by list_dir (hides entries) and search_files (skips files/directories).
func isNoisyEntry(name string, path string, matcher *ignore.GitIgnore) bool {
	if matcher != nil && matcher.MatchesPath(path) {
		return true
	}

	switch name {
	// OS clutter
	case ".DS_Store", "Thumbs.db", ".Trash", "desktop.ini",
		".Spotlight-V100", ".fseventsd":
		return true
	// Vendored / build directories that mislead project identification
	case "node_modules", ".git", "__pycache__", ".tox",
		"dist", "build", ".next", ".nuxt", ".output",
		"target", "vendor":
		return true
	}
	// Temp files: ~$*.docx, .#* (Emacs), *.swp/*.swo (Vim)
	if strings.HasPrefix(name, "~$") || strings.HasPrefix(name, ".#") {
		return true
	}
	if strings.HasSuffix(name, ".swp") || strings.HasSuffix(name, ".swo") {
		return true
	}
	// Ensure log and db files are ignored in case gitignore doesn't catch them
	if strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".db-journal") {
		return true
	}
	return false
}

// computeDirProfile summarizes a directory's composition by file extension.
// Returns a human-readable string like "45 .go, 3 .mod, 2 .sum, 8 directories".
// Computed from the full visible entry list (after noise filtering, before pagination)
// so the model gets mathematical grounding that resists semantic anchoring.
func computeDirProfile(items []map[string]interface{}) string {
	extCounts := make(map[string]int)
	dirCount := 0
	for _, item := range items {
		itemType, _ := item["type"].(string)
		if itemType == "directory" {
			dirCount++
			continue
		}
		name, _ := item["name"].(string)
		ext := strings.ToLower(filepath.Ext(name))
		if ext == "" {
			ext = "(no ext)"
		}
		extCounts[ext]++
	}

	// Sort extensions by count descending, take top 6
	type extEntry struct {
		ext   string
		count int
	}
	var sorted []extEntry
	for ext, count := range extCounts {
		sorted = append(sorted, extEntry{ext, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].count != sorted[j].count {
			return sorted[i].count > sorted[j].count
		}
		return sorted[i].ext < sorted[j].ext // stable tie-break
	})
	if len(sorted) > 6 {
		sorted = sorted[:6]
	}

	var parts []string
	for _, e := range sorted {
		parts = append(parts, fmt.Sprintf("%d %s", e.count, e.ext))
	}
	if dirCount > 0 {
		parts = append(parts, fmt.Sprintf("%d directories", dirCount))
	}

	if len(parts) == 0 {
		return "empty directory"
	}
	return strings.Join(parts, ", ")
}

// NewPeekFileTool creates the peek_file tool.
// Returns the first 20 lines of a file — a low-cost sampling tool designed
// to encourage the local model to ground its hypotheses in actual file content
// rather than directory names. The name "peek" signals cheapness, making the
// model more likely to call it than read_file (which it perceives as expensive).
func NewPeekFileTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "peek_file",
		description: "Quick peek: returns the first 20 lines of a file. Use to verify what a file actually contains before drawing conclusions.",
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

			if in.Path == "" {
				return ToolError("path is required: specify the file path to peek"), nil
			}

			resolvedPath, err := validator.ValidatePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			// Reject directories — peek_file is only for files
			info, err := os.Stat(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to stat path: %v", err)), nil
			}
			if info.IsDir() {
				return ToolError(fmt.Sprintf("path '%s' is a directory, not a file. Use list_dir to explore directories.", in.Path)), nil
			}

			// Spec §6: Excel files are binary — can't peek
			ext := strings.ToLower(filepath.Ext(resolvedPath))
			if ext == ".xlsx" || ext == ".xls" {
				result := ToolError("Cannot peek binary Excel file. Use read_file for full profiling and cached data access.")
				result.RelatedTools = []string{"read_file"}
				return result, nil
			}

			file, err := os.Open(resolvedPath)
			if err != nil {
				return ToolError(fmt.Sprintf("failed to open file '%s': %v", in.Path, err)), nil
			}
			defer file.Close()

			const peekLines = 20
			scanner := bufio.NewScanner(file)
			var lines []string
			for scanner.Scan() {
				lines = append(lines, scanner.Text())
				if len(lines) >= peekLines {
					break
				}
			}
			if err := scanner.Err(); err != nil {
				return ToolError(fmt.Sprintf("failed to read file: %v", err)), nil
			}

			content := strings.Join(lines, "\n")
			if len(lines) > 0 {
				content += "\n"
			}

			result := ToolSuccess(map[string]interface{}{
				"content":   content,
				"path":      resolvedPath,
				"lineCount": len(lines),
			})

			if len(lines) >= peekLines {
				result.Hint = "File continues beyond 20 lines. Use read_file with startLine/endLine to see more."
			}

			// Spec §6: tabular file hint
			if ext == ".csv" || ext == ".tsv" {
				tabularHint := "This is a tabular data file. Use read_file for full profiling and cached data access."
				if result.Hint != "" {
					result.Hint += "\n" + tabularHint
				} else {
					result.Hint = tabularHint
				}
			}

			return result, nil
		},
	}
}

// NewWriteFileTool creates the write_file tool.
// Writes content to a file path with security validation, UTF-8 enforcement,
// automatic parent directory creation, and backup-on-overwrite with LRU eviction.
func NewWriteFileTool(validator *PathValidator) *BaseAgentTool {
	return &BaseAgentTool{
		name:        "write_file",
		description: "Write content to a file. Creates parent directories automatically. Backs up existing files before overwriting.",
		schema: GetToolGBNFSchema(map[string]interface{}{
			"path":    map[string]interface{}{"type": "string", "description": "Absolute path to the file to write"},
			"content": map[string]interface{}{"type": "string", "description": "File content to write"},
		}, []string{"path", "content"}),
		executeFn: func(ctx context.Context, input json.RawMessage) (*ToolResult, error) {
			var in struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal(input, &in); err != nil {
				return nil, err
			}

			if in.Path == "" {
				return ToolError("path is required: specify the file path to write"), nil
			}
			if in.Content == "" {
				return ToolError("content is required: specify the file content to write"), nil
			}

			resolvedPath, err := validator.ValidateWritePath(in.Path)
			if err != nil {
				return ToolError(fmt.Sprintf("path validation failed: %v", err)), nil
			}

			// Reject directories
			if info, err := os.Stat(resolvedPath); err == nil && info.IsDir() {
				return ToolError(fmt.Sprintf("path '%s' is a directory, not a file. Specify a file path.", in.Path)), nil
			}

			// Reject binary content (null bytes)
			if strings.ContainsRune(in.Content, 0) {
				return ToolError("binary content not allowed: write_file only supports UTF-8 text files"), nil
			}

			// Check if file exists for action tracking and backup
			action := "created"
			if _, err := os.Stat(resolvedPath); err == nil {
				action = "updated"
				// Backup existing file before overwriting
				if err := BackupFile(resolvedPath); err != nil {
					fmt.Fprintf(os.Stderr, "[write_file] Backup failed (non-fatal): %v\n", err)
				}
			}

			// Create parent directories
			parentDir := filepath.Dir(resolvedPath)
			if err := os.MkdirAll(parentDir, 0755); err != nil {
				return ToolError(fmt.Sprintf("failed to create parent directories: %v", err)), nil
			}

			// Write file
			if err := os.WriteFile(resolvedPath, []byte(in.Content), 0644); err != nil {
				return ToolError(fmt.Sprintf("failed to write file: %v", err)), nil
			}

			lineCount := strings.Count(in.Content, "\n")
			if len(in.Content) > 0 && !strings.HasSuffix(in.Content, "\n") {
				lineCount++ // count last line without trailing newline
			}

			return ToolSuccess(map[string]interface{}{
				"status":       "success",
				"action":       action,
				"path":         resolvedPath,
				"linesWritten": lineCount,
			}), nil
		},
	}
}

// BackupFile copies an existing file to .tzro/backups/{sha256}.bak before overwriting.
// Enforces LRU eviction at maxBackups files.
func BackupFile(filePath string) error {
	const maxBackups = 50

	// Determine backup directory
	tzroDir := os.Getenv("TZRO_DIR")
	if tzroDir == "" {
		tzroDir = "."
	}
	backupDir := filepath.Join(tzroDir, ".tzro", "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Read existing file
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read file for backup: %w", err)
	}

	// Write backup with hash-based name
	hash := fmt.Sprintf("%x", sha256Sum([]byte(filePath)))
	backupPath := filepath.Join(backupDir, hash+".bak")
	if err := os.WriteFile(backupPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write backup: %w", err)
	}

	// LRU eviction
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil // Non-fatal
	}

	type backupEntry struct {
		name    string
		modTime int64
	}
	var backups []backupEntry
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bak") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		backups = append(backups, backupEntry{name: e.Name(), modTime: info.ModTime().UnixNano()})
	}

	if len(backups) > maxBackups {
		// Sort by mod time ascending (oldest first)
		sort.Slice(backups, func(i, j int) bool {
			return backups[i].modTime < backups[j].modTime
		})
		// Remove oldest entries
		for i := 0; i < len(backups)-maxBackups; i++ {
			_ = os.Remove(filepath.Join(backupDir, backups[i].name))
		}
	}

	return nil
}

// sha256Sum computes a SHA-256 hash of the given data.
func sha256Sum(data []byte) [32]byte {
	return sha256.Sum256(data)
}
