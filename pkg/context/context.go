package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/ast"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// PackItem represents a single code snippet, symbol, or test in a context pack.
type PackItem struct {
	FilePath    string  `json:"file_path"`
	SymbolName  string  `json:"symbol_name,omitempty"`
	Kind        string  `json:"kind,omitempty"`
	StartLine   int     `json:"start_line"`
	EndLine     int     `json:"end_line"`
	Reason      string  `json:"reason"`
	Score       float64 `json:"score"`
	Content     string  `json:"content"`
	TokenWeight int     `json:"token_weight"`
	Hash        string  `json:"hash,omitempty"`
}

// ContextPack represents the assembled context bundle.
type ContextPack struct {
	Query       string     `json:"query"`
	Budget      int        `json:"budget"`
	UsedTokens  int        `json:"used_tokens"`
	Items       []PackItem `json:"items"`
	GeneratedAt time.Time  `json:"generated_at"`
}

// EstimateTokens provides a deterministic rule-of-thumb estimate (~4 chars per token).
func EstimateTokens(text string) int {
	tokens := len(text) / 4
	if tokens == 0 && len(text) > 0 {
		return 1
	}
	return tokens
}

// FormatMarkdown formats the context pack into a clean, agent-readable context document.
func (cp *ContextPack) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Context Pack: %q (Budget: %d tokens, Used: ~%d tokens)\n\n", cp.Query, cp.Budget, cp.UsedTokens))

	for i, item := range cp.Items {
		sb.WriteString(fmt.Sprintf("## [%d] %s", i+1, item.FilePath))
		if item.SymbolName != "" {
			sb.WriteString(fmt.Sprintf(" : `%s` (%s)", item.SymbolName, item.Kind))
		}
		sb.WriteString(fmt.Sprintf("\n- **Reason:** %s\n", item.Reason))
		if item.StartLine > 0 && item.EndLine >= item.StartLine {
			sb.WriteString(fmt.Sprintf("- **Lines:** %d-%d\n", item.StartLine, item.EndLine))
		}
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimRight(item.Content, "\n"))
		sb.WriteString("\n```\n\n")
	}

	return sb.String()
}

// Assembler manages building token-budgeted context packs.
type Assembler struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewAssembler creates a new context pack assembler.
// If policy is nil, the assembler operates without privacy filtering.
func NewAssembler(s *store.Store, policy *dlp.PolicyEngine) *Assembler {
	return &Assembler{store: s, policy: policy}
}

