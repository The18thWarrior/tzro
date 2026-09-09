package context

import (
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// ModuleImport represents an imported module or symbol reference.
type ModuleImport struct {
	ImportPath     string   // e.g. "./auth/jwt" or "express"
	ImportedSymbols []string // e.g. ["ValidateToken", "TokenPayload"]
	IsDefault      bool
	SourceFilePath string
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
				imp.ImportPath = strings.Trim(rawPath, `"'` + "`")
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
				pathPart = strings.Trim(pathPart, `;"'` + "`")

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
				rawPath := strings.Trim(strings.TrimSpace(trimmed[start:start+end]), `"'` + "`")
				imports = append(imports, ModuleImport{
					ImportPath:     rawPath,
					SourceFilePath: filePath,
				})
			}
		}
	}

	return imports
}

// ResolveImportedFile attempts to resolve an import path relative to source file.
func ResolveImportedFile(sourcePath, importPath string) []string {
	dir := filepath.Dir(sourcePath)
	target := filepath.Join(dir, importPath)

	var candidates []string
	extensions := []string{"", ".ts", ".tsx", ".js", ".jsx", "/index.ts", "/index.js"}
	for _, ext := range extensions {
		candidates = append(candidates, target+ext)
	}
	return candidates
}

