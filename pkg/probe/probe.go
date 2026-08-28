package probe

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	EndLine      int    `json:"end_line"`
	MatchingLine string `json:"matching_line"`
	Hash         string `json:"hash,omitempty"`
}

// ProbeReport contains the aggregate discovery results.
type ProbeReport struct {
	Query        string        `json:"query"`
	Matches      []MatchResult `json:"matches"`
	ScannedFiles int           `json:"scanned_files"`
	DurationMs   int64         `json:"duration_ms"`
}

// FormatMarkdown formats the probe report into a high-density, token-efficient summary.
func (r *ProbeReport) FormatMarkdown() string {
	if len(r.Matches) == 0 {
		return fmt.Sprintf("No matches found for %q (scanned %d files).", r.Query, r.ScannedFiles)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d matches for %q:\n\n", len(r.Matches), r.Query))

	for _, m := range r.Matches {
		if m.SymbolName != "" {
			sb.WriteString(fmt.Sprintf("- **%s** (`%s` in `%s:%d-%d`)", m.SymbolName, m.Kind, m.FilePath, m.StartLine, m.EndLine))
		} else {
			sb.WriteString(fmt.Sprintf("- `%s:%d`", m.FilePath, m.StartLine))
		}
		if m.Hash != "" {
			sb.WriteString(fmt.Sprintf(" [Hash: #%s]", m.Hash))
		}
		sb.WriteString("\n")
		if m.MatchingLine != "" {
			sb.WriteString(fmt.Sprintf("  ```\n  %s\n  ```\n", strings.TrimSpace(m.MatchingLine)))
		}
	}

	return sb.String()
}

// Probe executes a fast local discovery search across workspaceRoot.
func Probe(workspaceRoot, query string, maxResults int, s *store.Store) (*ProbeReport, error) {
	if maxResults <= 0 {
		maxResults = 20
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

	err := filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
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

		if d.IsDir() {
			return nil
		}

		// Only check text and code files (<2MB)
		info, err := d.Info()
		if err != nil || info.Size() > 2*1024*1024 {
			return nil
		}

		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		report.ScannedFiles++

		// Quick case-insensitive check
		if !bytes.Contains(bytes.ToLower(content), []byte(queryLower)) {
			return nil
		}

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

		// Attempt AST symbol resolution for richer context
		skel, _ := ast.Skeletonize(path, content, s)

		match := MatchResult{
			FilePath:     relPath,
			StartLine:    firstMatchLineNum,
			EndLine:      firstMatchLineNum,
			MatchingLine: firstMatchingLine,
		}

		if skel != nil && len(skel.Hashes) > 0 {
			match.Hash = skel.Hashes[0]
		}

		// Try to query symbols from store for this file
		if s != nil {
			syms, _ := s.SearchSymbols(query, 5)
			for _, sym := range syms {
				if sym.FilePath == path || sym.FilePath == relPath {
					match.SymbolName = sym.Symbol
					match.Kind = sym.Kind
					match.Hash = sym.Hash
					match.StartLine = sym.Line
					break
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
