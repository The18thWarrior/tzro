package context

import (
	"bufio"
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// Precision tiers.
const (
	PrecisionPrecise   = "precise"
	PrecisionSyntactic = "syntactic"
	PrecisionInferred  = "inferred"
)

// Relationship types.
const (
	RelCaller      = "caller"
	RelImplementor = "implementor"
	RelEmbedder    = "embedder"
	RelTest        = "test"
	RelConfig      = "config"
)

// Symbol represents a code declaration symbol to find impact for.
type Symbol struct {
	Name           string `json:"name"`
	Kind           string `json:"kind,omitempty"`
	FilePath       string `json:"file_path,omitempty"`
	StartLine      int    `json:"start_line,omitempty"`
	EndLine        int    `json:"end_line,omitempty"`
	Package        string `json:"package,omitempty"`
	Workspace      string `json:"workspace,omitempty"`
	SourceSnapshot string `json:"source_snapshot,omitempty"`
	Language       string `json:"language,omitempty"`
	Module         string `json:"module,omitempty"`
}

// RawReference represents an unbudgeted reference to a symbol.
type RawReference struct {
	FilePath     string  `json:"file_path"`
	StartLine    int     `json:"start_line"`
	EndLine      int     `json:"end_line"`
	Precision    string  `json:"precision"`
	Relationship string  `json:"relationship"`
	Content      string  `json:"content"`
	SymbolName   string  `json:"symbol_name"`
	Score        float64 `json:"score"`
	SourceSymbol *Symbol `json:"source_symbol,omitempty"`
	TargetSymbol *Symbol `json:"target_symbol,omitempty"`
	Snapshot     string  `json:"snapshot,omitempty"`
}

// ImpactReport represents the complete, unbudgeted blast radius analysis metadata.
type ImpactReport struct {
	WorkspaceRoot         string          `json:"workspace_root"`
	SnapshotProvenance    string          `json:"snapshot_provenance,omitempty"`
	ChangedSymbols        []Symbol        `json:"changed_symbols"`
	ReferenceEdges        []RawReference  `json:"reference_edges"`
	UniqueReferencesCount int             `json:"unique_references_count"`
	TotalEdgesCount       int             `json:"total_edges_count"`
	CandidateTestFiles    []string        `json:"candidate_test_files"`
	AffectedModules       []string        `json:"affected_modules"`
	Coverage              *CoverageReport `json:"coverage"`
	GeneratedAt           time.Time       `json:"generated_at"`
}

// ReferenceAdapter is the language-specific interface for locating symbol references.
type ReferenceAdapter interface {
	FindReferences(workspaceRoot string, symbol Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error)
}

// GoGrepAdapter is the reference adapter for Go codebases.
type GoGrepAdapter struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewGoGrepAdapter creates a new Go reference adapter.
func NewGoGrepAdapter(s *store.Store, policy *dlp.PolicyEngine) *GoGrepAdapter {
	return &GoGrepAdapter{store: s, policy: policy}
}

// isGeneratedFile checks whether a file matches standard generated code suffixes.
func isGeneratedFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".pb.go") ||
		strings.HasSuffix(lower, "_gen.go") ||
		strings.HasSuffix(lower, ".generated.go") ||
		strings.Contains(lower, "node_modules/") ||
		strings.Contains(lower, "vendor/")
}

