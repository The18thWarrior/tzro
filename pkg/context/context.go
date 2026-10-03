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
	"tzro/pkg/inspector"
	"tzro/pkg/store"
	"tzro/pkg/tokenizer"
)

// PackItem represents a single code snippet, symbol, or test in a context pack.
type PackItem struct {
	FilePath     string  `json:"file_path"`
	SymbolName   string  `json:"symbol_name,omitempty"`
	Kind         string  `json:"kind,omitempty"`
	Signature    string  `json:"signature,omitempty"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Reason       string  `json:"reason"`
	Score        float64 `json:"score"`
	Content      string  `json:"content"`
	TokenWeight  int     `json:"token_weight"`
	Hash         string  `json:"hash,omitempty"`
	Precision    string  `json:"precision,omitempty"`    // precise | syntactic | inferred
	Relationship string  `json:"relationship,omitempty"` // caller | implementor | embedder | test | config | anchor | callee
	Direction    string  `json:"direction,omitempty"`    // incoming | outgoing
}

// UnresolvedImport records an import that could not be resolved during analysis.
type UnresolvedImport struct {
	SourceFile string `json:"source_file"`
	ImportPath string `json:"import_path"`
}

// CoverageReport discloses candidate discovery, inclusion, and truncation statistics.
type CoverageReport struct {
	TotalCandidates     int                `json:"total_candidates"`
	IncludedCandidates  int                `json:"included_candidates"`
	TruncatedCount      int                `json:"truncated_count"`
	TruncatedManifest   []string           `json:"truncated_manifest"`
	Unresolved          []UnresolvedImport `json:"unresolved,omitempty"`
	FallbackUsed        bool               `json:"fallback_used"`
	FallbackReason      string             `json:"fallback_reason,omitempty"`
	NoChanges           bool               `json:"no_changes,omitempty"`
	NoReferences        bool               `json:"no_references,omitempty"`
	UnsupportedSyntax   []string           `json:"unsupported_syntax,omitempty"`
	UnresolvedImports   []string           `json:"unresolved_imports,omitempty"`
	AdapterErrors       []string           `json:"adapter_errors,omitempty"`
	ResourceLimits      []string           `json:"resource_limits,omitempty"`
	IncompleteDiscovery bool               `json:"incomplete_discovery,omitempty"`
}

// TokenizerMetadata carries exact token accounting metadata.
type TokenizerMetadata struct {
	Encoding             string `json:"encoding"`
	VocabularyVersion    string `json:"vocabulary_version,omitempty"`
	Mode                 string `json:"mode"` // exact | estimated
	ContentTokens        int    `json:"content_tokens"`
	SerializedPackTokens int    `json:"serialized_pack_tokens"` // Complete Markdown representation, including its envelope.
}

// ContextPack represents the assembled context bundle.
type ContextPack struct {
	Query       string             `json:"query"`
	Budget      int                `json:"budget"`
	UsedTokens  int                `json:"used_tokens"`
	Items       []PackItem         `json:"items"`
	GeneratedAt time.Time          `json:"generated_at"`
	Coverage    *CoverageReport    `json:"coverage,omitempty"`
	Impact      *ImpactReport      `json:"impact,omitempty"`
	Tokenizer   *TokenizerMetadata `json:"tokenizer,omitempty"`
	TraceID     string             `json:"trace_id,omitempty"`
}

// EstimateTokens calculates exact BPE token count using the centralized tokenizer.
func EstimateTokens(text string) int {
	return tokenizer.CountDefault(text)
}

// FormatMarkdown formats the context pack into a clean, agent-readable context document.
func (cp *ContextPack) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Context Pack: %q (Budget: %d tokens, Used: ~%d tokens)\n\n", cp.Query, cp.Budget, cp.UsedTokens))

	if cp.TraceID != "" {
		sb.WriteString(fmt.Sprintf("**Trace ID:** `%s` — inspect via `tzro inspect explain %s`\n\n", cp.TraceID, cp.TraceID))
	}

	if cp.Coverage != nil {
		sb.WriteString("### Coverage Report\n")
		sb.WriteString(fmt.Sprintf("- **Candidates Found:** %d\n", cp.Coverage.TotalCandidates))
		sb.WriteString(fmt.Sprintf("- **Candidates Included:** %d\n", cp.Coverage.IncludedCandidates))
		if cp.Coverage.TruncatedCount > 0 {
			sb.WriteString(fmt.Sprintf("- **Truncated Due to Budget:** %d\n", cp.Coverage.TruncatedCount))
			if len(cp.Coverage.TruncatedManifest) > 0 {
				display := cp.Coverage.TruncatedManifest
				extra := 0
				if len(display) > 10 {
					extra = len(display) - 10
					display = display[:10]
				}
				sb.WriteString(fmt.Sprintf("- **Truncated Files:** %s", strings.Join(display, ", ")))
				if extra > 0 {
					sb.WriteString(fmt.Sprintf(" (and %d more)", extra))
				}
				sb.WriteString("\n")
			}
		}
		if len(cp.Coverage.Unresolved) > 0 {
			sb.WriteString("- **Unresolved Imports:**\n")
			for _, u := range cp.Coverage.Unresolved {
				sb.WriteString(fmt.Sprintf("  - `%s` in %s\n", u.ImportPath, u.SourceFile))
			}
		}
		sb.WriteString("\n---\n\n")
	}

	for i, item := range cp.Items {
		sb.WriteString(fmt.Sprintf("## [%d] %s", i+1, item.FilePath))
		if item.SymbolName != "" {
			sb.WriteString(fmt.Sprintf(" : `%s` (%s)", item.SymbolName, item.Kind))
		}
		sb.WriteString(fmt.Sprintf("\n- **Reason:** %s\n", item.Reason))
		if item.Precision != "" || item.Relationship != "" || item.Direction != "" {
			parts := []string{}
			if item.Direction != "" {
				parts = append(parts, item.Direction)
			}
			if item.Precision != "" {
				parts = append(parts, item.Precision)
			}
			meta := strings.Join(parts, ", ")
			if item.Relationship != "" {
				if meta != "" {
					sb.WriteString(fmt.Sprintf("- **Impact:** `%s` (%s)\n", item.Relationship, meta))
				} else {
					sb.WriteString(fmt.Sprintf("- **Impact:** `%s`\n", item.Relationship))
				}
			} else if meta != "" {
				sb.WriteString(fmt.Sprintf("- **Impact:** (%s)\n", meta))
			}
		}
		if item.StartLine > 0 && item.EndLine >= item.StartLine {
			sb.WriteString(fmt.Sprintf("- **Lines:** %d-%d\n", item.StartLine, item.EndLine))
		}
		sb.WriteString("```\n")
		sb.WriteString(strings.TrimRight(item.Content, "\n"))
		sb.WriteString("\n```\n\n")
	}

	return sb.String()
}

