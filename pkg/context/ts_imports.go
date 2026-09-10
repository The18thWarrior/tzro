package context

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// ModuleImport represents an imported module or symbol reference.
type ModuleImport struct {
	ImportPath      string   // e.g. "./auth/jwt" or "express"
	ImportedSymbols []string // e.g. ["ValidateToken", "TokenPayload"]
	IsDefault       bool
	SourceFilePath  string
}

// ExtractImports parses a TypeScript or JavaScript source file and extracts imported modules and symbols.
func ExtractImports(filePath string, source []byte) ([]ModuleImport, error) {
	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		// Fallback simple regex / lexical scanner for TS/JS files if grammar not matched
		return extractImportsLexical(filePath, string(source)), nil
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return extractImportsLexical(filePath, string(source)), nil
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return extractImportsLexical(filePath, string(source)), nil
	}

	var imports []ModuleImport

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nodeType := bt.NodeType(node)

		// ES6 import_statement
		if nodeType == "import_statement" {
			imp := ModuleImport{SourceFilePath: filePath}
			sourceNode := bt.ChildByField(node, "source")
			if sourceNode != nil {
				rawPath := string(source[sourceNode.StartByte():sourceNode.EndByte()])
				imp.ImportPath = strings.Trim(rawPath, `"'`+"`")
			}

			// Look for import specifiers inside clause
			for i := 0; i < node.ChildCount(); i++ {
				child := node.Child(i)
				childType := bt.NodeType(child)
				if childType == "import_clause" || childType == "named_imports" {
					for j := 0; j < child.ChildCount(); j++ {
						spec := child.Child(j)
						if bt.NodeType(spec) == "import_specifier" {
							nameNode := bt.ChildByField(spec, "name")
							if nameNode != nil {
								sym := string(source[nameNode.StartByte():nameNode.EndByte()])
								imp.ImportedSymbols = append(imp.ImportedSymbols, sym)
							}
						}
					}
				} else if childType == "identifier" {
					imp.ImportedSymbols = append(imp.ImportedSymbols, string(source[child.StartByte():child.EndByte()]))
					imp.IsDefault = true
				}
			}

			if imp.ImportPath != "" {
				imports = append(imports, imp)
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)

	if len(imports) == 0 {
		return extractImportsLexical(filePath, string(source)), nil
	}

	return imports, nil
}

// extractImportsLexical is a robust scanner fallback for common TS/JS import statements and require() calls.
func extractImportsLexical(filePath, content string) []ModuleImport {
	var imports []ModuleImport
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// ES6: import ... from '...'
		if strings.HasPrefix(trimmed, "import ") {
			fromIdx := strings.Index(trimmed, " from ")
			if fromIdx >= 0 {
				pathPart := strings.TrimSpace(trimmed[fromIdx+6:])
				pathPart = strings.Trim(pathPart, `;"'`+"`")

				clausePart := strings.TrimSpace(trimmed[7:fromIdx])
				clausePart = strings.Trim(clausePart, "{}")
				var syms []string
				for _, s := range strings.Split(clausePart, ",") {
					sym := strings.TrimSpace(s)
					if sym != "" {
						syms = append(syms, sym)
					}
				}
				imports = append(imports, ModuleImport{
					ImportPath:      pathPart,
					ImportedSymbols: syms,
					SourceFilePath:  filePath,
				})
			}
		}

		// CommonJS: const ... = require('...')
		if strings.Contains(trimmed, "require(") {
			start := strings.Index(trimmed, "require(") + 8
			end := strings.Index(trimmed[start:], ")")
			if end >= 0 {
				rawPath := strings.Trim(strings.TrimSpace(trimmed[start:start+end]), `"'`+"`")
				imports = append(imports, ModuleImport{
					ImportPath:     rawPath,
					SourceFilePath: filePath,
				})
			}
		}
	}

	return imports
}

// TSConfig represents path alias configurations from tsconfig.json or jsconfig.json.
type TSConfig struct {
	BaseURL string              `json:"baseUrl"`
	Paths   map[string][]string `json:"paths"`
}

