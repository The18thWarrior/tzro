package context

import (
	stdctx "context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// PythonAdapter provides AST- and module-aware reference discovery for Python.
type PythonAdapter struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewPythonAdapter creates a new Python reference adapter.
func NewPythonAdapter(s *store.Store, policy *dlp.PolicyEngine) *PythonAdapter {
	return &PythonAdapter{
		store:  s,
		policy: policy,
	}
}

// FindReferences finds references for a single symbol.
func (a *PythonAdapter) FindReferences(workspaceRoot string, sym Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()
	refs, _, err := a.FindReferencesBatch(ctx, workspaceRoot, []Symbol{sym}, excludes, includeGenerated)
	return refs, err
}

// isPyTestFile checks if a file matches Python test conventions.
func isPyTestFile(relPath string) bool {
	base := strings.ToLower(filepath.Base(relPath))
	dir := strings.ToLower(filepath.Dir(relPath))
	if base == "conftest.py" {
		return true
	}
	if strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") {
		return true
	}
	if strings.HasSuffix(base, "_test.py") {
		return true
	}
	if strings.Contains(dir, "tests") || strings.Contains(dir, "testing") {
		return strings.HasSuffix(base, ".py")
	}
	return false
}

// pyImportBinding represents an import binding in Python.
type pyImportBinding struct {
	ModulePath     string // e.g. "foo.bar" or relative ".bar"
	ImportedSymbol string // e.g. "Baz", or "" if whole module imported
	LocalAlias     string // e.g. "b"
	IsStarImport   bool   // from foo import *
	IsRelative     bool   // from . / ..
	RelativeDots   int    // number of leading dots
	LineNum        int
	SourceFile     string
}

