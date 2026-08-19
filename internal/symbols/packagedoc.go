package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	"tzro/internal/config"
	"tzro/internal/embeddings"
)

// PackageDocSummary represents generic, AST-extracted package/module information
// across all supported languages (Go, TypeScript/JavaScript, Python, Rust, Java).
type PackageDocSummary struct {
	RelPath      string          `json:"relPath"`
	PackageName  string          `json:"packageName"`
	Language     string          `json:"language"`
	DocSummary   string          `json:"docSummary"`
	SourceFiles  []string        `json:"sourceFiles"`
	KeySymbols   []Symbol        `json:"keySymbols"`
	Dependencies map[string]bool `json:"dependencies"`
}

// EntrypointSummary represents an extracted executable entrypoint, root command,
// or CLI subcommand definition across supported language ecosystems.
type EntrypointSummary struct {
	RelPath     string   `json:"relPath"`
	Name        string   `json:"name"`
	Language    string   `json:"language"`
	Command     string   `json:"command"`
	Description string   `json:"description"`
	Subcommands []string `json:"subcommands,omitempty"`
}

// ExtractPackageSummaries walks any directory tree and dynamically extracts
// module/package names, module-level docstrings, source files, dependencies,
// and exported AST symbols across Go, Python, TypeScript/JavaScript, Rust, and Java.
func ExtractPackageSummaries(rootDir string) ([]PackageDocSummary, error) {
	var summaries []PackageDocSummary

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			return nil
		}

		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" || name == "__pycache__" || name == "target" || name == "dist" || name == "build" {
			return filepath.SkipDir
		}

		entries, readErr := os.ReadDir(path)
		if readErr != nil {
			return nil
		}

		var sourceFiles []string
		var docSummary string
		var pkgName string
		var detectedLang string
		deps := make(map[string]bool)
		var pkgSymbols []Symbol

		// First, check for folder-level readme/overview (e.g. README.md, doc.go, __init__.py, mod.rs)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if strings.EqualFold(fname, "readme.md") || strings.EqualFold(fname, "doc.go") {
				content, err := os.ReadFile(filepath.Join(path, fname))
				if err == nil {
					preview := extractDocPreview(fname, string(content))
					if preview.Title != "" && len(preview.Preview) > 0 {
						docSummary = fmt.Sprintf("%s: %s", preview.Title, strings.Join(preview.Preview, " "))
					}
				}
			}
		}

		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			fname := entry.Name()
			if strings.HasPrefix(fname, ".") || isTestFile(fname) {
				continue
			}

			ext := strings.ToLower(filepath.Ext(fname))
			if !isCodeFile(ext) {
				continue
			}

			sourceFiles = append(sourceFiles, fname)
			fullPath := filepath.Join(path, fname)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}

			// Detect language from grammar registry
			lang := detectSourceLanguage(fname)
			if lang != "" && detectedLang == "" {
				detectedLang = lang
			}

			// Extract module/package documentation if not already extracted
			if docSummary == "" {
				docSummary = extractModuleDoc(fname, content, lang)
			}
			if pkgName == "" {
				pkgName = extractModuleName(fname, content, lang, filepath.Base(path))
			}

			// Extract exported symbols using tree-sitter AST
			syms, symErr := ExtractSymbols(fname, content)
			if symErr == nil && len(syms) > 0 {
				for _, s := range syms {
					if s.Exported {
						s.File = filepath.Join(path, fname)
						pkgSymbols = append(pkgSymbols, s)
					}
				}
			}

			// Extract imported dependencies
			for _, imp := range extractImports(content, lang) {
				deps[imp] = true
			}
		}

		if len(sourceFiles) > 0 {
			rel, _ := filepath.Rel(rootDir, path)
			if rel == "." {
				rel = filepath.Base(path)
			}
			if pkgName == "" {
				pkgName = filepath.Base(path)
			}
			if detectedLang == "" {
				detectedLang = "code"
			}
			if docSummary == "" {
				docSummary = fmt.Sprintf("Module `%s` contains %d %s source files.", pkgName, len(sourceFiles), detectedLang)
			}

			summaries = append(summaries, PackageDocSummary{
				RelPath:      rel,
				PackageName:  pkgName,
				Language:     detectedLang,
				DocSummary:   docSummary,
				SourceFiles:  sourceFiles,
				KeySymbols:   pkgSymbols,
				Dependencies: deps,
			})
		}

		return nil
	})

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].RelPath < summaries[j].RelPath
	})

	return summaries, err
}

