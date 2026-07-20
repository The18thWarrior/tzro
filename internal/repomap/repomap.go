// Package repomap provides a deterministic AST-based utility that walks a Go source tree,
// parses each file's public declarations, and emits a structured markdown document
// mapping the codebase architecture.
//
// Used as the pre-compiled context source for Direct Synthesis mode (Grilling Decision #4)
// and as a general-purpose codebase overview tool.
package repomap

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// GenerateRepoMap walks the Go source tree at rootDir, parses each .go file's
// exported declarations, and returns a structured markdown string.
// Skips test files (*_test.go) and non-Go files.
func GenerateRepoMap(rootDir string) (string, error) {
	var combined strings.Builder
	combined.WriteString("# Source Architecture Map\n\n")

	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		// Only process Go files, skip tests
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}

		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, parser.DeclarationErrors)
		if err != nil {
			return nil // Skip unparseable files
		}

		relPath, _ := filepath.Rel(rootDir, path)
		combined.WriteString(fmt.Sprintf("## File: %s\n", relPath))
		combined.WriteString(fmt.Sprintf("- **Package**: `%s`\n", f.Name.Name))

		var structs []string
		var interfaces []string
		var funcs []string

		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				if d.Tok == token.TYPE {
					for _, spec := range d.Specs {
						ts, ok := spec.(*ast.TypeSpec)
						if !ok {
							continue
						}
						if ast.IsExported(ts.Name.Name) {
							switch ts.Type.(type) {
							case *ast.StructType:
								structs = append(structs, ts.Name.Name)
							case *ast.InterfaceType:
								interfaces = append(interfaces, ts.Name.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				if ast.IsExported(d.Name.Name) {
					recv := ""
					if d.Recv != nil && len(d.Recv.List) > 0 {
						recv = formatReceiver(d.Recv.List[0].Type)
					}
					funcs = append(funcs, fmt.Sprintf("`func %s%s`", recv, d.Name.Name))
				}
			}
		}

		if len(structs) > 0 {
			combined.WriteString(fmt.Sprintf("- **Structs**: %s\n", strings.Join(structs, ", ")))
		}
		if len(interfaces) > 0 {
			combined.WriteString(fmt.Sprintf("- **Interfaces**: %s\n", strings.Join(interfaces, ", ")))
		}
		if len(funcs) > 0 {
			combined.WriteString("- **Functions**:\n")
			for _, fn := range funcs {
				combined.WriteString(fmt.Sprintf("  - %s\n", fn))
			}
		}
		combined.WriteString("\n")
		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to walk source tree: %w", err)
	}

	return combined.String(), nil
}

// formatReceiver extracts a human-readable receiver string from a function declaration.
func formatReceiver(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return fmt.Sprintf("(%s) ", t.Name)
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return fmt.Sprintf("(*%s) ", ident.Name)
		}
	}
	return ""
}
