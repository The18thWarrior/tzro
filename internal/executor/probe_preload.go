package executor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"tzro/internal/tools"
)

// defaultPreloadMaxChars is the default character budget for pre-loaded directory content.
// 32K chars ≈ ~8K tokens — fits comfortably in the router model's 16K context window
// alongside the system prompt (~2K tokens) and per-step prompts (~1K tokens).
const defaultPreloadMaxChars = 32000

// collectPreloadFiles walks the PreloadPaths and returns a sorted list of all
// readable file paths (same filter as preloadDirectoryContext). Used to
// initialize the Exploration Queue (ADR-0058).
func collectPreloadFiles(paths []string) []string {
	var allFiles []string
	for _, dir := range paths {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			name := filepath.Base(path)
			if strings.HasSuffix(name, "_test.go") {
				return nil
			}
			switch ext {
			case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java", ".md", ".txt", ".rst":
				allFiles = append(allFiles, path)
			}
			return nil
		})
	}
	sort.Strings(allFiles)
	return allFiles
}

// preloadDirectoryContext walks one or more directories and concatenates their
// readable files into a single context string. This provides probes with complete
// source material before the Thought Chain loop, eliminating the routing problem
// where probes miss files during exploration.
//
// File handling by extension:
//   - .go (non-test): AST-extracted exported symbols (types, funcs, methods, vars)
//   - .md, .txt: raw content with file header
//   - Other extensions: skipped
//
// Test files (*_test.go) are always excluded.
// A character budget (maxChars) prevents context window overflow.
func preloadDirectoryContext(paths []string, maxChars int) string {
	if len(paths) == 0 {
		return ""
	}

	var sections []string
	totalChars := 0

	for _, dir := range paths {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}

		// Collect files sorted for deterministic output
		var files []string
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			name := filepath.Base(path)

			// Skip test files
			if strings.HasSuffix(name, "_test.go") {
				return nil
			}

			// Only process supported extensions
			switch ext {
			case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java",
				".md", ".txt", ".rst":
				files = append(files, path)
			}
			return nil
		})

		sort.Strings(files)

		for _, path := range files {
			if totalChars >= maxChars {
				break
			}

			ext := strings.ToLower(filepath.Ext(path))
			relPath, _ := filepath.Rel(dir, path)
			if relPath == "" {
				relPath = filepath.Base(path)
			}

			var section string
			switch ext {
			case ".go":
				// Strategy: prefer full raw content (prevents hallucination from symbol-only context).
				// Fall back to AST-extracted symbols only when raw content would exceed 60% of
				// remaining budget — this means small packages get full source, large packages
				// get AST summaries.
				content, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				rawSection := fmt.Sprintf("### %s\n```go\n%s\n```", relPath, string(content))
				remainingBudget := maxChars - totalChars
				if len(rawSection) <= remainingBudget*60/100 {
					section = rawSection
				} else {
					// AST fallback for large files
					section = extractGoFileSymbols(path, relPath)
				}
			case ".md", ".txt":
				content, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				section = fmt.Sprintf("### %s\n%s", relPath, string(content))
			}

			if section == "" {
				continue
			}

			// Budget check: truncate if adding this section would exceed budget
			if totalChars+len(section) > maxChars {
				remaining := maxChars - totalChars
				if remaining > 100 { // Only add if we can fit something meaningful
					section = section[:remaining] + "\n... (truncated due to context budget)"
				} else {
					break
				}
			}

			sections = append(sections, section)
			totalChars += len(section)
		}
	}

	if len(sections) == 0 {
		return ""
	}

	return strings.Join(sections, "\n\n---\n\n")
}

// extractGoFileSymbols parses a Go file and extracts exported symbols
// (types, functions, methods, variables, constants, interfaces) with their
// full signatures. Returns a formatted section string.
func extractGoFileSymbols(absPath, relPath string) string {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, absPath, nil, parser.ParseComments)
	if err != nil {
		// Fallback: return raw content for unparseable Go files
		content, readErr := os.ReadFile(absPath)
		if readErr != nil {
			return ""
		}
		return fmt.Sprintf("### %s\n```go\n%s\n```", relPath, string(content))
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("### %s (package %s)", relPath, node.Name.Name))

	for _, decl := range node.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if !s.Name.IsExported() {
						continue
					}
					switch t := s.Type.(type) {
					case *ast.InterfaceType:
						lines = append(lines, fmt.Sprintf("- interface **%s**", s.Name.Name))
						if t.Methods != nil {
							for _, m := range t.Methods.List {
								if len(m.Names) > 0 {
									lines = append(lines, fmt.Sprintf("  - %s", m.Names[0].Name))
								}
							}
						}
					case *ast.StructType:
						lines = append(lines, fmt.Sprintf("- type **%s** struct", s.Name.Name))
					default:
						lines = append(lines, fmt.Sprintf("- type **%s**", s.Name.Name))
					}
				case *ast.ValueSpec:
					for _, name := range s.Names {
						if name.IsExported() {
							kind := "var"
							if d.Tok == token.CONST {
								kind = "const"
							}
							lines = append(lines, fmt.Sprintf("- %s **%s**", kind, name.Name))
						}
					}
				}
			}
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}
			sig := formatFuncSignature(d)
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvType := formatReceiverType(d.Recv.List[0].Type)
				lines = append(lines, fmt.Sprintf("- func (%s) **%s**%s", recvType, d.Name.Name, sig))
			} else {
				lines = append(lines, fmt.Sprintf("- func **%s**%s", d.Name.Name, sig))
			}
		}
	}

	if len(lines) <= 1 { // Only header, no symbols
		return ""
	}

	return strings.Join(lines, "\n")
}