// ExtractEntrypoints scans configured or candidate entrypoint directories (cmd, cli, commands, bin, src/cli, etc.)
// and uses Neural Semantic Similarity to discover custom candidate directories up to depth 3.
func ExtractEntrypoints(rootDir string) []EntrypointSummary {
	var entrypoints []EntrypointSummary
	candidateDirs := config.GlobalConfig.GetEntrypointDirectories()
	scannedSet := make(map[string]bool)

	for _, relDir := range candidateDirs {
		scannedSet[filepath.Clean(relDir)] = true
	}

	// Dynamic Semantic Discovery: Walk up to depth 3 to find custom entrypoint/CLI directories
	// via Neural Embedding Vector Space (ADR-0081, SOLUTION_APPROACH.md Principle 1)
	entrypointPrototypes := []string{
		"CLI command line interface executable application subcommands flags parser",
		"main application entrypoint binary runner daemon interface",
		"terminal commands handlers dispatch scripts tools cli",
	}

	_ = filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(rootDir, path)
		if rel == "." {
			return nil
		}
		rel = filepath.Clean(rel)

		// Depth limit: max 3 levels deep
		depth := strings.Count(rel, string(filepath.Separator)) + 1
		if depth > 3 {
			return filepath.SkipDir
		}

		name := info.Name()
		if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "testdata" || name == "test" || name == "tests" || name == "__pycache__" || name == "target" || name == "dist" || name == "build" {
			return filepath.SkipDir
		}

		if !scannedSet[rel] {
			// Compute semantic similarity of directory name/path against entrypoint prototypes
			var maxSim float64 = 0.0
			for _, proto := range entrypointPrototypes {
				sim := embeddings.CosineSimilarity(rel+" "+name, proto)
				if sim > maxSim {
					maxSim = sim
				}
			}

			// If semantically close (threshold >= 0.40) or matches known CLI tokens, include directory
			if maxSim >= 0.40 {
				candidateDirs = append(candidateDirs, rel)
				scannedSet[rel] = true
			}
		}

		return nil
	})

	for _, relDir := range candidateDirs {
		targetPath := filepath.Join(rootDir, relDir)
		info, err := os.Stat(targetPath)
		if err != nil || !info.IsDir() {
			continue
		}

		// Inspect files and immediate subdirectories
		_ = filepath.Walk(targetPath, func(path string, fInfo os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			name := fInfo.Name()
			if strings.HasPrefix(name, ".") || isTestFile(name) {
				if fInfo.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if !fInfo.IsDir() && isCodeFile(strings.ToLower(filepath.Ext(name))) {
				content, err := os.ReadFile(path)
				if err != nil {
					return nil
				}
				rel, _ := filepath.Rel(rootDir, path)
				lang := detectSourceLanguage(name)

				// Look for main functions, cobra commands, click/argparse commands
				extractedCmds := extractCLICommands(name, content, lang)
				for _, cmd := range extractedCmds {
					cmd.RelPath = rel
					entrypoints = append(entrypoints, cmd)
				}
			}
			return nil
		})
	}

	return entrypoints
}