// FindReferences scans the workspace for references to a given symbol.
func (a *GoGrepAdapter) FindReferences(workspaceRoot string, sym Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error) {
	var refs []RawReference

	defaultIgnores := map[string]bool{
		".git":         true,
		".tzro":        true,
		"node_modules": true,
		"vendor":       true,
		"dist":         true,
		"bin":          true,
	}

	callPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym.Name) + `\b`)
	embedPattern := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(sym.Name) + `\s*$`)

	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(workspaceRoot, path)
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			if defaultIgnores[d.Name()] {
				return filepath.SkipDir
			}
			if excludes != nil && excludes.MatchesPath(relPath) {
				return filepath.SkipDir
			}
			return nil
		}

		if excludes != nil && excludes.MatchesPath(relPath) {
			return nil
		}

		if !includeGenerated && isGeneratedFile(relPath) {
			return nil
		}

		if a.policy != nil {
			eval := a.policy.EvaluatePath(relPath)
			if !eval.Allowed {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(relPath))
		isGo := ext == ".go"
		isConfig := ext == ".yaml" || ext == ".yml" || ext == ".json" || ext == ".toml"

		if !isGo && !isConfig {
			return nil
		}

		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		contentStr := string(contentBytes)

		// Check if file mentions the symbol
		if !callPattern.MatchString(contentStr) {
			// Check if naming convention indicates inferred test file
			isNameMatch := false
			if sym.FilePath != "" {
				baseTarget := strings.TrimSuffix(filepath.Base(sym.FilePath), ".go")
				if strings.HasSuffix(relPath, baseTarget+"_test.go") {
					isNameMatch = true
				}
			}
			if strings.HasSuffix(relPath, "_test.go") && strings.Contains(strings.ToLower(filepath.Base(relPath)), strings.ToLower(sym.Name)) {
				isNameMatch = true
			}

			if isNameMatch {
				symCopy := sym
				refs = append(refs, RawReference{
					FilePath:     relPath,
					StartLine:    1,
					EndLine:      min(30, countLines(contentStr)),
					Precision:    PrecisionInferred,
					Relationship: RelTest,
					Content:      contentStr,
					SymbolName:   sym.Name,
					Score:        40.0,
					SourceSymbol: &symCopy,
				})
			}
			return nil
		}

		symCopy := sym

		// Handle config file reference
		if isConfig {
			lines := strings.Split(contentStr, "\n")
			for i, line := range lines {
				if callPattern.MatchString(line) {
					start := max(1, i-2)
					end := min(len(lines), i+3)
					snippet := strings.Join(lines[start-1:end], "\n")
					refs = append(refs, RawReference{
						FilePath:     relPath,
						StartLine:    start,
						EndLine:      end,
						Precision:    PrecisionSyntactic,
						Relationship: RelConfig,
						Content:      snippet,
						SymbolName:   sym.Name,
						Score:        50.0,
						SourceSymbol: &symCopy,
					})
				}
			}
			return nil
		}

		// Handle Go source file
		isTestFile := strings.HasSuffix(relPath, "_test.go")
		lines := strings.Split(contentStr, "\n")

		for i, line := range lines {
			if callPattern.MatchString(line) {
				lineNum := i + 1
				rel := RelCaller
				prec := PrecisionSyntactic
				score := 80.0

				if isTestFile {
					rel = RelTest
					prec = PrecisionSyntactic
					score = 70.0
				} else if embedPattern.MatchString(line) {
					rel = RelEmbedder
					prec = PrecisionSyntactic
					score = 90.0
				} else if strings.Contains(line, "func ") && strings.Contains(line, "("+sym.Name+")") {
					rel = RelImplementor
					prec = PrecisionSyntactic
					score = 85.0
				}

				// Extract context window around reference site
				start := max(1, lineNum-4)
				end := min(len(lines), lineNum+10)
				snippet := strings.Join(lines[start-1:end], "\n")

				refs = append(refs, RawReference{
					FilePath:     relPath,
					StartLine:    start,
					EndLine:      end,
					Precision:    prec,
					Relationship: rel,
					Content:      snippet,
					SymbolName:   sym.Name,
					Score:        score,
					SourceSymbol: &symCopy,
				})
			}
		}

		return nil
	})

	return refs, nil
}

func countLines(s string) int {
	return strings.Count(s, "\n") + 1
}

// ImpactAnalyzer orchestrates the change-impact context pack pipeline.
type ImpactAnalyzer struct {
	store    *store.Store
	policy   *dlp.PolicyEngine
	adapter  ReferenceAdapter
	registry *AdapterRegistry
	assembly *Assembler
}

// NewImpactAnalyzer creates an ImpactAnalyzer with default GoRipgrepAdapter.
func NewImpactAnalyzer(s *store.Store, policy *dlp.PolicyEngine) *ImpactAnalyzer {
	reg := NewAdapterRegistry(s, policy)
	goAdapter, _ := reg.Get("go")
	return &ImpactAnalyzer{
		store:    s,
		policy:   policy,
		adapter:  goAdapter,
		registry: reg,
		assembly: NewAssembler(s, policy),
	}
}

