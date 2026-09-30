package probe

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/ast"
	"tzro/pkg/store"
)

// MatchResult represents a single discovered code symbol or location.
type MatchResult struct {
	FilePath     string `json:"file_path"`
	SymbolName   string `json:"symbol_name,omitempty"`
	Kind         string `json:"kind,omitempty"`
	StartLine    int    `json:"start_line"`
	MatchLine    int    `json:"match_line,omitempty"`
	EndLine      int    `json:"end_line"`
	MatchingLine string `json:"matching_line"`
	Hash         string `json:"hash,omitempty"`
}

// ProbeReport contains the aggregate discovery results.
type ProbeReport struct {
	Query               string        `json:"query"`
	Matches             []MatchResult `json:"matches"`
	ScannedFiles        int           `json:"scanned_files"`
	SkippedNontextFiles int           `json:"skipped_nontext_files,omitempty"`
	DurationMs          int64         `json:"duration_ms"`
}

// FormatMarkdown formats the probe report into a high-density, token-efficient summary.
func (r *ProbeReport) FormatMarkdown() string {
	note := ""
	if r.SkippedNontextFiles > 0 {
		note = fmt.Sprintf("\nSkipped %d file(s) containing NUL bytes or invalid UTF-8.\n", r.SkippedNontextFiles)
	}
	if len(r.Matches) == 0 {
		return fmt.Sprintf("No matches found for %q (scanned %d files).", r.Query, r.ScannedFiles) + note
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matches for %q:\n\n", len(r.Matches), r.Query))

	for _, m := range r.Matches {
		if m.SymbolName != "" {
			sb.WriteString(fmt.Sprintf("- **%s** (`%s` in `%s:%d-%d`)", m.SymbolName, m.Kind, m.FilePath, m.StartLine, m.EndLine))
		} else {
			line := m.MatchLine
			if line == 0 {
				line = m.StartLine
			}
			sb.WriteString(fmt.Sprintf("- `%s:%d`", m.FilePath, line))
		}
		if m.Hash != "" {
			sb.WriteString(fmt.Sprintf(" [Hash: #%s]", m.Hash))
		}
		sb.WriteString("\n")
		if m.MatchingLine != "" {
			sb.WriteString(fmt.Sprintf("  ```\n  %s\n  ```\n", strings.TrimSpace(m.MatchingLine)))
		}
	}

	return sb.String() + note
}

// Probe executes a fast local discovery search across workspaceRoot.
func Probe(workspaceRoot, query string, maxResults int, s *store.Store) (*ProbeReport, error) {
	if maxResults <= 0 {
		maxResults = 20
	}

	policy, err := newDiscoveryPolicy(workspaceRoot)
	if err != nil {
		return nil, err
	}

	// Build gitignore matcher
	var ign *ignore.GitIgnore
	gitIgnorePath := filepath.Join(workspaceRoot, ".gitignore")
	if gitIgnoreContent, err := os.ReadFile(gitIgnorePath); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
	}

	queryLower := strings.ToLower(query)
	report := &ProbeReport{
		Query: query,
	}

	defaultIgnores := map[string]bool{
		".git":         true,
		".tzro":        true,
		"node_modules": true,
		"vendor":       true,
		"bin":          true,
		"obj":          true,
		"dist":         true,
		".DS_Store":    true,
	}

	err = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(workspaceRoot, path)
		if relPath == "." {
			return nil
		}

		base := d.Name()
		if defaultIgnores[base] {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if ign != nil && ign.MatchesPath(relPath) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if !policy.engine.EvaluatePath(filepath.ToSlash(relPath)).Allowed {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}

		// Only read regular, permitted workspace files.
		resolved, allowed := policy.readableFile(path, relPath)
		if !allowed {
			return nil
		}

		// Only check text and code files (<2MB)
		content, err := os.ReadFile(resolved)
		if err != nil {
			return nil
		}

		report.ScannedFiles++
		if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
			if policy.engine.EvaluateContent(string(content)).Allowed {
				report.SkippedNontextFiles++
			}
			return nil
		}

		// Quick case-insensitive check
		if !bytes.Contains(bytes.ToLower(content), []byte(queryLower)) {
			return nil
		}

		if !policy.engine.EvaluateContent(string(content)).Allowed {
			return nil
		}
		redacted, mapping := policy.redactor.Redact(string(content))
		sensitive := len(mapping) > 0 || redacted != string(content)

		// Find matching line
		scanner := bufio.NewScanner(bytes.NewReader(content))
		lineNum := 1
		var firstMatchingLine string
		firstMatchLineNum := 1

		for scanner.Scan() {
			line := scanner.Text()
			if strings.Contains(strings.ToLower(line), queryLower) {
				firstMatchingLine = line
				firstMatchLineNum = lineNum
				break
			}
			lineNum++
		}

		if sensitive {
			firstMatchingLine, _ = policy.redactor.Redact(firstMatchingLine)
		}
		match := MatchResult{FilePath: relPath, StartLine: firstMatchLineNum, EndLine: firstMatchLineNum, MatchLine: firstMatchLineNum, MatchingLine: firstMatchingLine}
		if !sensitive {
			// Preserve indexing, but bind the result to its actual source span.
			if s != nil {
				_, _ = ast.Skeletonize(path, content, s, workspaceRoot)
			}
			span, spanErr := ast.ExtractDeclarationSpan(path, content, firstMatchLineNum, "", s)
			if spanErr == nil && span != nil && span.Kind != "unknown" {
				match.SymbolName, match.Kind = span.SymbolName, span.Kind
				match.StartLine, match.EndLine = span.StartLine, span.EndLine
				if s != nil && span.BodyHash != "" {
					if _, err := s.GetBlob(span.BodyHash); err == nil {
						match.Hash = span.BodyHash
					}
				}
			}
		}

		report.Matches = append(report.Matches, match)
		if len(report.Matches) >= maxResults {
			return filepath.SkipAll
		}

		return nil
	})

	if err != nil && err != filepath.SkipAll {
		return nil, err
	}

	return report, nil
}