// AssembleArchitectureMap builds a clean, high-level architecture document
// using a consolidated Package-Level Dependency Graph (instead of a raw 100k+ char function call dump).
func AssembleArchitectureMap(rootDir string, summaries []PackageDocSummary, callGraphSymbols []CallGraphSymbol, callEdges []CallEdge) string {
	var b strings.Builder

	b.WriteString("# System Architecture Overview\n\n")
	b.WriteString("## Core Subsystems & Module Map\n\n")

	for _, s := range summaries {
		b.WriteString(fmt.Sprintf("### `%s` (`%s`)\n", s.RelPath, s.PackageName))
		b.WriteString(fmt.Sprintf("%s\n\n", s.DocSummary))
		b.WriteString(fmt.Sprintf("- **Language**: %s\n", s.Language))
		b.WriteString(fmt.Sprintf("- **Source Files (%d)**: %s\n", len(s.SourceFiles), strings.Join(s.SourceFiles, ", ")))
		if len(s.KeySymbols) > 0 {
			maxSyms := 8
			if len(s.KeySymbols) < maxSyms {
				maxSyms = len(s.KeySymbols)
			}
			var symNames []string
			for _, sym := range s.KeySymbols[:maxSyms] {
				symNames = append(symNames, fmt.Sprintf("`%s` (%s)", sym.Name, sym.Kind))
			}
			b.WriteString(fmt.Sprintf("- **Key Exported Symbols**: %s\n", strings.Join(symNames, ", ")))
		}
		b.WriteString("\n")
	}

	b.WriteString("## Package Dependency & Data Flow Graph\n\n")
	pkgEdges := buildPackageDependencyEdges(summaries, callEdges)
	if len(pkgEdges) > 0 {
		for _, e := range pkgEdges {
			b.WriteString(fmt.Sprintf("- `%s` → `%s`\n", e.From, e.To))
		}
	} else {
		b.WriteString("(no cross-module dependencies detected)\n")
	}

	return b.String()
}

// AssembleProjectReadmeMap builds a comprehensive project overview incorporating
// discovered CLI entrypoints, package maps, public API signatures, and ADRs.
func AssembleProjectReadmeMap(rootDir string, summaries []PackageDocSummary, entrypoints []EntrypointSummary, adrsCombined string) string {
	var b strings.Builder

	b.WriteString("# Project Overview & Architecture\n\n")

	if len(entrypoints) > 0 {
		b.WriteString("## CLI Commands & Entrypoints\n\n")
		for _, ep := range entrypoints {
			b.WriteString(fmt.Sprintf("### `%s` (`%s`)\n", ep.Command, ep.RelPath))
			if ep.Description != "" {
				b.WriteString(fmt.Sprintf("%s\n\n", ep.Description))
			}
			if len(ep.Subcommands) > 0 {
				b.WriteString(fmt.Sprintf("- **Available Subcommands**: %s\n\n", strings.Join(ep.Subcommands, ", ")))
			}
		}
	}

	b.WriteString("## Discovered Modules & Packages\n\n")
	for _, s := range summaries {
		b.WriteString(fmt.Sprintf("- **`%s`** (%s): %s\n", s.RelPath, s.Language, s.DocSummary))
	}

	b.WriteString("\n## Key Public APIs & Exported Symbols\n\n")
	for _, s := range summaries {
		if len(s.KeySymbols) == 0 {
			continue
		}
		b.WriteString(fmt.Sprintf("### Module `%s`\n\n", s.RelPath))
		maxSyms := 10
		if len(s.KeySymbols) < maxSyms {
			maxSyms = len(s.KeySymbols)
		}
		for _, sym := range s.KeySymbols[:maxSyms] {
			sig := sym.Signature
			if sig == "" {
				sig = fmt.Sprintf("%s %s", sym.Kind, sym.Name)
			}
			doc := sym.DocComment
			if doc != "" {
				b.WriteString(fmt.Sprintf("- `%s`: %s\n", sig, doc))
			} else {
				b.WriteString(fmt.Sprintf("- `%s`\n", sig))
			}
		}
		b.WriteString("\n")
	}

	if adrsCombined != "" {
		b.WriteString("## Architectural Decisions & ADR Log\n\n")
		b.WriteString(adrsCombined)
	}

	return b.String()
}

type packageEdge struct {
	From string
	To   string
}

