package context

import (
	stdctx "context"
	"encoding/json"
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

// TypeScriptAdapter provides precise AST- and import-aware reference discovery for TS and JS.
type TypeScriptAdapter struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewTypeScriptAdapter creates a new TypeScript/JavaScript reference adapter.
func NewTypeScriptAdapter(s *store.Store, policy *dlp.PolicyEngine) *TypeScriptAdapter {
	return &TypeScriptAdapter{
		store:  s,
		policy: policy,
	}
}

// FindReferences finds references for a single symbol.
func (a *TypeScriptAdapter) FindReferences(workspaceRoot string, sym Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()
	refs, _, err := a.FindReferencesBatch(ctx, workspaceRoot, []Symbol{sym}, excludes, includeGenerated)
	return refs, err
}

// isTSTestFile checks if a path matches standard TS/JS test conventions.
func isTSTestFile(relPath string) bool {
	lower := strings.ToLower(relPath)
	if strings.Contains(lower, "__tests__/") ||
		strings.Contains(lower, ".test.") ||
		strings.Contains(lower, ".spec.") ||
		strings.HasSuffix(lower, "_test.ts") ||
		strings.HasSuffix(lower, "_test.js") {
		return true
	}
	return false
}

// FindNearestTSConfig locates the closest tsconfig.json or jsconfig.json starting from fileDir up to workspaceRoot.
func FindNearestTSConfig(workspaceRoot, fileDir string) *TSConfig {
	curr := fileDir
	wsClean := filepath.Clean(workspaceRoot)

	for {
		for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
			cfgPath := filepath.Join(curr, name)
			if data, err := os.ReadFile(cfgPath); err == nil {
				cleaned := stripJSONComments(string(data))
				var raw struct {
					Extends         string `json:"extends"`
					CompilerOptions struct {
						BaseURL string              `json:"baseUrl"`
						Paths   map[string][]string `json:"paths"`
					} `json:"compilerOptions"`
				}
				if err := json.Unmarshal([]byte(cleaned), &raw); err == nil {
					baseCfg := &TSConfig{
						BaseURL: raw.CompilerOptions.BaseURL,
						Paths:   raw.CompilerOptions.Paths,
					}
					// Handle relative extends
					if raw.Extends != "" && (strings.HasPrefix(raw.Extends, "./") || strings.HasPrefix(raw.Extends, "../")) {
						parentPath := filepath.Join(curr, raw.Extends)
						if !strings.HasSuffix(parentPath, ".json") {
							parentPath += ".json"
						}
						if parentData, pErr := os.ReadFile(parentPath); pErr == nil {
							var parentRaw struct {
								CompilerOptions struct {
									BaseURL string              `json:"baseUrl"`
									Paths   map[string][]string `json:"paths"`
								} `json:"compilerOptions"`
							}
							if err := json.Unmarshal([]byte(stripJSONComments(string(parentData))), &parentRaw); err == nil {
								if baseCfg.BaseURL == "" {
									baseCfg.BaseURL = parentRaw.CompilerOptions.BaseURL
								}
								if baseCfg.Paths == nil {
									baseCfg.Paths = parentRaw.CompilerOptions.Paths
								} else {
									for k, v := range parentRaw.CompilerOptions.Paths {
										if _, exists := baseCfg.Paths[k]; !exists {
											baseCfg.Paths[k] = v
										}
									}
								}
							}
						}
					}
					return baseCfg
				}
			}
		}

		if curr == wsClean || curr == "." || curr == "/" || curr == filepath.Dir(curr) {
			break
		}
		curr = filepath.Dir(curr)
	}

	return LoadTSConfig(workspaceRoot)
}

type tsImportBinding struct {
	ImportPath     string
	ImportedSymbol string
	LocalAlias     string
	IsDefault      bool
	IsNamespace    bool
	IsTypeOnly     bool
	SourceFile     string
	LineNum        int
}

