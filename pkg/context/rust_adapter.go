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

// RustAdapter provides AST- and crate/module-aware reference discovery for Rust.
type RustAdapter struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewRustAdapter creates a new Rust reference adapter.
func NewRustAdapter(s *store.Store, policy *dlp.PolicyEngine) *RustAdapter {
	return &RustAdapter{
		store:  s,
		policy: policy,
	}
}

// FindReferences finds references for a single symbol.
func (a *RustAdapter) FindReferences(workspaceRoot string, sym Symbol, excludes *ignore.GitIgnore, includeGenerated bool) ([]RawReference, error) {
	ctx, cancel := stdctx.WithTimeout(stdctx.Background(), 10*time.Second)
	defer cancel()
	refs, _, err := a.FindReferencesBatch(ctx, workspaceRoot, []Symbol{sym}, excludes, includeGenerated)
	return refs, err
}

// isRustTestFile checks if a file is an integration test under tests/ or named like a test.
func isRustTestFile(relPath string) bool {
	dir := strings.ToLower(filepath.Dir(relPath))
	base := strings.ToLower(filepath.Base(relPath))
	if strings.Contains(dir, "tests") {
		return strings.HasSuffix(base, ".rs")
	}
	return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.rs")
}

// rustImportBinding represents a resolved or declared `use` binding in Rust.
type rustImportBinding struct {
	FullPath       string // e.g. "crate::foo::Bar"
	ImportedSymbol string // "Bar"
	LocalAlias     string // "Bar" or alias
	IsReExport     bool   // pub use
	IsGlob         bool   // use foo::*
	LineNum        int
	SourceFile     string
}

type rustCrateInfo struct {
	Name    string
	Root    string // relative to workspaceRoot, e.g. "crates/core" or "."
	LibFile string // "src/lib.rs"
	BinFile string // "src/main.rs"
}