func buildPackageDependencyEdges(summaries []PackageDocSummary, callEdges []CallEdge) []packageEdge {
	edgeSet := make(map[string]bool)
	var result []packageEdge

	// 1. Resolve import-based dependencies
	for _, s := range summaries {
		for dep := range s.Dependencies {
			for _, other := range summaries {
				if other.RelPath == s.RelPath {
					continue
				}
				if strings.HasSuffix(dep, other.RelPath) || strings.HasSuffix(dep, other.PackageName) {
					key := fmt.Sprintf("%s|%s", s.RelPath, other.RelPath)
					if !edgeSet[key] {
						edgeSet[key] = true
						result = append(result, packageEdge{From: s.RelPath, To: other.RelPath})
					}
				}
			}
		}
	}

	// 2. Resolve call-graph cross-file caller/callee package dependencies
	for _, ce := range callEdges {
		fromPkg := filepath.Dir(ce.CallerFile)
		toPkg := filepath.Dir(ce.CalleeFile)
		if fromPkg != "" && toPkg != "" && fromPkg != toPkg && fromPkg != "." && toPkg != "." {
			key := fmt.Sprintf("%s|%s", fromPkg, toPkg)
			if !edgeSet[key] {
				edgeSet[key] = true
				result = append(result, packageEdge{From: fromPkg, To: toPkg})
			}
		}
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].From == result[j].From {
			return result[i].To < result[j].To
		}
		return result[i].From < result[j].From
	})

	if len(result) > 25 {
		result = result[:25]
	}

	return result
}

func extractCLICommands(filename string, source []byte, lang string) []EntrypointSummary {
	var cmds []EntrypointSummary
	str := string(source)

	// Go Cobra / CLI Commands
	if lang == "go" {
		if strings.Contains(str, "cobra.Command") {
			lines := strings.Split(str, "\n")
			var currentCmd EntrypointSummary
			for _, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.Contains(trimmed, "&cobra.Command") {
					if currentCmd.Command != "" {
						cmds = append(cmds, currentCmd)
					}
					currentCmd = EntrypointSummary{
						Language: "go",
					}
				}
				if strings.HasPrefix(trimmed, "Use:") {
					currentCmd.Command = strings.Trim(strings.TrimPrefix(trimmed, "Use:"), `", `)
				}
				if strings.HasPrefix(trimmed, "Short:") {
					currentCmd.Description = strings.Trim(strings.TrimPrefix(trimmed, "Short:"), `", `)
				}
			}
			if currentCmd.Command != "" {
				cmds = append(cmds, currentCmd)
			}
		} else if strings.HasSuffix(filename, "main.go") {
			cmds = append(cmds, EntrypointSummary{
				Command:     filepath.Base(filepath.Dir(filename)),
				Language:    "go",
				Description: fmt.Sprintf("Executable main binary entrypoint in %s", filename),
			})
		}
	}

	// Python Click / Argparse / main
	if lang == "python" {
		if strings.Contains(str, "if __name__ == '__main__':") || strings.Contains(str, `if __name__ == "__main__":`) {
			cmds = append(cmds, EntrypointSummary{
				Command:     strings.TrimSuffix(filename, ".py"),
				Language:    "python",
				Description: fmt.Sprintf("Executable script entrypoint: %s", filename),
			})
		}
	}

	// Rust main
	if lang == "rust" && (filename == "main.rs" || strings.HasPrefix(filename, "cli")) {
		cmds = append(cmds, EntrypointSummary{
			Command:     "cargo run",
			Language:    "rust",
			Description: fmt.Sprintf("Binary executable entrypoint: %s", filename),
		})
	}

	return cmds
}

func detectSourceLanguage(filename string) string {
	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry != nil {
		return strings.ToLower(entry.Name)
	}
	return ""
}

func isTestFile(fname string) bool {
	lower := strings.ToLower(fname)
	return strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, ".spec.ts") ||
		strings.HasSuffix(lower, ".test.js") ||
		strings.HasSuffix(lower, ".spec.js") ||
		strings.HasPrefix(lower, "test_") ||
		strings.HasSuffix(lower, "_test.py")
}