// formatFuncSignature extracts the parameter and return type signature from a FuncDecl.
func formatFuncSignature(fn *ast.FuncDecl) string {
	var params []string
	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			typeStr := formatExpr(p.Type)
			if len(p.Names) > 0 {
				for _, name := range p.Names {
					params = append(params, fmt.Sprintf("%s %s", name.Name, typeStr))
				}
			} else {
				params = append(params, typeStr)
			}
		}
	}

	var returns []string
	if fn.Type.Results != nil {
		for _, r := range fn.Type.Results.List {
			typeStr := formatExpr(r.Type)
			if len(r.Names) > 0 {
				for _, name := range r.Names {
					returns = append(returns, fmt.Sprintf("%s %s", name.Name, typeStr))
				}
			} else {
				returns = append(returns, typeStr)
			}
		}
	}

	sig := "(" + strings.Join(params, ", ") + ")"
	if len(returns) == 1 {
		sig += " " + returns[0]
	} else if len(returns) > 1 {
		sig += " (" + strings.Join(returns, ", ") + ")"
	}
	return sig
}

// formatReceiverType formats the receiver type of a method.
func formatReceiverType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.StarExpr:
		return "*" + formatExpr(t.X)
	case *ast.Ident:
		return t.Name
	default:
		return formatExpr(expr)
	}
}

// formatExpr converts an AST expression to a string representation.
func formatExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + formatExpr(t.X)
	case *ast.SelectorExpr:
		return formatExpr(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + formatExpr(t.Elt)
	case *ast.MapType:
		return "map[" + formatExpr(t.Key) + "]" + formatExpr(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.Ellipsis:
		return "..." + formatExpr(t.Elt)
	case *ast.FuncType:
		return "func(...)"
	case *ast.ChanType:
		return "chan " + formatExpr(t.Value)
	default:
		return "?"
	}
}

// webOnlyTools lists tools that operate exclusively over the network.
// Probes restricted to these tools should NOT auto-detect local PreloadPaths,
// because injecting local directory content contaminates web research synthesis
// (observed: technical_deep_dive_gguf 4.75→1.00 in benchmark run 14).
var webOnlyTools = map[string]bool{
	"web_search": true,
	"web_browse": true,
}

// isWebOnlyProbe returns true when every tool in allowedTools is a web-only tool.
// When true, the probe should skip local PreloadPaths auto-detection to avoid
// contaminating web research context with irrelevant local files.
func isWebOnlyProbe(allowedTools []string) bool {
	if len(allowedTools) == 0 {
		return false
	}
	for _, t := range allowedTools {
		if !webOnlyTools[t] {
			return false
		}
	}
	return true
}

// isCacheEquippedProbe returns true if the probe has cache exploration tools
// (introspect_cache, sql_cached_data) in its allowedTools. These probes are
// Analyze Nodes that get data through the cache bridge — preloading directory
// content produces empty/irrelevant context (e.g., CSV directories yield 0
// chars from preloadDirectoryContext which only reads code/doc files).
func isCacheEquippedProbe(allowedTools []string) bool {
	for _, t := range allowedTools {
		if t == "introspect_cache" || t == "sql_cached_data" {
			return true
		}
	}
	return false
}

// pathPattern matches directory-like paths in text (e.g., "internal/cache/", "docs/adr/", "internal/inference/").
// Requires at least one slash and a word character, optionally ending with a trailing slash.
var pathPattern = regexp.MustCompile(`(?:^|\s|['"(])([a-zA-Z][a-zA-Z0-9_\-./]*/)`)

// detectPreloadPaths scans probe goal and context text for directory-like paths,
// resolves them against the project root, and returns paths to existing directories.
// This auto-detection enables universal pre-loading without requiring the planner
// to explicitly set PreloadPaths.
func detectPreloadPaths(goal, taskContext string) []string {
	allowedPaths := tools.GetAllowedPaths()
	if len(allowedPaths) == 0 {
		return nil
	}
	projectRoot := allowedPaths[0]

	// Scan both goal and taskContext for paths
	combined := goal + "\n" + taskContext
	matches := pathPattern.FindAllStringSubmatch(combined, -1)

	seen := make(map[string]bool)
	var result []string

	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		candidate := strings.TrimRight(match[1], "/")

		// Skip common false positives
		if candidate == "" || candidate == "." || candidate == ".." {
			continue
		}
		// Skip URLs
		if strings.Contains(candidate, "://") || strings.HasPrefix(candidate, "http") {
			continue
		}
		// Skip benchmark/test output directories
		if strings.Contains(candidate, "benchmarks") || strings.Contains(candidate, "test_output") ||
			strings.Contains(candidate, "output") {
			continue
		}

		// Resolve against project root
		absPath := filepath.Join(projectRoot, candidate)

		// Validate it's an existing directory
		info, err := os.Stat(absPath)
		if err != nil || !info.IsDir() {
			continue
		}

		if !seen[absPath] {
			seen[absPath] = true
			result = append(result, absPath)
		}
	}

	if len(result) > 0 {
		fmt.Fprintf(os.Stderr, "[Probe] Auto-detected PreloadPaths from goal: %v\n", result)
	}

	return result
}