// LoadTSConfig attempts to load and parse tsconfig.json or jsconfig.json from workspaceRoot.
func LoadTSConfig(workspaceRoot string) *TSConfig {
	for _, name := range []string{"tsconfig.json", "jsconfig.json"} {
		p := filepath.Join(workspaceRoot, name)
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}

		cleaned := stripJSONComments(string(data))
		var raw struct {
			CompilerOptions struct {
				BaseURL string              `json:"baseUrl"`
				Paths   map[string][]string `json:"paths"`
			} `json:"compilerOptions"`
		}

		if err := json.Unmarshal([]byte(cleaned), &raw); err == nil {
			return &TSConfig{
				BaseURL: raw.CompilerOptions.BaseURL,
				Paths:   raw.CompilerOptions.Paths,
			}
		}
	}
	return nil
}

// stripJSONComments removes // and /* ... */ comments from JSONC content.
func stripJSONComments(content string) string {
	var sb strings.Builder
	inString := false
	inLineComment := false
	inBlockComment := false
	escape := false
	runes := []rune(content)
	n := len(runes)

	for i := 0; i < n; i++ {
		r := runes[i]

		if inLineComment {
			if r == '\n' {
				inLineComment = false
				sb.WriteRune(r)
			}
			continue
		}

		if inBlockComment {
			if r == '*' && i+1 < n && runes[i+1] == '/' {
				inBlockComment = false
				i++ // skip '/'
			}
			continue
		}

		if inString {
			sb.WriteRune(r)
			if escape {
				escape = false
			} else if r == '\\' {
				escape = true
			} else if r == '"' {
				inString = false
			}
			continue
		}

		// Not in string or comment
		if r == '"' {
			inString = true
			sb.WriteRune(r)
			continue
		}

		if r == '/' && i+1 < n {
			if runes[i+1] == '/' {
				inLineComment = true
				i++ // skip second '/'
				continue
			} else if runes[i+1] == '*' {
				inBlockComment = true
				i++ // skip '*'
				continue
			}
		}

		sb.WriteRune(r)
	}

	return sb.String()
}

// ResolveImportedFile attempts to resolve an import path relative to source file or via tsconfig aliases.
func ResolveImportedFile(workspaceRoot, sourcePath, importPath string, tsconfig *TSConfig) []string {
	var targets []string
	extensions := []string{"", ".ts", ".tsx", ".js", ".jsx", "/index.ts", "/index.js"}

	// 1. Relative imports (./ or ../)
	if strings.HasPrefix(importPath, "./") || strings.HasPrefix(importPath, "../") {
		dir := filepath.Dir(sourcePath)
		target := filepath.Join(dir, importPath)
		targets = append(targets, target)
	} else if tsconfig != nil {
		// 2. Check tsconfig paths aliases (e.g. "@/components/*" or "~/*")
		matched := false
		for pattern, mappings := range tsconfig.Paths {
			prefix := strings.TrimSuffix(pattern, "*")
			if strings.HasPrefix(importPath, prefix) {
				remainder := strings.TrimPrefix(importPath, prefix)
				for _, mapping := range mappings {
					mappingPrefix := strings.TrimSuffix(mapping, "*")
					resolvedRel := filepath.Join(mappingPrefix, remainder)
					baseDir := workspaceRoot
					if tsconfig.BaseURL != "" {
						baseDir = filepath.Join(workspaceRoot, tsconfig.BaseURL)
					}
					target := filepath.Join(baseDir, resolvedRel)
					targets = append(targets, target)
					matched = true
				}
			}
		}

		// 3. Check tsconfig baseUrl resolution
		if !matched && tsconfig.BaseURL != "" {
			baseDir := filepath.Join(workspaceRoot, tsconfig.BaseURL)
			targets = append(targets, filepath.Join(baseDir, importPath))
		}
	}

	// If neither relative nor tsconfig matched, also check relative to workspaceRoot as fallback
	if len(targets) == 0 && workspaceRoot != "" {
		targets = append(targets, filepath.Join(workspaceRoot, importPath))
	}

	var candidates []string
	for _, target := range targets {
		for _, ext := range extensions {
			candidates = append(candidates, target+ext)
		}
	}
	return candidates
}
