package context

import (
	"bufio"
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
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	StartLine int    `json:"start_line,omitempty"`
	Package   string `json:"package,omitempty"`
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
				refs = append(refs, RawReference{
					FilePath:     relPath,
					StartLine:    1,
					EndLine:      min(30, countLines(contentStr)),
					Precision:    PrecisionInferred,
					Relationship: RelTest,
					Content:      contentStr,
					SymbolName:   sym.Name,
					Score:        40.0,
				})
			}
			return nil
		}

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
					})
					break
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
				prec := PrecisionPrecise
				score := 80.0

				if isTestFile {
					rel = RelTest
					prec = PrecisionSyntactic
					score = 70.0
				} else if embedPattern.MatchString(line) {
					rel = RelEmbedder
					prec = PrecisionPrecise
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
				})
				break
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
	assembly *Assembler
}

// NewImpactAnalyzer creates an ImpactAnalyzer with default GoGrepAdapter.
func NewImpactAnalyzer(s *store.Store, policy *dlp.PolicyEngine) *ImpactAnalyzer {
	return &ImpactAnalyzer{
		store:    s,
		policy:   policy,
		adapter:  NewGoGrepAdapter(s, policy),
		assembly: NewAssembler(s, policy),
	}
}

// AnalyzeSymbol discovers references to a symbol and packs them into a ContextPack.
func (ia *ImpactAnalyzer) AnalyzeSymbol(workspaceRoot, symbolName string, budget int, includeGenerated bool) (*ContextPack, error) {
	sym := Symbol{Name: symbolName}
	return ia.packReferences(workspaceRoot, fmt.Sprintf("impact:%s", symbolName), []Symbol{sym}, budget, includeGenerated)
}

// AnalyzeDiff extracts changed symbols from diff text and packs their references into a ContextPack.
func (ia *ImpactAnalyzer) AnalyzeDiff(workspaceRoot, diffText string, budget int, includeGenerated bool) (*ContextPack, error) {
	symbols := ExtractSymbolsFromDiff(diffText)
	if len(symbols) == 0 {
		return &ContextPack{
			Query:       "impact:diff",
			Budget:      budget,
			GeneratedAt: time.Now().UTC(),
			Coverage: &CoverageReport{
				TotalCandidates:    0,
				IncludedCandidates: 0,
				TruncatedCount:     0,
				TruncatedManifest:  []string{},
			},
		}, nil
	}

	query := fmt.Sprintf("impact:diff (%d symbols)", len(symbols))
	return ia.packReferences(workspaceRoot, query, symbols, budget, includeGenerated)
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

func (ia *ImpactAnalyzer) packReferences(workspaceRoot, query string, symbols []Symbol, budget int, includeGenerated bool) (*ContextPack, error) {
	if budget <= 0 {
		budget = 4000
	}

	var ign *ignore.GitIgnore
	gitIgnorePath := filepath.Join(workspaceRoot, ".gitignore")
	if gitIgnoreContent, err := os.ReadFile(gitIgnorePath); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
	}

	var allRefs []RawReference
	seenKeys := make(map[string]bool)

	for _, sym := range symbols {
		refs, err := ia.adapter.FindReferences(workspaceRoot, sym, ign, includeGenerated)
		if err != nil {
			continue
		}
		for _, ref := range refs {
			key := fmt.Sprintf("%s:%d:%s", ref.FilePath, ref.StartLine, ref.Relationship)
			if !seenKeys[key] {
				seenKeys[key] = true
				allRefs = append(allRefs, ref)
			}
		}
	}

	totalCandidates := len(allRefs)

	// Sort candidates:
	// 1. Precision tier: precise > syntactic > inferred
	// 2. Score DESC
	// 3. FilePath ASC, StartLine ASC
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

	sort.SliceStable(allRefs, func(i, j int) bool {
		rI := precisionRank(allRefs[i].Precision)
		rJ := precisionRank(allRefs[j].Precision)
		if rI != rJ {
			return rI < rJ
		}
		if allRefs[i].Score != allRefs[j].Score {
			return allRefs[i].Score > allRefs[j].Score
		}
		if allRefs[i].FilePath != allRefs[j].FilePath {
			return allRefs[i].FilePath < allRefs[j].FilePath
		}
		return allRefs[i].StartLine < allRefs[j].StartLine
	})

	pack := &ContextPack{
		Query:       query,
		Budget:      budget,
		GeneratedAt: time.Now().UTC(),
	}

	used := 0
	var truncatedManifest []string

	for _, ref := range allRefs {
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
	pack.Coverage = &CoverageReport{
		TotalCandidates:    totalCandidates,
		IncludedCandidates: len(pack.Items),
		TruncatedCount:     len(truncatedManifest),
		TruncatedManifest:  truncatedManifest,
		FallbackUsed:       false,
	}

	return pack, nil
}