// FindReferencesBatch executes batch reference analysis for TypeScript/JavaScript symbols.
func (a *TypeScriptAdapter) FindReferencesBatch(
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
		".git":         true,
		".tzro":        true,
		"node_modules": true,
		"dist":         true,
		"build":        true,
		".next":        true,
		".nuxt":        true,
	}

	symMap := make(map[string]Symbol)
	for _, sym := range symbols {
		if sym.Name != "" {
			symMap[sym.Name] = sym
		}
	}

	// 1. Gather all TS/JS files in the workspace
	var codeFiles []string
	var configFiles []string

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
		if a.policy != nil && !a.policy.EvaluatePath(relPath).Allowed {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(relPath))
		switch ext {
		case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
			codeFiles = append(codeFiles, relPath)
		case ".json", ".yaml", ".yml", ".toml":
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

	// 2. Scan all files for computed dynamic require/import & build barrel map
	barrelMap := make(map[string][]string) // targetFile -> []barrelFiles
	for _, codeFile := range codeFiles {
		content, _, err := readFile(codeFile)
		if err != nil {
			continue
		}

		// Extract any computed/unresolved dynamic imports across the workspace
		_, unresolvedList := extractTSBindings(codeFile, []byte(content))
		for _, u := range unresolvedList {
			cov.UnresolvedImports = append(cov.UnresolvedImports, u)
			cov.IncompleteDiscovery = true
		}

		tsconfig := FindNearestTSConfig(workspaceRoot, filepath.Dir(filepath.Join(workspaceRoot, codeFile)))

		// Check for export * from '...' or export { ... } from '...'
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "export ") && strings.Contains(trimmed, " from ") {
				fromIdx := strings.Index(trimmed, " from ")
				rawTarget := strings.Trim(strings.TrimSpace(trimmed[fromIdx+6:]), `;"'`+"`")
				resolved := ResolveImportedFile(workspaceRoot, codeFile, rawTarget, tsconfig)
				for _, cand := range resolved {
					fullCand := cand
					if !filepath.IsAbs(fullCand) {
						fullCand = filepath.Join(workspaceRoot, cand)
					}
					if _, statErr := os.Stat(fullCand); statErr == nil {
						rel := cleanRel(cand)
						barrelMap[rel] = append(barrelMap[rel], codeFile)
					}
				}
			}
		}
	}

	// 3. For each symbol, trace direct consumers and barrel consumers
	for _, sym := range symbols {
		symCopy := sym
		targetDeclFile := sym.FilePath
		symPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym.Name) + `\b`)

		// Find files that import this declaration directly or through barrel files
		validSourceFiles := make(map[string]bool)
		if targetDeclFile != "" {
			cleanTarget := cleanRel(targetDeclFile)
			validSourceFiles[cleanTarget] = true
			// Bounded traversal for barrel files
			visitedBarrels := make(map[string]bool)
			queue := []string{cleanTarget}
			for len(queue) > 0 {
				curr := queue[0]
				queue = queue[1:]
				if visitedBarrels[curr] {
					continue
				}
				visitedBarrels[curr] = true
				validSourceFiles[curr] = true
				for _, b := range barrelMap[curr] {
					cleanB := cleanRel(b)
					if !visitedBarrels[cleanB] {
						queue = append(queue, cleanB)
					}
				}
			}
		}

		for _, codeFile := range codeFiles {
			content, lines, err := readFile(codeFile)
			if err != nil {
				continue
			}

			if !symPattern.MatchString(content) {
				// Check inferred test match by naming convention
				if sym.FilePath != "" && isTSTestFile(codeFile) {
					base := strings.TrimSuffix(filepath.Base(sym.FilePath), filepath.Ext(sym.FilePath))
					if strings.Contains(filepath.Base(codeFile), base) {
						refs = append(refs, RawReference{
							FilePath:     codeFile,
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

			// Parse import bindings in codeFile to check if sym is imported and how
			bindings, _ := extractTSBindings(codeFile, []byte(content))

			isTest := isTSTestFile(codeFile)
			tsconfig := FindNearestTSConfig(workspaceRoot, filepath.Dir(filepath.Join(workspaceRoot, codeFile)))

			// Check if sym is imported in codeFile
			isImported := false
			localAlias := sym.Name
			isTypeOnly := false
			isNamespace := false
			nsName := ""

			for _, b := range bindings {
				// Resolve target of import
				resolvedCands := ResolveImportedFile(workspaceRoot, codeFile, b.ImportPath, tsconfig)
				matchesTarget := false
				if len(validSourceFiles) == 0 {
					matchesTarget = true
				} else {
					for _, cand := range resolvedCands {
						rel := cleanRel(cand)
						if validSourceFiles[rel] {
							matchesTarget = true
							break
						}
					}
				}

				if matchesTarget {
					if b.IsNamespace {
						isNamespace = true
						nsName = b.LocalAlias
						isImported = true
					} else if b.ImportedSymbol == sym.Name || (b.IsDefault && sym.Name == "default") {
						isImported = true
						if b.LocalAlias != "" {
							localAlias = b.LocalAlias
						}
						if b.IsTypeOnly {
							isTypeOnly = true
						}
					}
				}
			}

			// If this file IS the declaration file itself, calls from within the file are callers
			isSameFile := (sym.FilePath != "" && codeFile == sym.FilePath)

			// Scan lines for occurrences of the active local name (or namespace.symbol)
			var activeRe *regexp.Regexp
			if isNamespace && nsName != "" {
				activeRe = regexp.MustCompile(`\b` + regexp.QuoteMeta(nsName) + `\.` + regexp.QuoteMeta(sym.Name) + `\b`)
			} else {
				activeRe = regexp.MustCompile(`\b` + regexp.QuoteMeta(localAlias) + `\b`)
			}

			for i, line := range lines {
				lineNum := i + 1
				trimmed := strings.TrimSpace(line)

				// Skip import statement or re-export statement line itself unless type-only reporting
				isImportDecl := strings.HasPrefix(trimmed, "import ")
				isReexportDecl := strings.HasPrefix(trimmed, "export ") && (strings.Contains(trimmed, " from ") || strings.HasPrefix(trimmed, "export {"))
				if isImportDecl || isReexportDecl {
					if isTypeOnly && strings.Contains(line, sym.Name) {
						start := max(1, lineNum-2)
						end := min(len(lines), lineNum+3)
						refs = append(refs, RawReference{
							FilePath:     codeFile,
							StartLine:    start,
							EndLine:      end,
							Precision:    PrecisionSyntactic,
							Relationship: RelEmbedder,
							Content:      strings.Join(lines[start-1:end], "\n"),
							SymbolName:   sym.Name,
							Score:        75.0,
							SourceSymbol: &symCopy,
						})
					}
					continue
				}

				if activeRe.MatchString(line) {
					// Precision: Syntactic for syntax-matched references
					prec := PrecisionSyntactic
					rel := RelCaller
					score := 80.0

					if isTest {
						rel = RelTest
						score = 70.0
					} else if isTypeOnly {
						rel = RelEmbedder
						score = 65.0
					}

					// If imported or same-file, it's a proven lexical binding
					if !isImported && !isSameFile && targetDeclFile != "" {
						// Shadowed or unimported match with same name
						prec = PrecisionInferred
						score = 45.0
					}

					start := max(1, lineNum-4)
					end := min(len(lines), lineNum+10)
					snippet := strings.Join(lines[start-1:end], "\n")

					refs = append(refs, RawReference{
						FilePath:     codeFile,
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
		}

		// Also check configuration files
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

// extractTSBindings parses import statements and require calls using Tree-sitter or lexical scanner.
func extractTSBindings(filePath string, source []byte) ([]tsImportBinding, []string) {
	var bindings []tsImportBinding
	var unresolved []string

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return extractTSBindingsLexical(filePath, string(source))
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return extractTSBindingsLexical(filePath, string(source))
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return extractTSBindingsLexical(filePath, string(source))
	}

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nodeType := bt.NodeType(node)

		// 1. import statement: import ... from '...'
		if nodeType == "import_statement" {
			srcNode := bt.ChildByField(node, "source")
			if srcNode != nil {
				rawPath := string(source[srcNode.StartByte():srcNode.EndByte()])
				importPath := strings.Trim(rawPath, `"'`+"`")
				lineNum := int(node.StartPoint().Row) + 1

				// Check if 'import type'
				isTypeOnly := strings.HasPrefix(strings.TrimSpace(string(source[node.StartByte():srcNode.StartByte()])), "import type")

				var walkClause func(n *gotreesitter.Node)
				walkClause = func(n *gotreesitter.Node) {
					if n == nil {
						return
					}
					nt := bt.NodeType(n)
					if nt == "import_specifier" {
						nameNode := bt.ChildByField(n, "name")
						aliasNode := bt.ChildByField(n, "alias")
						if nameNode != nil {
							name := string(source[nameNode.StartByte():nameNode.EndByte()])
							alias := name
							if aliasNode != nil {
								alias = string(source[aliasNode.StartByte():aliasNode.EndByte()])
							}
							bindings = append(bindings, tsImportBinding{
								ImportPath:     importPath,
								ImportedSymbol: name,
								LocalAlias:     alias,
								IsTypeOnly:     isTypeOnly,
								SourceFile:     filePath,
								LineNum:        lineNum,
							})
						}
					} else if nt == "namespace_import" {
						for k := 0; k < n.ChildCount(); k++ {
							if bt.NodeType(n.Child(k)) == "identifier" {
								nsName := string(source[n.Child(k).StartByte():n.Child(k).EndByte()])
								bindings = append(bindings, tsImportBinding{
									ImportPath:  importPath,
									LocalAlias:  nsName,
									IsNamespace: true,
									IsTypeOnly:  isTypeOnly,
									SourceFile:  filePath,
									LineNum:     lineNum,
								})
							}
						}
					} else if nt == "import_clause" {
						for k := 0; k < n.ChildCount(); k++ {
							c := n.Child(k)
							if bt.NodeType(c) == "identifier" {
								defName := string(source[c.StartByte():c.EndByte()])
								bindings = append(bindings, tsImportBinding{
									ImportPath:     importPath,
									ImportedSymbol: "default",
									LocalAlias:     defName,
									IsDefault:      true,
									IsTypeOnly:     isTypeOnly,
									SourceFile:     filePath,
									LineNum:        lineNum,
								})
							}
						}
					}
					for k := 0; k < n.ChildCount(); k++ {
						walkClause(n.Child(k))
					}
				}
				walkClause(node)
			}
		}

		// 2. Dynamic import() or require()
		if nodeType == "call_expression" {
			fnNode := bt.ChildByField(node, "function")
			argsNode := bt.ChildByField(node, "arguments")
			if fnNode != nil && argsNode != nil {
				fnName := string(source[fnNode.StartByte():fnNode.EndByte()])
				if fnName == "require" || fnName == "import" {
					if argsNode.ChildCount() >= 2 {
						firstArg := argsNode.Child(1) // ( is child 0
						argText := string(source[firstArg.StartByte():firstArg.EndByte()])
						if strings.HasPrefix(argText, `"`) || strings.HasPrefix(argText, `'`) || strings.HasPrefix(argText, "`") {
							rawPath := strings.Trim(argText, `"'`+"`")
							bindings = append(bindings, tsImportBinding{
								ImportPath: rawPath,
								SourceFile: filePath,
								LineNum:    int(node.StartPoint().Row) + 1,
							})
						} else {
							// Computed require(variable) -> unsupported/unresolved!
							unresolved = append(unresolved, fmt.Sprintf("%s: computed %s(%s)", filePath, fnName, argText))
						}
					}
				}
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)
	return bindings, unresolved
}

// extractTSBindingsLexical is a fallback scanner for TS/JS files.
func extractTSBindingsLexical(filePath, content string) ([]tsImportBinding, []string) {
	var bindings []tsImportBinding
	var unresolved []string
	lines := strings.Split(content, "\n")

	for idx, line := range lines {
		lineNum := idx + 1
		trimmed := strings.TrimSpace(line)

		// import ... from '...'
		if strings.HasPrefix(trimmed, "import ") {
			isTypeOnly := strings.HasPrefix(trimmed, "import type ")
			fromIdx := strings.Index(trimmed, " from ")
			if fromIdx >= 0 {
				pathPart := strings.TrimSpace(trimmed[fromIdx+6:])
				pathPart = strings.Trim(pathPart, `;"'`+"`")
				clause := strings.TrimSpace(trimmed[7:fromIdx])
				if isTypeOnly {
					clause = strings.TrimPrefix(clause, "type ")
				}

				if strings.HasPrefix(clause, "* as ") {
					nsName := strings.TrimSpace(strings.TrimPrefix(clause, "* as "))
					bindings = append(bindings, tsImportBinding{
						ImportPath:  pathPart,
						LocalAlias:  nsName,
						IsNamespace: true,
						IsTypeOnly:  isTypeOnly,
						SourceFile:  filePath,
						LineNum:     lineNum,
					})
				} else if strings.HasPrefix(clause, "{") && strings.HasSuffix(clause, "}") {
					inside := strings.Trim(clause, "{}")
					for _, symPart := range strings.Split(inside, ",") {
						symPart = strings.TrimSpace(symPart)
						if symPart == "" {
							continue
						}
						name := symPart
						alias := symPart
						if strings.Contains(symPart, " as ") {
							p := strings.Split(symPart, " as ")
							name = strings.TrimSpace(p[0])
							alias = strings.TrimSpace(p[1])
						}
						bindings = append(bindings, tsImportBinding{
							ImportPath:     pathPart,
							ImportedSymbol: name,
							LocalAlias:     alias,
							IsTypeOnly:     isTypeOnly,
							SourceFile:     filePath,
							LineNum:        lineNum,
						})
					}
				} else {
					// default import
					bindings = append(bindings, tsImportBinding{
						ImportPath:     pathPart,
						ImportedSymbol: "default",
						LocalAlias:     clause,
						IsDefault:      true,
						IsTypeOnly:     isTypeOnly,
						SourceFile:     filePath,
						LineNum:        lineNum,
					})
				}
			}
		}
	}

	return bindings, unresolved
}