// Assemble selects and ranks relevant symbols, implementations, tests, and documentation.
func (a *Assembler) Assemble(workspaceRoot, query string, budget int) (*ContextPack, error) {
	if budget <= 0 {
		budget = 4000
	}

	// 1. Build gitignore matcher
	var ign *ignore.GitIgnore
	gitIgnorePath := filepath.Join(workspaceRoot, ".gitignore")
	if gitIgnoreContent, err := os.ReadFile(gitIgnorePath); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
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

	pack := &ContextPack{
		Query:       query,
		Budget:      budget,
		GeneratedAt: time.Now().UTC(),
	}

	// Validate freshness and re-index modified files incrementally
	if a.store != nil {
		_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				if d != nil && defaultIgnores[d.Name()] {
					return filepath.SkipDir
				}
				return nil
			}

			relPath, _ := filepath.Rel(workspaceRoot, path)
			if ign != nil && ign.MatchesPath(relPath) {
				return nil
			}

			// Privacy policy: skip blocked/denied paths without reading from disk
			if a.policy != nil {
				eval := a.policy.EvaluatePath(relPath)
				if !eval.Allowed {
					return nil
				}
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			// Check freshness against store
			lastMod, prevHash, err := a.store.GetFileIndexState(workspaceRoot, relPath)
			currMod := info.ModTime().UnixNano()
			if err != nil || lastMod != currMod {
				// File is dirty or never indexed: parse and update index
				content, err := os.ReadFile(path)
				if err == nil {
					currHash := store.ComputeHash(string(content))
					if currHash != prevHash {
						_ = a.store.PruneFileSymbols(workspaceRoot, relPath)
						_ = a.store.PruneFileSymbols(workspaceRoot, path)
						_, _ = ast.Skeletonize(relPath, content, a.store, workspaceRoot)
						_ = a.store.UpdateFileIndexState(workspaceRoot, relPath, currMod, currHash)
					} else {

						// Content unchanged, update mod timestamp
						_ = a.store.UpdateFileIndexState(workspaceRoot, relPath, currMod, currHash)
					}
				}
			}
			return nil

		})
	}

	candidateMap := make(map[string]*PackItem)
	tsconfig := LoadTSConfig(workspaceRoot)

	// 2. FTS5 Symbol search
	if a.store != nil {

		syms, err := a.store.SearchSymbols(workspaceRoot, query, 25)
		if err == nil {
			for _, sym := range syms {
				relPath := sym.FilePath
				if filepath.IsAbs(relPath) {
					relPath, _ = filepath.Rel(workspaceRoot, relPath)
				}

				// Privacy policy: drop symbols from blocked/denied file paths
				if a.policy != nil {
					eval := a.policy.EvaluatePath(relPath)
					if !eval.Allowed {
						continue
					}
				}

				key := fmt.Sprintf("%s:%s", relPath, sym.Symbol)
				candidateMap[key] = &PackItem{
					FilePath:   relPath,
					SymbolName: sym.Symbol,
					Kind:       sym.Kind,
					StartLine:  sym.Line,
					EndLine:    sym.Line + 20, // default window if body not expanded
					Reason:     fmt.Sprintf("FTS5 symbol index match for %q", sym.Symbol),
					Score:      100.0,
					Hash:       sym.Hash,
				}
			}
		}
	}

	// 3. Scan workspace for literal matches, tests, and related files
	queryWords := strings.Fields(strings.ToLower(query))
	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
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

		// Privacy policy: skip blocked/denied paths without reading from disk
		if a.policy != nil {
			eval := a.policy.EvaluatePath(relPath)
			if !eval.Allowed {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}

		if d.IsDir() {
			return nil
		}

		info, err := d.Info()
		if err != nil || info.Size() > 1024*1024 { // 1MB limit for context candidate
			return nil
		}

		relLower := strings.ToLower(relPath)

		// Check filename relevance
		fileScore := 0.0
		for _, qw := range queryWords {
			if strings.Contains(relLower, qw) {
				fileScore += 30.0
			}
		}

		isTestFile := strings.HasSuffix(relLower, "_test.go") || strings.Contains(relLower, ".test.") || strings.Contains(relLower, ".spec.")
		if isTestFile && fileScore > 0 {
			fileScore += 20.0
		}

		if fileScore > 0 {
			contentBytes, err := os.ReadFile(path)
			if err == nil {
				skel, _ := ast.Skeletonize(relPath, contentBytes, nil, "")
				body := string(contentBytes)
				if skel != nil && skel.SkeletonCode != "" {
					body = skel.SkeletonCode
				}

				reason := "File name and path matches query keywords"
				if isTestFile {
					reason = "Associated test suite covering matching keywords"
				}

				key := fmt.Sprintf("%s:file", relPath)
				if existing, exists := candidateMap[key]; exists {
					if fileScore > existing.Score {
						existing.Score = fileScore
					}
				} else {
					candidateMap[key] = &PackItem{
						FilePath:    relPath,
						Reason:      reason,
						Score:       fileScore,
						Content:     body,
						TokenWeight: EstimateTokens(body),
					}
				}

				// Check TypeScript / JavaScript import relationships
				if strings.HasSuffix(relLower, ".ts") || strings.HasSuffix(relLower, ".tsx") || strings.HasSuffix(relLower, ".js") || strings.HasSuffix(relLower, ".jsx") {
					imports, _ := ExtractImports(path, contentBytes)
					for _, imp := range imports {
						for _, candidatePath := range ResolveImportedFile(workspaceRoot, path, imp.ImportPath, tsconfig) {
							if _, err := os.Stat(candidatePath); err == nil {
								relImpPath, _ := filepath.Rel(workspaceRoot, candidatePath)
								impContent, err := os.ReadFile(candidatePath)
								if err == nil {
									impKey := fmt.Sprintf("%s:imported", relImpPath)
									if _, exists := candidateMap[impKey]; !exists {
										impBody := string(impContent)
										skelImp, _ := ast.Skeletonize(relImpPath, impContent, nil, "")
										if skelImp != nil && skelImp.SkeletonCode != "" {
											impBody = skelImp.SkeletonCode
										}
										symLabel := ""
										if len(imp.ImportedSymbols) > 0 {
											symLabel = fmt.Sprintf(" (symbols: %s)", strings.Join(imp.ImportedSymbols, ", "))
										}
										candidateMap[impKey] = &PackItem{
											FilePath:    relImpPath,
											Reason:      fmt.Sprintf("Imported by %s%s", relPath, symLabel),
											Score:       fileScore - 5.0, // slightly lower score than direct importer
											Content:     impBody,
											TokenWeight: EstimateTokens(impBody),
										}
									}
								}
								break
							}
						}
					}
				}
			}
		}

		return nil
	})


	// 4. Resolve candidate content if missing (e.g. from symbol search)
	for _, item := range candidateMap {
		if item.Content == "" {
			fullPath := filepath.Join(workspaceRoot, item.FilePath)
			contentBytes, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}

			// For symbol matches, try targeted declaration span extraction first
			if item.SymbolName != "" {
				span, spanErr := ast.ExtractDeclarationSpan(
					item.FilePath, contentBytes, item.StartLine, item.SymbolName, a.store,
				)
				if spanErr == nil && span != nil {
					item.Content = span.Code
					item.TokenWeight = span.TokenWeight
					item.StartLine = span.StartLine
					item.EndLine = span.EndLine
					item.Kind = span.Kind
					item.Hash = span.BodyHash
					continue
				}
			}

			// Fallback: whole-file skeleton
			skel, _ := ast.Skeletonize(item.FilePath, contentBytes, nil, "")
			if skel != nil && skel.SkeletonCode != "" {
				item.Content = skel.SkeletonCode
			} else {
				item.Content = string(contentBytes)
			}
			item.TokenWeight = EstimateTokens(item.Content)
		}
	}


	// 4.5. Privacy content evaluation and redaction
	if a.policy != nil {
		redactor := dlp.NewRedactor()
		for key, item := range candidateMap {
			if item.Content == "" {
				continue
			}
			eval := a.policy.EvaluateContent(item.Content)
			if !eval.Allowed {
				// Block/Deny: drop candidate entirely
				delete(candidateMap, key)
				continue
			}
			// Always run redactor to catch secrets even when policy says allow
			redacted, mapping := redactor.Redact(item.Content)
			if len(mapping) > 0 {
				item.Content = redacted
				item.TokenWeight = EstimateTokens(redacted)
			}
		}
	}


	// 5. Stable deterministic sorting: primary by Score DESC, secondary by FilePath ASC, then StartLine ASC
	var sortedCandidates []*PackItem
	for _, item := range candidateMap {
		if item.Content != "" {
			sortedCandidates = append(sortedCandidates, item)
		}
	}

	sort.SliceStable(sortedCandidates, func(i, j int) bool {
		if sortedCandidates[i].Score != sortedCandidates[j].Score {
			return sortedCandidates[i].Score > sortedCandidates[j].Score
		}
		if sortedCandidates[i].FilePath != sortedCandidates[j].FilePath {
			return sortedCandidates[i].FilePath < sortedCandidates[j].FilePath
		}
		return sortedCandidates[i].StartLine < sortedCandidates[j].StartLine
	})

	// 6. Strict knapsack packing under token budget — no unconditional bypasses
	used := 0
	for _, c := range sortedCandidates {
		// Strict invariant: NEVER allow used + TokenWeight > budget
		if used+c.TokenWeight <= budget {
			pack.Items = append(pack.Items, *c)
			used += c.TokenWeight
		}
		if used == budget {
			break
		}
	}
	pack.UsedTokens = used

	return pack, nil
}
