package context

import (
	"bufio"
	"bytes"
	stdctx "context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// BatchedReferenceAdapter is an optional extension for ReferenceAdapter
// that supports batched symbol lookup, cancellation, and explicit coverage reporting.
type BatchedReferenceAdapter interface {
	ReferenceAdapter
	FindReferencesBatch(ctx stdctx.Context, workspaceRoot string, symbols []Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, *CoverageReport, error)
}

// AdapterRegistry manages language-specific reference adapters.
type AdapterRegistry struct {
	mu       sync.RWMutex
	adapters map[string]ReferenceAdapter
}

// NewAdapterRegistry creates an AdapterRegistry initialized with default adapters.
func NewAdapterRegistry(s *store.Store, policy *dlp.PolicyEngine) *AdapterRegistry {
	r := &AdapterRegistry{
		adapters: make(map[string]ReferenceAdapter),
	}
	goAdapter := NewGoRipgrepAdapter(s, policy)
	r.Register("go", goAdapter)
	tsAdapter := NewTypeScriptAdapter(s, policy)
	r.Register("typescript", tsAdapter)
	r.Register("javascript", tsAdapter)
	pyAdapter := NewPythonAdapter(s, policy)
	r.Register("python", pyAdapter)
	rustAdapter := NewRustAdapter(s, policy)
	r.Register("rust", rustAdapter)
	return r
}

// Register registers a reference adapter for a language.
func (r *AdapterRegistry) Register(language string, adapter ReferenceAdapter) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.adapters[strings.ToLower(language)] = adapter
}

// Get retrieves the reference adapter for a language.
func (r *AdapterRegistry) Get(language string) (ReferenceAdapter, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.adapters[strings.ToLower(language)]
	return a, ok
}

// SupportedLanguages returns a sorted list of registered language identifiers.
func (r *AdapterRegistry) SupportedLanguages() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var langs []string
	for k := range r.adapters {
		langs = append(langs, k)
	}
	sort.Strings(langs)
	return langs
}

// FindReferences finds incoming references for a symbol across registered adapters.
func (r *AdapterRegistry) FindReferences(ctx stdctx.Context, workspaceRoot, filePath, symbolName string) ([]RawReference, *CoverageReport, error) {
	lang := DetectLanguage(filePath)
	adapter, ok := r.Get(lang)
	if !ok {
		adapter, ok = r.Get("go")
	}
	if adapter == nil {
		return nil, nil, fmt.Errorf("no adapter registered for language %q", lang)
	}

	sym := Symbol{
		Name:      symbolName,
		FilePath:  filePath,
		Language:  lang,
		Workspace: workspaceRoot,
	}

	if bAdapter, isBatch := adapter.(BatchedReferenceAdapter); isBatch {
		return bAdapter.FindReferencesBatch(ctx, workspaceRoot, []Symbol{sym}, nil, false)
	}
	refs, err := adapter.FindReferences(workspaceRoot, sym, nil, false)
	return refs, nil, err
}

// DetectLanguage determines language from file path extension.
func DetectLanguage(filePath string) string {
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return "typescript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".yaml", ".yml", ".json", ".toml":
		return "config"
	default:
		return ""
	}
}

// GoRipgrepAdapter uses ripgrep for fast reference discovery, with fallback to GoGrepAdapter.
type GoRipgrepAdapter struct {
	store    *store.Store
	policy   *dlp.PolicyEngine
	fallback *GoGrepAdapter
	rgPath   string
}

// NewGoRipgrepAdapter creates a new GoRipgrepAdapter.
func NewGoRipgrepAdapter(s *store.Store, policy *dlp.PolicyEngine) *GoRipgrepAdapter {
	rg, _ := exec.LookPath("rg")
	return &GoRipgrepAdapter{
		store:    s,
		policy:   policy,
		fallback: NewGoGrepAdapter(s, policy),
		rgPath:   rg,
	}
}

// SetRipgrepPath overrides the ripgrep executable path (useful for testing).
func (a *GoRipgrepAdapter) SetRipgrepPath(path string) {
	a.rgPath = path
}

// FindReferences finds references for a single symbol.
func (a *GoRipgrepAdapter) FindReferences(workspaceRoot string, sym Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()
	refs, _, err := a.FindReferencesBatch(ctx, workspaceRoot, []Symbol{sym}, excludes, includeGenerated)
	return refs, err
}