// EnforceEnvelopeBudget ensures that the serialized markdown representation does not exceed maxTokens.
// Items are pruned from least relevant, and if the base envelope cannot fit, ErrBudgetTooSmall is returned.
func (cp *ContextPack) EnforceEnvelopeBudget(maxTokens int) error {
	emptyPack := &ContextPack{
		Query:       cp.Query,
		Budget:      maxTokens,
		GeneratedAt: cp.GeneratedAt,
	}
	baseTokens := EstimateTokens(emptyPack.FormatMarkdown())
	if maxTokens < baseTokens {
		return fmt.Errorf("%w: budget %d cannot fit minimum envelope (%d tokens)", tokenizer.ErrBudgetTooSmall, maxTokens, baseTokens)
	}

	for EstimateTokens(cp.FormatMarkdown()) > maxTokens && len(cp.Items) > 0 {
		popped := cp.Items[len(cp.Items)-1]
		cp.Items = cp.Items[:len(cp.Items)-1]
		if cp.Coverage != nil {
			cp.Coverage.IncludedCandidates = len(cp.Items)
			cp.Coverage.TruncatedCount++
			cp.Coverage.TruncatedManifest = append(cp.Coverage.TruncatedManifest, popped.FilePath)
		}
		cp.UsedTokens -= popped.TokenWeight
	}

	finalTokens := EstimateTokens(cp.FormatMarkdown())
	if finalTokens > maxTokens {
		return fmt.Errorf("%w: budget %d cannot fit envelope (%d tokens)", tokenizer.ErrBudgetTooSmall, maxTokens, finalTokens)
	}
	if cp.Tokenizer != nil {
		cp.Tokenizer.ContentTokens = cp.UsedTokens
		cp.Tokenizer.SerializedPackTokens = finalTokens
	}
	return nil
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
			for rank, sym := range syms {
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
					// Preserve search relevance within the symbol tier instead of
					// replacing BM25 order with alphabetical file order below.
					Score: 100.0 + float64(len(syms)-rank)/float64(len(syms)),
					Hash:  sym.Hash,
				}
			}
		}
	}

	// 3. Scan workspace for literal matches, tests, and related files
	var queryWords []string
	for _, word := range strings.Fields(strings.ToLower(query)) {
		// Sentence punctuation is not part of an explicitly named file.
		// Keep internal dots, slashes, and underscores intact.
		word = strings.Trim(word, ".,;:!?()[]{}<>\"'`“”‘’")
		if word != "" {
			queryWords = append(queryWords, word)
		}
	}
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
					item.Signature = span.Signature
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
	var included []PackItem
	var truncatedManifest []string
	for _, c := range sortedCandidates {
		if used+c.TokenWeight <= budget {
			included = append(included, *c)
			used += c.TokenWeight
			continue
		}

		// Try degrading to signature stub if possible
		if c.Signature != "" && c.Signature != c.Content {
			stubItem := *c
			stubItem.Content = stubItem.Signature + " { /* body omitted to fit budget */ }"
			stubItem.TokenWeight = EstimateTokens(stubItem.Content)
			if used+stubItem.TokenWeight <= budget {
				included = append(included, stubItem)
				used += stubItem.TokenWeight
				continue
			}
		}

		truncatedManifest = append(truncatedManifest, c.FilePath)
	}

	pack.Items = included
	pack.UsedTokens = used
	if pack.Coverage == nil {
		pack.Coverage = &CoverageReport{
			TotalCandidates:    len(sortedCandidates),
			IncludedCandidates: len(pack.Items),
			TruncatedCount:     len(truncatedManifest),
			TruncatedManifest:  truncatedManifest,
		}
	}

	// Always-on trace recording
	if a.store != nil {
		traceID := fmt.Sprintf("trace_%d", time.Now().UTC().UnixNano())
		var disc []inspector.CandidateTrace
		for _, c := range sortedCandidates {
			disc = append(disc, inspector.CandidateTrace{
				Path:   c.FilePath,
				Hash:   c.Hash,
				Score:  c.Score,
				Tokens: c.TokenWeight,
			})
		}
		var ranked []inspector.RankedCandidate
		for i, c := range sortedCandidates {
			ranked = append(ranked, inspector.RankedCandidate{
				Path:         c.FilePath,
				Rank:         i + 1,
				Score:        c.Score,
				Precision:    c.Precision,
				Relationship: c.Relationship,
			})
		}
		var packed []inspector.PackedItem
		for _, it := range pack.Items {
			packed = append(packed, inspector.PackedItem{
				Path:       it.FilePath,
				TokensUsed: it.TokenWeight,
				Tier:       inspector.TierMeasured,
			})
		}
		tr := &inspector.Trace{
			ID:        traceID,
			Workspace: workspaceRoot,
			CreatedAt: time.Now().UTC(),
			Query:     query,
			Config: inspector.TraceConfig{
				Budget: budget,
			},
			Discovery: disc,
			Ranking:   ranked,
			Packing: inspector.PackingStage{
				TotalBudget:       budget,
				BudgetRemaining:   budget - used,
				IncludedItems:     packed,
				TruncatedManifest: truncatedManifest,
			},
		}
		_ = inspector.NewEngine(a.store, a.policy).RecordTrace(tr)
		pack.TraceID = traceID
	}

	// Count the final envelope, including the trace link added above.
	pack.Tokenizer = &TokenizerMetadata{
		Encoding:             tokenizer.EncodingDefault,
		VocabularyVersion:    "tiktoken-cl100k_base",
		Mode:                 tokenizer.ModeExact,
		ContentTokens:        pack.UsedTokens,
		SerializedPackTokens: EstimateTokens(pack.FormatMarkdown()),
	}

	return pack, nil
}