func extractModuleName(filename string, source []byte, lang, fallbackDir string) string {
	switch lang {
	case "go":
		entry := grammars.DetectLanguage(filepath.Base(filename))
		if entry == nil || entry.Language() == nil {
			return fallbackDir
		}
		parser := gotreesitter.NewParser(entry.Language())
		tree, err := parser.Parse(source)
		if err != nil {
			return fallbackDir
		}
		defer tree.Release()
		bt := gotreesitter.Bind(tree)
		root := tree.RootNode()

		var pkgName string
		walkChildren(bt, root, func(node *gotreesitter.Node) {
			if bt.NodeType(node) == "package_clause" {
				nameNode := bt.ChildByField(node, "name")
				if nameNode != nil {
					pkgName = bt.NodeText(nameNode)
				}
			}
		})
		if pkgName != "" {
			return pkgName
		}
	}
	return fallbackDir
}

func extractModuleDoc(filename string, source []byte, lang string) string {
	lines := strings.Split(string(source), "\n")
	if len(lines) == 0 {
		return ""
	}

	// Python docstrings (top of module)
	if lang == "python" {
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, `"""`) || strings.HasPrefix(trimmed, `'''`) {
				doc := strings.Trim(trimmed, `"'`)
				if doc != "" {
					return doc
				}
				if i+1 < len(lines) {
					return strings.TrimSpace(lines[i+1])
				}
			}
		}
	}

	// Rust module-level doc comment (//! ...)
	if lang == "rust" {
		var docLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//!") {
				docLines = append(docLines, strings.TrimSpace(strings.TrimPrefix(trimmed, "//!")))
			}
		}
		if len(docLines) > 0 {
			return strings.Join(docLines, " ")
		}
	}

	// Go package doc comment
	if lang == "go" {
		entry := grammars.DetectLanguage("file.go")
		if entry != nil && entry.Language() != nil {
			parser := gotreesitter.NewParser(entry.Language())
			tree, err := parser.Parse(source)
			if err == nil {
				defer tree.Release()
				bt := gotreesitter.Bind(tree)
				root := tree.RootNode()
				var doc string
				walkChildren(bt, root, func(node *gotreesitter.Node) {
					if bt.NodeType(node) == "package_clause" {
						doc = extractDocComment(bt, node, source)
					}
				})
				if doc != "" {
					return doc
				}
			}
		}
	}

	return ""
}

func extractImports(source []byte, lang string) []string {
	var imports []string
	lines := strings.Split(string(source), "\n")

	switch lang {
	case "go":
		inImport := false
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import (") {
				inImport = true
				continue
			}
			if inImport {
				if strings.HasPrefix(trimmed, ")") {
					inImport = false
					break
				}
				path := strings.Trim(trimmed, `"`)
				if path != "" {
					imports = append(imports, path)
				}
			} else if strings.HasPrefix(trimmed, "import ") {
				path := strings.Trim(strings.TrimPrefix(trimmed, "import "), `"`)
				if path != "" {
					imports = append(imports, path)
				}
			}
		}

	case "python":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import ") {
				imports = append(imports, strings.TrimPrefix(trimmed, "import "))
			} else if strings.HasPrefix(trimmed, "from ") {
				parts := strings.Split(trimmed, " import ")
				if len(parts) > 0 {
					imports = append(imports, strings.TrimPrefix(parts[0], "from "))
				}
			}
		}

	case "typescript", "javascript":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import ") && strings.Contains(trimmed, "from ") {
				parts := strings.Split(trimmed, "from ")
				if len(parts) > 1 {
					imports = append(imports, strings.Trim(strings.Trim(parts[1], ";"), `"'`))
				}
			}
		}

	case "rust":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "use ") {
				imports = append(imports, strings.Trim(strings.TrimPrefix(trimmed, "use "), ";"))
			}
		}

	case "java":
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "import ") {
				imports = append(imports, strings.Trim(strings.TrimPrefix(trimmed, "import "), ";"))
			}
		}
	}

	return imports
}