// NewImpactAnalyzerWithRegistry creates an ImpactAnalyzer with an explicit AdapterRegistry.
func NewImpactAnalyzerWithRegistry(s *store.Store, policy *dlp.PolicyEngine, reg *AdapterRegistry) *ImpactAnalyzer {
	goAdapter, _ := reg.Get("go")
	if goAdapter == nil {
		goAdapter = NewGoRipgrepAdapter(s, policy)
	}
	return &ImpactAnalyzer{
		store:    s,
		policy:   policy,
		adapter:  goAdapter,
		registry: reg,
		assembly: NewAssembler(s, policy),
	}
}

// SetAdapter overrides the default adapter (for testing).
func (ia *ImpactAnalyzer) SetAdapter(adapter ReferenceAdapter) {
	ia.adapter = adapter
	if ia.registry != nil {
		ia.registry.Register("go", adapter)
	}
}

// Registry returns the underlying AdapterRegistry.
func (ia *ImpactAnalyzer) Registry() *AdapterRegistry {
	return ia.registry
}

// AnalyzeReport performs complete unbudgeted impact discovery, returning both full analysis metadata
// in ImpactReport and the token-budgeted ContextPack.
func (ia *ImpactAnalyzer) AnalyzeReport(
	ctx stdctx.Context,
	workspaceRoot string,
	symbols []Symbol,
	budget int,
	includeGenerated bool,
	snapshot string,
) (*ImpactReport, *ContextPack, error) {
	if budget <= 0 {
		budget = 4000
	}

	var ign *ignore.GitIgnore
	gitIgnorePath := filepath.Join(workspaceRoot, ".gitignore")
	if gitIgnoreContent, err := os.ReadFile(gitIgnorePath); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
	}

	cov := &CoverageReport{}
	if len(symbols) == 0 {
		cov.NoChanges = true
		pack := &ContextPack{
			Query:       "impact:diff",
			Budget:      budget,
			GeneratedAt: time.Now().UTC(),
			Coverage:    cov,
		}
		report := &ImpactReport{
			WorkspaceRoot:      workspaceRoot,
			SnapshotProvenance: snapshot,
			Coverage:           cov,
			GeneratedAt:        time.Now().UTC(),
		}
		return report, pack, nil
	}

	// Group symbols by language
	symbolsByLang := make(map[string][]Symbol)
	for _, sym := range symbols {
		lang := sym.Language
		if lang == "" && sym.FilePath != "" {
			lang = DetectLanguage(sym.FilePath)
		}
		if lang == "" {
			lang = "go"
		}
		symbolsByLang[lang] = append(symbolsByLang[lang], sym)
	}

	var allRefs []RawReference

	for lang, langSyms := range symbolsByLang {
		var adapter ReferenceAdapter
		if ia.registry != nil {
			adapter, _ = ia.registry.Get(lang)
		}
		if adapter == nil {
			adapter = ia.adapter
		}

		if adapter == nil {
			cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("no adapter registered for language %q", lang))
			cov.IncompleteDiscovery = true
			continue
		}

		if batched, ok := adapter.(BatchedReferenceAdapter); ok {
			bRefs, bCov, err := batched.FindReferencesBatch(ctx, workspaceRoot, langSyms, ign, includeGenerated)
			if err != nil {
				cov.AdapterErrors = append(cov.AdapterErrors, fmt.Sprintf("%s adapter error: %v", lang, err))
			}
			if bCov != nil {
				if bCov.FallbackUsed {
					cov.FallbackUsed = true
					cov.FallbackReason = bCov.FallbackReason
				}
				cov.AdapterErrors = append(cov.AdapterErrors, bCov.AdapterErrors...)
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, bCov.UnsupportedSyntax...)
				cov.UnresolvedImports = append(cov.UnresolvedImports, bCov.UnresolvedImports...)
				cov.ResourceLimits = append(cov.ResourceLimits, bCov.ResourceLimits...)
				if bCov.IncompleteDiscovery {
					cov.IncompleteDiscovery = true
				}
			}
			allRefs = append(allRefs, bRefs...)
		} else {
			for _, sym := range langSyms {
				refs, err := adapter.FindReferences(workspaceRoot, sym, ign, includeGenerated)
				if err != nil {
					cov.AdapterErrors = append(cov.AdapterErrors, fmt.Sprintf("%s adapter error for %s: %v", lang, sym.Name, err))
					continue
				}
				allRefs = append(allRefs, refs...)
			}
		}
	}

	if len(allRefs) == 0 {
		cov.NoReferences = true
	}

	// Calculate unique reference sites and affected files/modules
	uniqueRefSites := make(map[string]bool)
	uniqueTestFiles := make(map[string]bool)
	uniqueModules := make(map[string]bool)

	for _, ref := range allRefs {
		siteKey := fmt.Sprintf("%s:%d:%d", ref.FilePath, ref.StartLine, ref.EndLine)
		uniqueRefSites[siteKey] = true
		if ref.Relationship == RelTest {
			uniqueTestFiles[ref.FilePath] = true
		}
		dir := filepath.Dir(ref.FilePath)
		if dir == "." {
			dir = "/"
		}
		uniqueModules[dir] = true
	}

	var candidateTestFiles []string
	for tf := range uniqueTestFiles {
		candidateTestFiles = append(candidateTestFiles, tf)
	}
	sort.Strings(candidateTestFiles)

	var affectedModules []string
	for m := range uniqueModules {
		affectedModules = append(affectedModules, m)
	}
	sort.Strings(affectedModules)

	// Deduplicate references for packing display
	var displayCandidates []RawReference
	seenDisplayKeys := make(map[string]bool)
	for _, ref := range allRefs {
		key := fmt.Sprintf("%s:%d:%s:%s", ref.FilePath, ref.StartLine, ref.Relationship, ref.SymbolName)
		if !seenDisplayKeys[key] {
			seenDisplayKeys[key] = true
			displayCandidates = append(displayCandidates, ref)
		}
	}

	cov.TotalCandidates = len(displayCandidates)

	// Precision ranking: precise > syntactic > inferred
	precisionRank := func(p string) int {
		switch p {
		case PrecisionPrecise:
			return 0
		case PrecisionSyntactic:
			return 1
		case PrecisionInferred:
			return 2
		default:
			return 3
		}
	}

	sort.SliceStable(displayCandidates, func(i, j int) bool {
		rI := precisionRank(displayCandidates[i].Precision)
		rJ := precisionRank(displayCandidates[j].Precision)
		if rI != rJ {
			return rI < rJ
		}
		if displayCandidates[i].Score != displayCandidates[j].Score {
			return displayCandidates[i].Score > displayCandidates[j].Score
		}
		if displayCandidates[i].FilePath != displayCandidates[j].FilePath {
			return displayCandidates[i].FilePath < displayCandidates[j].FilePath
		}
		return displayCandidates[i].StartLine < displayCandidates[j].StartLine
	})

	query := fmt.Sprintf("impact (%d symbols)", len(symbols))
	if len(symbols) == 1 {
		query = fmt.Sprintf("impact:%s", symbols[0].Name)
	}

	pack := &ContextPack{
		Query:       query,
		Budget:      budget,
		GeneratedAt: time.Now().UTC(),
	}

	used := 0
	var truncatedManifest []string

	for _, ref := range displayCandidates {
		content := ref.Content
		if ia.policy != nil {
			redactor := dlp.NewRedactor()
			eval := ia.policy.EvaluateContent(content)
			if !eval.Allowed {
				continue
			}
			redacted, _ := redactor.Redact(content)
			content = redacted
		}

		tokens := EstimateTokens(content)
		if used+tokens <= budget {
			hash := ""
			if ia.store != nil {
				h, _ := ia.store.PutBlob(ref.FilePath, ref.StartLine, ref.EndLine, content)
				hash = h
			}

			item := PackItem{
				FilePath:     ref.FilePath,
				SymbolName:   ref.SymbolName,
				StartLine:    ref.StartLine,
				EndLine:      ref.EndLine,
				Reason:       fmt.Sprintf("%s reference to %q", ref.Relationship, ref.SymbolName),
				Score:        ref.Score,
				Content:      content,
				TokenWeight:  tokens,
				Hash:         hash,
				Precision:    ref.Precision,
				Relationship: ref.Relationship,
			}
			pack.Items = append(pack.Items, item)
			used += tokens
		} else {
			truncatedManifest = append(truncatedManifest, ref.FilePath)
		}
	}

	pack.UsedTokens = used
	cov.IncludedCandidates = len(pack.Items)
	cov.TruncatedCount = len(truncatedManifest)
	cov.TruncatedManifest = truncatedManifest
	pack.Coverage = cov

	report := &ImpactReport{
		WorkspaceRoot:         workspaceRoot,
		SnapshotProvenance:    snapshot,
		ChangedSymbols:        symbols,
		ReferenceEdges:        allRefs,
		UniqueReferencesCount: len(uniqueRefSites),
		TotalEdgesCount:       len(allRefs),
		CandidateTestFiles:    candidateTestFiles,
		AffectedModules:       affectedModules,
		Coverage:              cov,
		GeneratedAt:           pack.GeneratedAt,
	}

	pack.Impact = report
	return report, pack, nil
}