// FindReferencesBatch executes batch reference analysis for Python symbols.
func (a *PythonAdapter) FindReferencesBatch(
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

	defaultIgnores := map[string]bool{
		".git":          true,
		".tzro":         true,
		"__pycache__":   true,
		".venv":         true,
		"venv":          true,
		"env":           true,
		".env":          true,
		".tox":          true,
		".pytest_cache": true,
		".mypy_cache":   true,
		".ruff_cache":   true,
		"site-packages": true,
		"node_modules":  true,
		"vendor":        true,
		"dist":          true,
		"build":         true,
		".eggs":         true,
	}

	symMap := make(map[string]Symbol)
	for _, sym := range symbols {
		if sym.Name != "" {
			symMap[sym.Name] = sym
		}
	}

	// 1. Discover all Python files and configs
	var pyFiles []string
	var configFiles []string
	hasSrcLayout := false

	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(workspaceRoot, path)
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			name := d.Name()
			if defaultIgnores[name] || strings.HasSuffix(name, ".egg-info") {
				return filepath.SkipDir
			}
			if excludes != nil && excludes.MatchesPath(relPath) {
				return filepath.SkipDir
			}
			if relPath == "src" {
				hasSrcLayout = true
			}
			return nil
		}

		if excludes != nil && excludes.MatchesPath(relPath) {
			return nil
		}
		if a.policy != nil && !a.policy.EvaluatePath(relPath).Allowed {
			return nil
		}

		base := strings.ToLower(d.Name())
		ext := strings.ToLower(filepath.Ext(relPath))

		if ext == ".py" {
			pyFiles = append(pyFiles, relPath)
		} else if base == "pyproject.toml" || base == "setup.cfg" || base == "setup.py" {
			configFiles = append(configFiles, relPath)
		}
		return nil
	})

	var refs []RawReference
	fileContents := make(map[string]string)
	fileLines := make(map[string][]string)

	readFile := func(relPath string) (string, []string, error) {
		if c, ok := fileContents[relPath]; ok {
			return c, fileLines[relPath], nil
		}
		full := filepath.Join(workspaceRoot, relPath)
		data, err := os.ReadFile(full)
		if err != nil {
			return "", nil, err
		}
		content := string(data)
		lines := strings.Split(content, "\n")
		fileContents[relPath] = content
		fileLines[relPath] = lines
		return content, lines, nil
	}

	// Helper to normalize relative paths to workspaceRoot
	cleanRel := func(p string) string {
		if filepath.IsAbs(p) {
			if rel, err := filepath.Rel(workspaceRoot, p); err == nil {
				return filepath.Clean(rel)
			}
		}
		return filepath.Clean(p)
	}

	// 2. Scan each file for import bindings and dynamic patterns
	fileBindings := make(map[string][]pyImportBinding)
	fileReExports := make(map[string][]pyImportBinding) // module file -> re-exports

	for _, pyFile := range pyFiles {
		content, _, err := readFile(pyFile)
		if err != nil {
			continue
		}

		bindings, dynamicPatterns := extractPyBindings(pyFile, []byte(content))
		fileBindings[pyFile] = bindings

		for _, dyn := range dynamicPatterns {
			if dyn.IsStarImport {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("star import: from %s import * in %s", dyn.Detail, pyFile))
				cov.IncompleteDiscovery = true
			} else if dyn.IsDynamicImport {
				cov.UnresolvedImports = append(cov.UnresolvedImports, fmt.Sprintf("dynamic import: %s in %s", dyn.Detail, pyFile))
				cov.IncompleteDiscovery = true
			} else if dyn.IsDynamicAttr {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("dynamic attribute access: %s in %s", dyn.Detail, pyFile))
				cov.IncompleteDiscovery = true
			}
		}

		// Collect re-exports: if this file is __init__.py or a module that imports symbols
		for _, b := range bindings {
			if b.ImportedSymbol != "" {
				fileReExports[pyFile] = append(fileReExports[pyFile], b)
			}
		}
	}

	// 3. Resolve symbol references across files
	for _, sym := range symbols {
		symCopy := sym
		symPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym.Name) + `\b`)

		// Find defining file if sym.FilePath is given
		var definingFileRel string
		if sym.FilePath != "" {
			definingFileRel = cleanRel(sym.FilePath)
		}

		// Re-export resolution: trace files that re-export this symbol
		validSourceFiles := make(map[string]bool)
		if definingFileRel != "" {
			validSourceFiles[definingFileRel] = true

			// Follow re-exports outwards: find files importing from definingFileRel and re-exporting
			visited := make(map[string]bool)
			var queue []string
			queue = append(queue, definingFileRel)
			visited[definingFileRel] = true

			for len(queue) > 0 {
				curr := queue[0]
				queue = queue[1:]

				for candidateFile, reExports := range fileReExports {
					if visited[candidateFile] {
						continue
					}
					for _, rx := range reExports {
						if rx.ImportedSymbol == sym.Name || rx.IsStarImport {
							resolvedTargets := resolvePyModule(workspaceRoot, candidateFile, rx.ModulePath, rx.RelativeDots, hasSrcLayout)
							for _, rt := range resolvedTargets {
								if rt == curr {
									validSourceFiles[candidateFile] = true
									visited[candidateFile] = true
									queue = append(queue, candidateFile)
									break
								}
							}
						}
					}
				}
			}
		}

		// Check if the symbol is in conftest.py or is a shared fixture
		isConftestSymbol := false
		if definingFileRel != "" && filepath.Base(definingFileRel) == "conftest.py" {
			isConftestSymbol = true
		}

		for _, pyFile := range pyFiles {
			// Skip defining file itself for external references (unless self-referencing check)
			if definingFileRel != "" && pyFile == definingFileRel {
				continue
			}

			content, lines, err := readFile(pyFile)
			if err != nil {
				continue
			}

			if !symPattern.MatchString(content) {
				// Check inferred test match by naming convention
				if definingFileRel != "" && isPyTestFile(pyFile) {
					base := strings.TrimSuffix(filepath.Base(definingFileRel), ".py")
					testBase := strings.ToLower(filepath.Base(pyFile))
					if strings.Contains(testBase, base) {
						refs = append(refs, RawReference{
							FilePath:     pyFile,
							StartLine:    1,
							EndLine:      min(30, len(lines)),
							Precision:    PrecisionInferred,
							Relationship: RelTest,
							Content:      strings.Join(lines[:min(30, len(lines))], "\n"),
							SymbolName:   sym.Name,
							Score:        40.0,
							SourceSymbol: &symCopy,
						})
					}
				}
				continue
			}

			// Check import bindings in pyFile to determine how sym is referenced
			bindings := fileBindings[pyFile]
			isTest := isPyTestFile(pyFile)

			isDirectlyImported := false
			isModuleImported := false
			directAlias := sym.Name
			var moduleAliases []string

			for _, b := range bindings {
				resolvedTargets := resolvePyModule(workspaceRoot, pyFile, b.ModulePath, b.RelativeDots, hasSrcLayout)
				targetsMatch := false
				if len(validSourceFiles) == 0 {
					targetsMatch = true
				} else {
					for _, rt := range resolvedTargets {
						if validSourceFiles[rt] {
							targetsMatch = true
							break
						}
					}
				}

				if targetsMatch {
					if b.ImportedSymbol == sym.Name {
						isDirectlyImported = true
						if b.LocalAlias != "" {
							directAlias = b.LocalAlias
						}
					} else if b.ImportedSymbol == "" && !b.IsStarImport {
						// Entire module imported, e.g. import foo or import foo as f
						isModuleImported = true
						alias := b.ModulePath
						if b.LocalAlias != "" {
							alias = b.LocalAlias
						} else if idx := strings.LastIndex(alias, "."); idx != -1 {
							alias = alias[idx+1:]
						}
						moduleAliases = append(moduleAliases, alias)
					} else if b.IsStarImport {
						isDirectlyImported = true
					}
				}
			}

			// If neither directly imported nor module imported, but in the same directory/package, could be sibling
			isSibling := false
			if definingFileRel != "" && filepath.Dir(pyFile) == filepath.Dir(definingFileRel) {
				isSibling = true
			}

			// Detect local shadowing in pyFile
			shadowedRanges := findPyShadowedRanges(pyFile, []byte(content), sym.Name)

			// Scan lines for occurrences
			for i, line := range lines {
				lineNum := i + 1

				// Check if this line is in a shadowed range
				isShadowed := false
				for _, sr := range shadowedRanges {
					if lineNum >= sr.start && lineNum <= sr.end {
						isShadowed = true
						break
					}
				}
				if isShadowed {
					continue
				}

				// Check matching: direct call, aliased call, or module-qualified call (module.sym)
				matched := false
				if isDirectlyImported || isSibling || len(validSourceFiles) == 0 {
					pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(directAlias) + `\b`)
					if pattern.MatchString(line) {
						// Don't match the import statement line itself
						if !strings.HasPrefix(strings.TrimSpace(line), "import ") && !strings.HasPrefix(strings.TrimSpace(line), "from ") {
							matched = true
						}
					}
				}
				if !matched && isModuleImported {
					for _, modAlias := range moduleAliases {
						qualPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(modAlias) + `\.` + regexp.QuoteMeta(sym.Name) + `\b`)
						if qualPattern.MatchString(line) {
							matched = true
							break
						}
					}
				}

				if matched {
					start := max(1, lineNum-4)
					end := min(len(lines), lineNum+10)
					snippet := strings.Join(lines[start-1:end], "\n")

					rel := RelCaller
					prec := PrecisionSyntactic
					score := 80.0

					if isTest {
						rel = RelTest
						score = 70.0
					}

					refs = append(refs, RawReference{
						FilePath:     pyFile,
						StartLine:    start,
						EndLine:      end,
						Precision:    prec,
						Relationship: rel,
						Content:      snippet,
						SymbolName:   sym.Name,
						Score:        score,
						SourceSymbol: &symCopy,
					})
					break // One reference block per file is sufficient for candidate tracking
				}
			}
		}

		// Broaden test selection for conftest.py changes
		if isConftestSymbol {
			conftestDir := filepath.Dir(definingFileRel)
			for _, pyFile := range pyFiles {
				if isPyTestFile(pyFile) && (conftestDir == "." || strings.HasPrefix(pyFile, conftestDir)) {
					// Check if already in refs
					alreadyIn := false
					for _, r := range refs {
						if r.FilePath == pyFile && r.SymbolName == sym.Name {
							alreadyIn = true
							break
						}
					}
					if !alreadyIn {
						content, lines, _ := readFile(pyFile)
						refs = append(refs, RawReference{
							FilePath:     pyFile,
							StartLine:    1,
							EndLine:      min(30, len(lines)),
							Precision:    PrecisionInferred,
							Relationship: RelTest,
							Content:      content,
							SymbolName:   sym.Name,
							Score:        55.0,
							SourceSymbol: &symCopy,
						})
					}
				}
			}
		}

		// Scan configuration files statically (pyproject.toml, setup.cfg, setup.py)
		for _, cfgFile := range configFiles {
			content, lines, err := readFile(cfgFile)
			if err != nil {
				continue
			}
			if symPattern.MatchString(content) {
				for i, line := range lines {
					if symPattern.MatchString(line) {
						start := max(1, i-1)
						end := min(len(lines), i+3)
						refs = append(refs, RawReference{
							FilePath:     cfgFile,
							StartLine:    start,
							EndLine:      end,
							Precision:    PrecisionSyntactic,
							Relationship: RelConfig,
							Content:      strings.Join(lines[start-1:end], "\n"),
							SymbolName:   sym.Name,
							Score:        50.0,
							SourceSymbol: &symCopy,
						})
						break
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

type pyDynamicPattern struct {
	IsStarImport    bool
	IsDynamicImport bool
	IsDynamicAttr   bool
	Detail          string
}

type pyShadowRange struct {
	start int
	end   int
}

// resolvePyModule converts a module path (e.g. "foo.bar" or relative import) to candidate relative file paths.
func resolvePyModule(workspaceRoot, currentFile, modulePath string, relativeDots int, hasSrcLayout bool) []string {
	var candidates []string

	if relativeDots > 0 {
		// Relative import from currentFile's directory
		baseDir := filepath.Dir(currentFile)
		for i := 1; i < relativeDots; i++ {
			baseDir = filepath.Dir(baseDir)
		}
		modAsPath := strings.ReplaceAll(modulePath, ".", string(filepath.Separator))
		targetBase := filepath.Join(baseDir, modAsPath)

		candidates = append(candidates,
			targetBase+".py",
			filepath.Join(targetBase, "__init__.py"),
		)
		return candidates
	}

	// Absolute package import
	modAsPath := strings.ReplaceAll(modulePath, ".", string(filepath.Separator))

	// Direct root layout
	candidates = append(candidates,
		modAsPath+".py",
		filepath.Join(modAsPath, "__init__.py"),
	)

	// src layout
	if hasSrcLayout {
		candidates = append(candidates,
			filepath.Join("src", modAsPath+".py"),
			filepath.Join("src", modAsPath, "__init__.py"),
		)
	}

	// Also check if modulePath has a sub-component, e.g. "foo.bar" where "foo.py" is the file
	parts := strings.Split(modulePath, ".")
	if len(parts) > 1 {
		prefixPath := strings.Join(parts[:len(parts)-1], string(filepath.Separator))
		candidates = append(candidates, prefixPath+".py")
		if hasSrcLayout {
			candidates = append(candidates, filepath.Join("src", prefixPath+".py"))
		}
	}

	return candidates
}

// extractPyBindings extracts imports, star imports, dynamic imports, and getattr calls.
func extractPyBindings(filePath string, source []byte) ([]pyImportBinding, []pyDynamicPattern) {
	var bindings []pyImportBinding
	var dynamicPatterns []pyDynamicPattern

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return extractPyBindingsLexical(filePath, string(source))
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return extractPyBindingsLexical(filePath, string(source))
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return extractPyBindingsLexical(filePath, string(source))
	}

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nt := bt.NodeType(node)

		// 1. import foo [as f]
		if nt == "import_statement" {
			lineNum := int(node.StartPoint().Row) + 1
			for i := 0; i < node.ChildCount(); i++ {
				child := node.Child(i)
				ct := bt.NodeType(child)
				if ct == "dotted_name" {
					modName := string(source[child.StartByte():child.EndByte()])
					bindings = append(bindings, pyImportBinding{
						ModulePath: modName,
						LineNum:    lineNum,
						SourceFile: filePath,
					})
				} else if ct == "aliased_import" {
					nameNode := bt.ChildByField(child, "name")
					aliasNode := bt.ChildByField(child, "alias")
					if nameNode != nil {
						modName := string(source[nameNode.StartByte():nameNode.EndByte()])
						alias := ""
						if aliasNode != nil {
							alias = string(source[aliasNode.StartByte():aliasNode.EndByte()])
						}
						bindings = append(bindings, pyImportBinding{
							ModulePath: modName,
							LocalAlias: alias,
							LineNum:    lineNum,
							SourceFile: filePath,
						})
					}
				}
			}
		} else if nt == "import_from_statement" {
			// from <module> import <names>
			lineNum := int(node.StartPoint().Row) + 1
			modName := ""
			relDots := 0
			isRelative := false

			modNode := bt.ChildByField(node, "module_name")
			if modNode != nil {
				mt := bt.NodeType(modNode)
				rawMod := string(source[modNode.StartByte():modNode.EndByte()])
				if mt == "relative_import" {
					isRelative = true
					for _, ch := range rawMod {
						if ch == '.' {
							relDots++
						} else {
							break
						}
					}
					modName = strings.TrimLeft(rawMod, ".")
				} else {
					modName = rawMod
				}
			} else {
				// from . import foo (module_name might be nil when it starts with dots)
				rawStmt := string(source[node.StartByte():node.EndByte()])
				if strings.HasPrefix(strings.TrimSpace(rawStmt), "from .") {
					isRelative = true
					relDots = 1
					for i := 6; i < len(rawStmt) && rawStmt[i] == '.'; i++ {
						relDots++
					}
				}
			}

			// Check wildcard import
			rawStmt := string(source[node.StartByte():node.EndByte()])
			if strings.Contains(rawStmt, " import *") {
				dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
					IsStarImport: true,
					Detail:       modName,
				})
				bindings = append(bindings, pyImportBinding{
					ModulePath:   modName,
					IsStarImport: true,
					IsRelative:   isRelative,
					RelativeDots: relDots,
					LineNum:      lineNum,
					SourceFile:   filePath,
				})
			}

			// Check children for imported names
			for i := 0; i < node.ChildCount(); i++ {
				child := node.Child(i)
				ct := bt.NodeType(child)
				if ct == "dotted_name" && child != modNode {
					sym := string(source[child.StartByte():child.EndByte()])
					bindings = append(bindings, pyImportBinding{
						ModulePath:     modName,
						ImportedSymbol: sym,
						LocalAlias:     sym,
						IsRelative:     isRelative,
						RelativeDots:   relDots,
						LineNum:        lineNum,
						SourceFile:     filePath,
					})
				} else if ct == "aliased_import" {
					nameNode := bt.ChildByField(child, "name")
					aliasNode := bt.ChildByField(child, "alias")
					if nameNode != nil {
						sym := string(source[nameNode.StartByte():nameNode.EndByte()])
						alias := sym
						if aliasNode != nil {
							alias = string(source[aliasNode.StartByte():aliasNode.EndByte()])
						}
						bindings = append(bindings, pyImportBinding{
							ModulePath:     modName,
							ImportedSymbol: sym,
							LocalAlias:     alias,
							IsRelative:     isRelative,
							RelativeDots:   relDots,
							LineNum:        lineNum,
							SourceFile:     filePath,
						})
					}
				}
			}
		} else if nt == "call" {
			// Check for importlib.import_module, __import__, getattr, setattr
			fnNode := bt.ChildByField(node, "function")
			if fnNode != nil {
				fnName := string(source[fnNode.StartByte():fnNode.EndByte()])
				if strings.Contains(fnName, "import_module") || fnName == "__import__" {
					dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
						IsDynamicImport: true,
						Detail:          fnName,
					})
				} else if fnName == "getattr" || fnName == "setattr" {
					dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
						IsDynamicAttr: true,
						Detail:        fnName,
					})
				}
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return bindings, dynamicPatterns
}