// rgJSONLine represents a single output line from `rg --json`.
type rgJSONLine struct {
	Type string `json:"type"`
	Data struct {
		Path struct {
			Text string `json:"text"`
		} `json:"path"`
		Lines struct {
			Text string `json:"text"`
		} `json:"lines"`
		LineNumber int `json:"line_number"`
		Submatches []struct {
			Match struct {
				Text string `json:"text"`
			} `json:"match"`
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"submatches"`
	} `json:"data"`
}

// FindReferencesBatch executes a batched ripgrep search for symbols across the workspace.
func (a *GoRipgrepAdapter) FindReferencesBatch(
	ctx stdctx.Context,
	workspaceRoot string,
	symbols []Symbol,
	excludes *ignore.GitIgnore,
	includeGenerated bool,
) ([]RawReference, *CoverageReport, error) {
	cov := &CoverageReport{}

	if len(symbols) == 0 {
		cov.NoChanges = true
		return nil, cov, nil
	}

	// If rg is not available, execute fallback immediately
	if a.rgPath == "" {
		cov.FallbackUsed = true
		cov.FallbackReason = "ripgrep not available on PATH"
		refs, err := a.fallbackFindReferencesBatch(workspaceRoot, symbols, excludes, includeGenerated)
		if err != nil {
			cov.AdapterErrors = append(cov.AdapterErrors, err.Error())
		}
		return refs, cov, err
	}

	// Prepare ripgrep arguments safely as array
	// rg --json --line-number --color never --no-heading -w -e sym1 -e sym2 ...
	args := []string{
		"--json",
		"--line-number",
		"--color", "never",
		"--no-heading",
		"-w",
		"-g", "*.go",
		"-g", "*.yaml",
		"-g", "*.yml",
		"-g", "*.json",
		"-g", "*.toml",
		"-g", "!vendor/**",
		"-g", "!node_modules/**",
		"-g", "!.git/**",
		"-g", "!.tzro/**",
	}

	symMap := make(map[string]Symbol)
	for _, sym := range symbols {
		if sym.Name != "" {
			args = append(args, "-e", sym.Name)
			symMap[sym.Name] = sym
		}
	}

	cmd := exec.CommandContext(ctx, a.rgPath, args...)
	cmd.Dir = workspaceRoot

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	execErr := cmd.Run()
	// rg exits with code 1 when no matches are found, which is normal
	if execErr != nil {
		exitCode := cmd.ProcessState.ExitCode()
		if exitCode != 1 {
			// Subprocess failure or timeout -> trigger fallback
			cov.FallbackUsed = true
			cov.FallbackReason = fmt.Sprintf("ripgrep error (exit %d): %v, stderr: %s", exitCode, execErr, stderrBuf.String())
			refs, err := a.fallbackFindReferencesBatch(workspaceRoot, symbols, excludes, includeGenerated)
			if err != nil {
				cov.AdapterErrors = append(cov.AdapterErrors, err.Error())
			}
			return refs, cov, err
		}
	}

	// Parse ripgrep JSON output
	var refs []RawReference
	scanner := bufio.NewScanner(&stdoutBuf)
	// Cache file contents to avoid re-reading files for surrounding context
	fileCache := make(map[string][]string)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var parsed rgJSONLine
		if err := json.Unmarshal(line, &parsed); err != nil {
			continue
		}
		if parsed.Type != "match" {
			continue
		}

		relPath := parsed.Data.Path.Text
		if filepath.IsAbs(relPath) {
			r, err := filepath.Rel(workspaceRoot, relPath)
			if err == nil {
				relPath = r
			}
		}

		// Check excludes
		if excludes != nil && excludes.MatchesPath(relPath) {
			continue
		}
		if !includeGenerated && isGeneratedFile(relPath) {
			continue
		}
		if a.policy != nil {
			eval := a.policy.EvaluatePath(relPath)
			if !eval.Allowed {
				continue
			}
		}

		matchedText := parsed.Data.Lines.Text
		lineNum := parsed.Data.LineNumber

		// Load file lines for context snippet
		lines, cached := fileCache[relPath]
		if !cached {
			fullPath := filepath.Join(workspaceRoot, relPath)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				cov.AdapterErrors = append(cov.AdapterErrors, fmt.Sprintf("read error %s: %v", relPath, err))
				continue
			}
			lines = strings.Split(string(data), "\n")
			fileCache[relPath] = lines
		}

		start := max(1, lineNum-4)
		end := min(len(lines), lineNum+10)
		snippet := strings.Join(lines[start-1:end], "\n")

		ext := strings.ToLower(filepath.Ext(relPath))
		isConfig := ext == ".yaml" || ext == ".yml" || ext == ".json" || ext == ".toml"
		isTestFile := strings.HasSuffix(relPath, "_test.go")

		for _, sub := range parsed.Data.Submatches {
			mText := sub.Match.Text
			targetSym, ok := symMap[mText]
			if !ok {
				continue
			}

			rel := RelCaller
			prec := PrecisionSyntactic
			score := 80.0

			if isConfig {
				rel = RelConfig
				prec = PrecisionSyntactic
				score = 50.0
			} else if isTestFile {
				rel = RelTest
				prec = PrecisionSyntactic
				score = 70.0
			} else if strings.TrimSpace(matchedText) == mText {
				rel = RelEmbedder
				prec = PrecisionSyntactic
				score = 90.0
			} else if strings.Contains(matchedText, "func ") && strings.Contains(matchedText, "("+mText+")") {
				rel = RelImplementor
				prec = PrecisionSyntactic
				score = 85.0
			}

			symCopy := targetSym
			refs = append(refs, RawReference{
				FilePath:     relPath,
				StartLine:    start,
				EndLine:      end,
				Precision:    prec,
				Relationship: rel,
				Content:      snippet,
				SymbolName:   mText,
				Score:        score,
				SourceSymbol: &symCopy,
			})
		}
	}

	// Also check inferred test files that match naming conventions without explicit calls
	for _, sym := range symbols {
		if sym.FilePath != "" {
			baseTarget := strings.TrimSuffix(filepath.Base(sym.FilePath), ".go")
			testRel := filepath.Join(filepath.Dir(sym.FilePath), baseTarget+"_test.go")
			fullTest := filepath.Join(workspaceRoot, testRel)
			if _, err := os.Stat(fullTest); err == nil {
				// Verify if not already added
				alreadyAdded := false
				for _, r := range refs {
					if r.FilePath == testRel && r.SymbolName == sym.Name {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					if a.policy == nil || a.policy.EvaluatePath(testRel).Allowed {
						symCopy := sym
						contentBytes, _ := os.ReadFile(fullTest)
						contentStr := string(contentBytes)
						refs = append(refs, RawReference{
							FilePath:     testRel,
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
				}
			}
		}
	}

	if len(refs) == 0 {
		cov.NoReferences = true
	}

	return refs, cov, nil
}

// fallbackFindReferencesBatch uses GoGrepAdapter across all symbols, collecting all matches per file.
func (a *GoRipgrepAdapter) fallbackFindReferencesBatch(
	workspaceRoot string,
	symbols []Symbol,
	excludes *ignore.GitIgnore,
	includeGenerated bool,
) ([]RawReference, error) {
	var allRefs []RawReference
	for _, sym := range symbols {
		refs, err := a.fallback.FindReferences(workspaceRoot, sym, excludes, includeGenerated)
		if err != nil {
			continue
		}
		allRefs = append(allRefs, refs...)
	}
	return allRefs, nil
}

// maxDeclCandidates caps the number of declaration candidates returned to prevent
// quadratic blowup on high-fanout symbols (e.g., Error, String, Close).
const maxDeclCandidates = 50

// FindDeclarations finds candidate declaration locations for a symbol in workspace.
// The provided context is checked between files so callers can enforce timeouts.
// Directories and files matched by the workspace .gitignore are skipped.
func FindDeclarations(ctx stdctx.Context, workspaceRoot, symbolName string) ([]Symbol, error) {
	var results []Symbol
	declRe := regexp.MustCompile(`(?m)^type\s+` + regexp.QuoteMeta(symbolName) + `\b|^func\s+(\([^)]+\)\s+)?` + regexp.QuoteMeta(symbolName) + `\b|^var\s+` + regexp.QuoteMeta(symbolName) + `\b|^const\s+` + regexp.QuoteMeta(symbolName) + `\b`)

	// Load .gitignore for workspace-aware filtering.
	var ign *ignore.GitIgnore
	if gitIgnoreContent, err := os.ReadFile(filepath.Join(workspaceRoot, ".gitignore")); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
	}

	walkErr := filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		// Check context cancellation between files.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(workspaceRoot, path)

		if d.IsDir() {
			// Always skip .git (never listed in .gitignore itself).
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			// Skip any directory matched by .gitignore.
			if ign != nil && relPath != "." && ign.MatchesPath(relPath+"/") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip files matched by .gitignore.
		if ign != nil && ign.MatchesPath(relPath) {
			return nil
		}

		lang := DetectLanguage(path)
		if lang == "" || lang == "config" {
			return nil
		}

		contentBytes, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		// Declaration names are extracted verbatim from source. Files without the
		// requested name cannot contain a match and do not need AST parsing.
		if !bytes.Contains(contentBytes, []byte(symbolName)) {
			return nil
		}

		// Try AST extraction first
		decls, astErr := extractDeclarationsFromAST(relPath, contentBytes)
		if astErr == nil && len(decls) > 0 {
			for _, d := range decls {
				if d.Name == symbolName {
					results = append(results, d)
				}
			}
			if len(results) >= maxDeclCandidates {
				return fmt.Errorf("cap reached")
			}
			return nil
		}

		// Fallback for Go regex matching
		if strings.HasSuffix(d.Name(), ".go") {
			lines := strings.Split(string(contentBytes), "\n")
			for i, line := range lines {
				if declRe.MatchString(line) {
					kind := "function"
					if strings.HasPrefix(line, "type") {
						kind = "type"
					} else if strings.HasPrefix(line, "var") {
						kind = "variable"
					} else if strings.HasPrefix(line, "const") {
						kind = "constant"
					}
					results = append(results, Symbol{
						Name:      symbolName,
						Kind:      kind,
						FilePath:  relPath,
						StartLine: i + 1,
						Language:  "go",
					})
					if len(results) >= maxDeclCandidates {
						return fmt.Errorf("cap reached")
					}
				}
			}
		}
		return nil
	})

	// Swallow the sentinel "cap reached" error — results are valid.
	if walkErr != nil && walkErr.Error() != "cap reached" && walkErr != stdctx.Canceled && walkErr != stdctx.DeadlineExceeded {
		return results, walkErr
	}

	return results, nil
}

// ResolveSymbolAnchor disambiguates a symbol name. If filePath is empty and multiple declarations
// exist across different files, it returns an ambiguity error with candidates.
// The context is forwarded to FindDeclarations so timeouts are enforced during the walk.
func ResolveSymbolAnchor(ctx stdctx.Context, workspaceRoot, symbolName, filePath string) ([]Symbol, error) {
	decls, err := FindDeclarations(ctx, workspaceRoot, symbolName)
	if err != nil {
		return nil, err
	}

	if filePath != "" {
		var matched []Symbol
		cleanTarget := filepath.Clean(filePath)
		for _, d := range decls {
			if filepath.Clean(d.FilePath) == cleanTarget || strings.HasSuffix(filepath.Clean(d.FilePath), cleanTarget) {
				matched = append(matched, d)
			}
		}
		if len(matched) == 0 {
			// Fallback: create symbol with specified file path
			return []Symbol{{Name: symbolName, FilePath: filePath, Language: DetectLanguage(filePath)}}, nil
		}
		return matched, nil
	}

	// If no filePath disambiguation, check unique files
	fileMap := make(map[string][]Symbol)
	for _, d := range decls {
		fileMap[d.FilePath] = append(fileMap[d.FilePath], d)
	}

	if len(fileMap) > 1 {
		var candidates []string
		for file, ds := range fileMap {
			for _, d := range ds {
				candidates = append(candidates, fmt.Sprintf("%s:%d", file, d.StartLine))
			}
		}
		sort.Strings(candidates)
		return nil, fmt.Errorf("symbol %q is ambiguous; found declarations in: %s. Use --file <path> to disambiguate", symbolName, strings.Join(candidates, ", "))
	}

	if len(decls) == 1 {
		return decls, nil
	}

	// 0 declarations found: return bare symbol for search compatibility
	return []Symbol{{Name: symbolName}}, nil
}