// AnalyzeSymbol discovers references to a symbol and packs them into a ContextPack.
func (ia *ImpactAnalyzer) AnalyzeSymbol(workspaceRoot, symbolName string, budget int, includeGenerated bool) (*ContextPack, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()
	sym := Symbol{Name: symbolName}
	_, pack, err := ia.AnalyzeReport(ctx, workspaceRoot, []Symbol{sym}, budget, includeGenerated, "working_tree")
	return pack, err
}

// AnalyzeSymbolWithFile discovers references to a symbol disambiguated by file.
func (ia *ImpactAnalyzer) AnalyzeSymbolWithFile(workspaceRoot, symbolName, filePath string, budget int, includeGenerated bool) (*ImpactReport, *ContextPack, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()
	symbols, err := ResolveSymbolAnchor(workspaceRoot, symbolName, filePath)
	if err != nil {
		return nil, nil, err
	}
	return ia.AnalyzeReport(ctx, workspaceRoot, symbols, budget, includeGenerated, "working_tree")
}

// AnalyzeDiff extracts changed symbols from diff text and packs their references into a ContextPack.
func (ia *ImpactAnalyzer) AnalyzeDiff(workspaceRoot, diffText string, budget int, includeGenerated bool) (*ContextPack, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 30*time.Second)
	defer cancel()
	symbols := ExtractSymbolsFromDiff(diffText)
	_, pack, err := ia.AnalyzeReport(ctx, workspaceRoot, symbols, budget, includeGenerated, "working_tree")
	return pack, err
}