// extractPyBindingsLexical provides fallback parsing of Python import lines.
func extractPyBindingsLexical(filePath, source string) ([]pyImportBinding, []pyDynamicPattern) {
	var bindings []pyImportBinding
	var dynamicPatterns []pyDynamicPattern

	lines := strings.Split(source, "\n")
	importRe := regexp.MustCompile(`^import\s+([a-zA-Z0-9_.,\s]+)`)
	fromRe := regexp.MustCompile(`^from\s+([a-zA-Z0-9_.]+)\s+import\s+(.*)`)
	starRe := regexp.MustCompile(`^from\s+([a-zA-Z0-9_.]+)\s+import\s+\*`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if starRe.MatchString(trimmed) {
			m := starRe.FindStringSubmatch(trimmed)
			dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
				IsStarImport: true,
				Detail:       m[1],
			})
			bindings = append(bindings, pyImportBinding{
				ModulePath:   m[1],
				IsStarImport: true,
				LineNum:      lineNum,
				SourceFile:   filePath,
			})
			continue
		}

		if strings.Contains(trimmed, "importlib.import_module") || strings.Contains(trimmed, "__import__(") {
			dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
				IsDynamicImport: true,
				Detail:          "importlib",
			})
		}
		if strings.Contains(trimmed, "getattr(") || strings.Contains(trimmed, "setattr(") {
			dynamicPatterns = append(dynamicPatterns, pyDynamicPattern{
				IsDynamicAttr: true,
				Detail:        "getattr/setattr",
			})
		}

		if fromRe.MatchString(trimmed) {
			m := fromRe.FindStringSubmatch(trimmed)
			mod := m[1]
			syms := strings.Split(m[2], ",")
			relDots := 0
			for _, ch := range mod {
				if ch == '.' {
					relDots++
				} else {
					break
				}
			}
			isRel := relDots > 0

			for _, s := range syms {
				s = strings.TrimSpace(s)
				alias := s
				if strings.Contains(s, " as ") {
					parts := strings.Split(s, " as ")
					s = strings.TrimSpace(parts[0])
					alias = strings.TrimSpace(parts[1])
				}
				bindings = append(bindings, pyImportBinding{
					ModulePath:     mod,
					ImportedSymbol: s,
					LocalAlias:     alias,
					IsRelative:     isRel,
					RelativeDots:   relDots,
					LineNum:        lineNum,
					SourceFile:     filePath,
				})
			}
		} else if importRe.MatchString(trimmed) {
			m := importRe.FindStringSubmatch(trimmed)
			mods := strings.Split(m[1], ",")
			for _, mod := range mods {
				mod = strings.TrimSpace(mod)
				alias := ""
				if strings.Contains(mod, " as ") {
					parts := strings.Split(mod, " as ")
					mod = strings.TrimSpace(parts[0])
					alias = strings.TrimSpace(parts[1])
				}
				bindings = append(bindings, pyImportBinding{
					ModulePath: mod,
					LocalAlias: alias,
					LineNum:    lineNum,
					SourceFile: filePath,
				})
			}
		}
	}

	return bindings, dynamicPatterns
}

// findPyShadowedRanges identifies function/method scopes in Python where symName is shadowed by a parameter or assignment.
func findPyShadowedRanges(filePath string, source []byte, symName string) []pyShadowRange {
	var ranges []pyShadowRange

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return nil
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return nil
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return nil
	}

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nt := bt.NodeType(node)

		if nt == "function_definition" {
			paramsNode := bt.ChildByField(node, "parameters")
			isShadowed := false

			// Check parameters
			if paramsNode != nil {
				paramText := string(source[paramsNode.StartByte():paramsNode.EndByte()])
				paramPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(symName) + `\b`)
				if paramPattern.MatchString(paramText) {
					isShadowed = true
				}
			}

			// Check assignments inside function body
			if !isShadowed {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil {
					assignPattern := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(symName) + `\s*=`)
					bodyText := string(source[bodyNode.StartByte():bodyNode.EndByte()])
					if assignPattern.MatchString(bodyText) {
						isShadowed = true
					}
				}
			}

			if isShadowed {
				startLine := int(node.StartPoint().Row) + 1
				endLine := int(node.EndPoint().Row) + 1
				ranges = append(ranges, pyShadowRange{start: startLine, end: endLine})
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return ranges
}