// FindReferencesBatch executes batch reference analysis for Rust symbols.
func (a *RustAdapter) FindReferencesBatch(
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
		"target":       true,
		"node_modules": true,
		"vendor":       true,
		"dist":         true,
		"build":        true,
	}

	symMap := make(map[string]Symbol)
	for _, sym := range symbols {
		if sym.Name != "" {
			symMap[sym.Name] = sym
		}
	}

	// 1. Discover crates (support workspaces and single crates)
	crates := discoverRustCrates(workspaceRoot)

	// 2. Discover all Rust files and configs
	var rsFiles []string
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
		if a.policy != nil && !a.policy.EvaluatePath(relPath).Allowed {
			return nil
		}

		base := strings.ToLower(d.Name())
		ext := strings.ToLower(filepath.Ext(relPath))

		if ext == ".rs" {
			rsFiles = append(rsFiles, relPath)
		} else if base == "cargo.toml" || base == "cargo.lock" {
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

	cleanRel := func(p string) string {
		if filepath.IsAbs(p) {
			if rel, err := filepath.Rel(workspaceRoot, p); err == nil {
				return filepath.Clean(rel)
			}
		}
		return filepath.Clean(p)
	}

	// 3. Scan files for bindings, cfg limitations, and macros
	fileBindings := make(map[string][]rustImportBinding)
	fileReExports := make(map[string][]rustImportBinding)
	fileInlineTests := make(map[string][]rustLineRange)

	for _, rsFile := range rsFiles {
		content, _, err := readFile(rsFile)
		if err != nil {
			continue
		}

		bindings, inlineTests, limitations := extractRustInfo(rsFile, []byte(content))
		fileBindings[rsFile] = bindings
		fileInlineTests[rsFile] = inlineTests

		for _, lim := range limitations {
			if lim.IsGlobImport {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("glob import in %s: %s", rsFile, lim.Detail))
				cov.IncompleteDiscovery = true
			} else if lim.IsMacro {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("macro usage in %s: %s", rsFile, lim.Detail))
				cov.IncompleteDiscovery = true
			} else if lim.IsConditionalCfg {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("conditional compilation cfg in %s: %s", rsFile, lim.Detail))
			} else if lim.IsTraitAmbiguity {
				cov.UnsupportedSyntax = append(cov.UnsupportedSyntax, fmt.Sprintf("trait method ambiguity in %s: %s", rsFile, lim.Detail))
			}
		}

		for _, b := range bindings {
			if b.IsReExport {
				fileReExports[rsFile] = append(fileReExports[rsFile], b)
			}
		}
	}

	// 4. Resolve references for each target symbol
	for _, sym := range symbols {
		symCopy := sym
		symPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(sym.Name) + `\b`)

		var definingFileRel string
		if sym.FilePath != "" {
			definingFileRel = cleanRel(sym.FilePath)
		}

		// Trace re-exports if definingFileRel is known
		validSourceFiles := make(map[string]bool)
		if definingFileRel != "" {
			validSourceFiles[definingFileRel] = true

			visited := make(map[string]bool)
			var queue []string
			queue = append(queue, definingFileRel)
			visited[definingFileRel] = true

			for len(queue) > 0 {
				curr := queue[0]
				queue = queue[1:]

				for candFile, reExports := range fileReExports {
					if visited[candFile] {
						continue
					}
					for _, rx := range reExports {
						if rx.ImportedSymbol == sym.Name || rx.IsGlob {
							// Resolve Rust import path to file
							resolved := resolveRustImport(workspaceRoot, candFile, rx.FullPath, crates)
							for _, r := range resolved {
								if r == curr {
									validSourceFiles[candFile] = true
									visited[candFile] = true
									queue = append(queue, candFile)
									break
								}
							}
						}
					}
				}
			}
		}

		for _, rsFile := range rsFiles {
			// For defining file itself, check inline tests
			if definingFileRel != "" && rsFile == definingFileRel {
				inlineTestRanges := fileInlineTests[rsFile]
				if len(inlineTestRanges) > 0 {
					content, lines, err := readFile(rsFile)
					if err == nil && symPattern.MatchString(content) {
						for _, tr := range inlineTestRanges {
							for i := tr.start; i <= tr.end && i <= len(lines); i++ {
								line := lines[i-1]
								trimmed := strings.TrimSpace(line)
								if strings.HasPrefix(trimmed, "use ") || strings.HasPrefix(trimmed, "pub use ") {
									continue
								}
								if symPattern.MatchString(line) {
									start := max(1, i-4)
									end := min(len(lines), i+10)
									snippet := strings.Join(lines[start-1:end], "\n")
									refs = append(refs, RawReference{
										FilePath:     rsFile,
										StartLine:    start,
										EndLine:      end,
										Precision:    PrecisionSyntactic,
										Relationship: RelTest,
										Content:      snippet,
										SymbolName:   sym.Name,
										Score:        70.0,
										SourceSymbol: &symCopy,
									})
									break
								}
							}
						}
					}
				}
				continue
			}

			content, lines, err := readFile(rsFile)
			if err != nil {
				continue
			}

			if !symPattern.MatchString(content) {
				// Inferred test match by naming convention
				if definingFileRel != "" && isRustTestFile(rsFile) {
					base := strings.TrimSuffix(filepath.Base(definingFileRel), ".rs")
					symBase := strings.ToLower(sym.Name)
					testBase := strings.ToLower(filepath.Base(rsFile))
					crateName := ""
					if curCrate := findCrateForFile(definingFileRel, crates); curCrate != nil {
						crateName = strings.ToLower(curCrate.Name)
					}
					if strings.Contains(testBase, base) || strings.Contains(testBase, symBase) || (crateName != "" && strings.Contains(testBase, crateName)) {
						refs = append(refs, RawReference{
							FilePath:     rsFile,
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

			// Check bindings in rsFile
			bindings := fileBindings[rsFile]
			inlineTestRanges := fileInlineTests[rsFile]
			isTestFile := isRustTestFile(rsFile)

			isDirectlyUsed := false
			aliasName := sym.Name

			for _, b := range bindings {
				if b.ImportedSymbol == sym.Name || b.IsGlob {
					resolved := resolveRustImport(workspaceRoot, rsFile, b.FullPath, crates)
					matchesTarget := false
					if len(validSourceFiles) == 0 {
						matchesTarget = true
					} else {
						for _, r := range resolved {
							if validSourceFiles[r] {
								matchesTarget = true
								break
							}
						}
					}

					if matchesTarget {
						isDirectlyUsed = true
						if b.LocalAlias != "" {
							aliasName = b.LocalAlias
						}
					}
				}
			}

			// Also match if same crate or module tree
			isSameCrate := false
			if definingFileRel != "" {
				crateA := findCrateForFile(definingFileRel, crates)
				crateB := findCrateForFile(rsFile, crates)
				if crateA != nil && crateB != nil && crateA.Root == crateB.Root {
					isSameCrate = true
				}
			}

			for i, line := range lines {
				lineNum := i + 1
				trimmed := strings.TrimSpace(line)

				// Skip `use` statement itself
				if strings.HasPrefix(trimmed, "use ") || strings.HasPrefix(trimmed, "pub use ") {
					continue
				}

				matchPattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(aliasName) + `\b`)
				if matchPattern.MatchString(line) && (isDirectlyUsed || isSameCrate || len(validSourceFiles) == 0) {
					start := max(1, lineNum-4)
					end := min(len(lines), lineNum+10)
					snippet := strings.Join(lines[start-1:end], "\n")

					rel := RelCaller
					prec := PrecisionSyntactic
					score := 80.0

					// Check if inside inline test module #[cfg(test)] or #[test]
					isInlineTest := false
					for _, tr := range inlineTestRanges {
						if lineNum >= tr.start && lineNum <= tr.end {
							isInlineTest = true
							break
						}
					}

					if isTestFile || isInlineTest {
						rel = RelTest
						score = 70.0
					}

					refs = append(refs, RawReference{
						FilePath:     rsFile,
						StartLine:    start,
						EndLine:      end,
						Precision:    prec,
						Relationship: rel,
						Content:      snippet,
						SymbolName:   sym.Name,
						Score:        score,
						SourceSymbol: &symCopy,
					})
					break
				}
			}
		}

		// Also check Cargo.toml and Cargo.lock configuration files
		crateName := ""
		if definingFileRel != "" {
			if curCrate := findCrateForFile(definingFileRel, crates); curCrate != nil {
				crateName = curCrate.Name
			}
		}
		cratePattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(crateName) + `\b`)

		for _, cfgFile := range configFiles {
			content, lines, err := readFile(cfgFile)
			if err != nil {
				continue
			}
			matchedCfg := symPattern.MatchString(content) || (crateName != "" && cratePattern.MatchString(content))
			if matchedCfg {
				for i, line := range lines {
					if symPattern.MatchString(line) || (crateName != "" && cratePattern.MatchString(line)) {
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

type rustLineRange struct {
	start int
	end   int
}

type rustLimitation struct {
	IsGlobImport     bool
	IsMacro          bool
	IsConditionalCfg bool
	IsTraitAmbiguity bool
	Detail           string
}

// discoverRustCrates reads workspace members from Cargo.toml if present.
func discoverRustCrates(workspaceRoot string) []rustCrateInfo {
	var crates []rustCrateInfo

	cargoTomlPath := filepath.Join(workspaceRoot, "Cargo.toml")
	data, err := os.ReadFile(cargoTomlPath)
	if err == nil {
		content := string(data)
		// Check for [workspace] members = [...]
		if strings.Contains(content, "[workspace]") {
			memberRe := regexp.MustCompile(`(?s)members\s*=\s*\[(.*?)\]`)
			m := memberRe.FindStringSubmatch(content)
			if len(m) > 1 {
				rawMembers := strings.Split(m[1], ",")
				for _, rm := range rawMembers {
					rm = strings.Trim(strings.TrimSpace(rm), `"'`)
					if rm == "" {
						continue
					}
					// Expand globs, e.g. crates/*
					matches, gErr := filepath.Glob(filepath.Join(workspaceRoot, rm))
					if gErr == nil && len(matches) > 0 {
						for _, match := range matches {
							rel, _ := filepath.Rel(workspaceRoot, match)
							cName := filepath.Base(rel)
							crates = append(crates, rustCrateInfo{
								Name:    cName,
								Root:    rel,
								LibFile: filepath.Join(rel, "src", "lib.rs"),
								BinFile: filepath.Join(rel, "src", "main.rs"),
							})
						}
					} else {
						crates = append(crates, rustCrateInfo{
							Name:    filepath.Base(rm),
							Root:    rm,
							LibFile: filepath.Join(rm, "src", "lib.rs"),
							BinFile: filepath.Join(rm, "src", "main.rs"),
						})
					}
				}
			}
		}
	}

	// Always ensure root crate is included if src/lib.rs or src/main.rs exists
	rootLib := filepath.Join(workspaceRoot, "src", "lib.rs")
	rootBin := filepath.Join(workspaceRoot, "src", "main.rs")
	hasRoot := false
	if _, err := os.Stat(rootLib); err == nil {
		hasRoot = true
	} else if _, err := os.Stat(rootBin); err == nil {
		hasRoot = true
	}
	if hasRoot {
		crates = append(crates, rustCrateInfo{
			Name:    filepath.Base(workspaceRoot),
			Root:    ".",
			LibFile: "src/lib.rs",
			BinFile: "src/main.rs",
		})
	}

	return crates
}

func findCrateForFile(filePath string, crates []rustCrateInfo) *rustCrateInfo {
	clean := filepath.Clean(filePath)
	for i := range crates {
		c := &crates[i]
		if c.Root == "." {
			continue
		}
		if strings.HasPrefix(clean, c.Root+string(filepath.Separator)) {
			return c
		}
	}
	// Default to root crate if exists
	for i := range crates {
		if crates[i].Root == "." {
			return &crates[i]
		}
	}
	return nil
}

// resolveRustImport converts a `use` path like "crate::foo::Bar" or "super::Bar" to candidate file paths.
func resolveRustImport(workspaceRoot, currentFile, importPath string, crates []rustCrateInfo) []string {
	var candidates []string
	cleanCurrent := filepath.Clean(currentFile)
	curCrate := findCrateForFile(cleanCurrent, crates)

	crateDir := "."
	if curCrate != nil && curCrate.Root != "." {
		crateDir = curCrate.Root
	}

	srcDir := filepath.Join(crateDir, "src")

	parts := strings.Split(importPath, "::")
	if len(parts) == 0 {
		return candidates
	}

	first := parts[0]
	if first == "crate" {
		// Relative to crate root
		subParts := parts[1:]
		if len(subParts) > 0 {
			relPath := strings.Join(subParts, string(filepath.Separator))
			candidates = append(candidates,
				filepath.Join(srcDir, relPath+".rs"),
				filepath.Join(srcDir, relPath, "mod.rs"),
			)
			if len(subParts) > 1 {
				prefixPath := strings.Join(subParts[:len(subParts)-1], string(filepath.Separator))
				candidates = append(candidates,
					filepath.Join(srcDir, prefixPath+".rs"),
					filepath.Join(srcDir, prefixPath, "mod.rs"),
				)
			}
		}
	} else if first == "super" {
		curDir := filepath.Dir(cleanCurrent)
		parentDir := filepath.Dir(curDir)
		subParts := parts[1:]
		if len(subParts) > 0 {
			relPath := strings.Join(subParts, string(filepath.Separator))
			candidates = append(candidates,
				filepath.Join(parentDir, relPath+".rs"),
				filepath.Join(parentDir, relPath, "mod.rs"),
			)
			if len(subParts) > 1 {
				prefixPath := strings.Join(subParts[:len(subParts)-1], string(filepath.Separator))
				candidates = append(candidates,
					filepath.Join(parentDir, prefixPath+".rs"),
					filepath.Join(parentDir, prefixPath, "mod.rs"),
				)
			}
		}
	} else if first == "self" {
		curDir := filepath.Dir(cleanCurrent)
		subParts := parts[1:]
		if len(subParts) > 0 {
			relPath := strings.Join(subParts, string(filepath.Separator))
			candidates = append(candidates,
				filepath.Join(curDir, relPath+".rs"),
				filepath.Join(curDir, relPath, "mod.rs"),
			)
			if len(subParts) > 1 {
				prefixPath := strings.Join(subParts[:len(subParts)-1], string(filepath.Separator))
				candidates = append(candidates,
					filepath.Join(curDir, prefixPath+".rs"),
					filepath.Join(curDir, prefixPath, "mod.rs"),
				)
			}
		}
	} else {
		// Could be a workspace crate name or module name in current crate
		for _, c := range crates {
			if c.Name == first {
				subParts := parts[1:]
				cSrc := filepath.Join(c.Root, "src")
				if len(subParts) > 0 {
					relPath := strings.Join(subParts, string(filepath.Separator))
					candidates = append(candidates,
						filepath.Join(cSrc, relPath+".rs"),
						filepath.Join(cSrc, relPath, "mod.rs"),
					)
					if len(subParts) > 1 {
						prefixPath := strings.Join(subParts[:len(subParts)-1], string(filepath.Separator))
						candidates = append(candidates,
							filepath.Join(cSrc, prefixPath+".rs"),
							filepath.Join(cSrc, prefixPath, "mod.rs"),
						)
					}
					candidates = append(candidates, c.LibFile, c.BinFile)
				} else {
					candidates = append(candidates, c.LibFile, c.BinFile)
				}
				return candidates
			}
		}

		// Or a module inside current crate src/
		relPath := strings.Join(parts, string(filepath.Separator))
		candidates = append(candidates,
			filepath.Join(srcDir, relPath+".rs"),
			filepath.Join(srcDir, relPath, "mod.rs"),
		)
		if len(parts) > 1 {
			prefixPath := strings.Join(parts[:len(parts)-1], string(filepath.Separator))
			candidates = append(candidates,
				filepath.Join(srcDir, prefixPath+".rs"),
				filepath.Join(srcDir, prefixPath, "mod.rs"),
			)
		}
	}

	return candidates
}

// extractRustInfo parses `use` statements (including grouped uses), inline tests, and limitations.
func extractRustInfo(filePath string, source []byte) ([]rustImportBinding, []rustLineRange, []rustLimitation) {
	var bindings []rustImportBinding
	var inlineTests []rustLineRange
	var limitations []rustLimitation

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return extractRustInfoLexical(filePath, string(source))
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return extractRustInfoLexical(filePath, string(source))
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return extractRustInfoLexical(filePath, string(source))
	}

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nt := bt.NodeType(node)

		// 1. use_declaration
		if nt == "use_declaration" {
			lineNum := int(node.StartPoint().Row) + 1
			rawUse := string(source[node.StartByte():node.EndByte()])
			isPub := strings.HasPrefix(strings.TrimSpace(rawUse), "pub ")

			parsedBindings := parseRustUseClause(rawUse, filePath, lineNum, isPub)
			for _, b := range parsedBindings {
				if b.IsGlob {
					limitations = append(limitations, rustLimitation{
						IsGlobImport: true,
						Detail:       b.FullPath,
					})
				}
				bindings = append(bindings, b)
			}
		} else if nt == "mod_item" {
			// Check if this module is annotated with #[cfg(test)]
			rawMod := string(source[node.StartByte():node.EndByte()])
			if strings.Contains(rawMod, "cfg(test)") {
				startLine := int(node.StartPoint().Row) + 1
				endLine := int(node.EndPoint().Row) + 1
				inlineTests = append(inlineTests, rustLineRange{start: startLine, end: endLine})
			}
		} else if nt == "function_item" {
			// Check if this function has #[test]
			rawFn := string(source[node.StartByte():node.EndByte()])
			if strings.Contains(rawFn, "#[test]") {
				startLine := int(node.StartPoint().Row) + 1
				endLine := int(node.EndPoint().Row) + 1
				inlineTests = append(inlineTests, rustLineRange{start: startLine, end: endLine})
			}
		} else if nt == "attribute_item" {
			rawAttr := string(source[node.StartByte():node.EndByte()])
			if strings.Contains(rawAttr, "cfg(test)") || strings.Contains(rawAttr, "[test]") {
				startLine := int(node.StartPoint().Row) + 1
				inlineTests = append(inlineTests, rustLineRange{start: startLine, end: startLine + 100})
			} else if strings.Contains(rawAttr, "cfg(") {
				limitations = append(limitations, rustLimitation{
					IsConditionalCfg: true,
					Detail:           rawAttr,
				})
			}
		} else if nt == "macro_invocation" || nt == "macro_definition" {
			rawMacro := string(source[node.StartByte():node.EndByte()])
			limitations = append(limitations, rustLimitation{
				IsMacro: true,
				Detail:  strings.TrimSpace(strings.Split(rawMacro, "\n")[0]),
			})
		}

		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if bt.NodeType(child) == "attribute_item" {
				rawAttr := string(source[child.StartByte():child.EndByte()])
				if (strings.Contains(rawAttr, "cfg(test)") || strings.Contains(rawAttr, "[test]")) && i+1 < node.ChildCount() {
					nextChild := node.Child(i + 1)
					inlineTests = append(inlineTests, rustLineRange{
						start: int(nextChild.StartPoint().Row) + 1,
						end:   int(nextChild.EndPoint().Row) + 1,
					})
				}
			}
			walk(child)
		}
	}

	walk(root)
	return bindings, inlineTests, limitations
}

// parseRustUseClause decomposes simple and grouped use statements into individual bindings.
func parseRustUseClause(rawUse, filePath string, lineNum int, isPub bool) []rustImportBinding {
	var bindings []rustImportBinding

	cleaned := strings.TrimSpace(rawUse)
	cleaned = strings.TrimPrefix(cleaned, "pub ")
	cleaned = strings.TrimPrefix(cleaned, "use ")
	cleaned = strings.TrimSuffix(cleaned, ";")
	cleaned = strings.TrimSpace(cleaned)

	if cleaned == "" {
		return bindings
	}

	// Handle grouped uses, e.g. foo::{bar, baz as qux, sub::{a, b}}
	var flatten func(prefix, expr string)
	flatten = func(prefix, expr string) {
		expr = strings.TrimSpace(expr)
		if idx := strings.Index(expr, "::{"); idx != -1 && strings.HasSuffix(expr, "}") {
			basePrefix := expr[:idx]
			if prefix != "" {
				basePrefix = prefix + "::" + basePrefix
			}
			inner := expr[idx+3 : len(expr)-1]
			// Split inner by commas, taking nested braces into account
			items := splitRustGroupItems(inner)
			for _, it := range items {
				flatten(basePrefix, it)
			}
			return
		}

		full := expr
		if prefix != "" {
			full = prefix + "::" + expr
		}

		isGlob := strings.HasSuffix(full, "::*") || full == "*"
		symName := ""
		alias := ""

		if isGlob {
			symName = "*"
			alias = "*"
		} else if strings.Contains(full, " as ") {
			parts := strings.Split(full, " as ")
			full = strings.TrimSpace(parts[0])
			alias = strings.TrimSpace(parts[1])
			lastParts := strings.Split(full, "::")
			symName = lastParts[len(lastParts)-1]
		} else {
			lastParts := strings.Split(full, "::")
			symName = lastParts[len(lastParts)-1]
			alias = symName
		}

		bindings = append(bindings, rustImportBinding{
			FullPath:       full,
			ImportedSymbol: symName,
			LocalAlias:     alias,
			IsReExport:     isPub,
			IsGlob:         isGlob,
			LineNum:        lineNum,
			SourceFile:     filePath,
		})
	}

	flatten("", cleaned)
	return bindings
}

func splitRustGroupItems(s string) []string {
	var items []string
	depth := 0
	start := 0
	for i, ch := range s {
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		} else if ch == ',' && depth == 0 {
			item := strings.TrimSpace(s[start:i])
			if item != "" {
				items = append(items, item)
			}
			start = i + 1
		}
	}
	if start < len(s) {
		item := strings.TrimSpace(s[start:])
		if item != "" {
			items = append(items, item)
		}
	}
	return items
}

// extractRustInfoLexical provides fallback regex-based parsing of Rust files.
func extractRustInfoLexical(filePath, source string) ([]rustImportBinding, []rustLineRange, []rustLimitation) {
	var bindings []rustImportBinding
	var inlineTests []rustLineRange
	var limitations []rustLimitation

	lines := strings.Split(source, "\n")
	useRe := regexp.MustCompile(`^(pub\s+)?use\s+(.*?);`)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		lineNum := i + 1

		if useRe.MatchString(trimmed) {
			isPub := strings.HasPrefix(trimmed, "pub ")
			b := parseRustUseClause(trimmed, filePath, lineNum, isPub)
			for _, item := range b {
				if item.IsGlob {
					limitations = append(limitations, rustLimitation{
						IsGlobImport: true,
						Detail:       item.FullPath,
					})
				}
				bindings = append(bindings, item)
			}
		}

		if strings.Contains(trimmed, "#[cfg(test)]") || strings.Contains(trimmed, "#[test]") {
			inlineTests = append(inlineTests, rustLineRange{start: lineNum, end: min(len(lines), lineNum+30)})
		}
		if strings.Contains(trimmed, "#[cfg(") && !strings.Contains(trimmed, "cfg(test)") {
			limitations = append(limitations, rustLimitation{
				IsConditionalCfg: true,
				Detail:           trimmed,
			})
		}
		if strings.HasPrefix(trimmed, "macro_rules!") {
			limitations = append(limitations, rustLimitation{
				IsMacro: true,
				Detail:  trimmed,
			})
		}
	}

	return bindings, inlineTests, limitations
}