// AnalyzeDiffScope analyzes the requested Git scope (staged, unstaged, all) using snapshot-aware AST extraction.
func (ia *ImpactAnalyzer) AnalyzeDiffScope(
	ctx stdctx.Context,
	workspaceRoot string,
	scope GitScope,
	budget int,
	includeGenerated bool,
) (*ImpactReport, *ContextPack, error) {
	diffResult, err := AcquireGitDiff(ctx, workspaceRoot, scope)
	if err != nil {
		return nil, nil, err
	}

	symbols, cov, err := ExtractSymbolsFromSnapshotDiff(ctx, workspaceRoot, diffResult)
	if err != nil {
		return nil, nil, err
	}

	report, pack, err := ia.AnalyzeReport(ctx, workspaceRoot, symbols, budget, includeGenerated, string(scope))
	if err != nil {
		return nil, nil, err
	}

	if cov != nil {
		report.Coverage.UnsupportedSyntax = append(report.Coverage.UnsupportedSyntax, cov.UnsupportedSyntax...)
		if cov.NoChanges {
			report.Coverage.NoChanges = true
			pack.Coverage.NoChanges = true
		}
		if cov.IncompleteDiscovery {
			report.Coverage.IncompleteDiscovery = true
			pack.Coverage.IncompleteDiscovery = true
		}
	}

	return report, pack, nil
}

// ExtractSymbolsFromDiff parses a unified git diff and extracts identifiers near changed lines.
func ExtractSymbolsFromDiff(diffText string) []Symbol {
	identRe := regexp.MustCompile(`\b[A-Z][a-zA-Z0-9_]+\b`)
	var symbols []Symbol
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(strings.NewReader(diffText))
	currFile := ""

	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "+++ b/") {
			currFile = strings.TrimPrefix(line, "+++ b/")
			continue
		}

		if (strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-")) &&
			!strings.HasPrefix(line, "+++") && !strings.HasPrefix(line, "---") {
			matches := identRe.FindAllString(line, -1)
			for _, m := range matches {
				if !seen[m] && len(m) > 2 {
					seen[m] = true
					symbols = append(symbols, Symbol{
						Name:     m,
						FilePath: currFile,
					})
				}
			}
		}
	}

	return symbols
}
