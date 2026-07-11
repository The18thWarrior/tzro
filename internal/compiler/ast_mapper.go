package compiler

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
)

// GenerateMap creates a static graph context representation of the Go workspace
// by parsing source code with native go/parser and extracting key declarations.
func GenerateMap(workspaceRoot string) (string, error) {
	var sb strings.Builder

	// Directories to scan (ignore noisy ones)
	targetDirs := []string{"cmd", "internal", "pkg"}

	for _, dir := range targetDirs {
		fullPath := filepath.Join(workspaceRoot, dir)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				// Ignore hidden dirs and noisy dirs
				if strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
				return nil
			}

			// Parse using Go standard library parser
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return nil // Skip files that fail to parse
			}

			var fileSigs []string
			ast.Inspect(file, func(n ast.Node) bool {
				if n == nil {
					return true
				}
				switch x := n.(type) {
				case *ast.TypeSpec:
					var typeKind string
					switch x.Type.(type) {
					case *ast.StructType:
						typeKind = "struct"
					case *ast.InterfaceType:
						typeKind = "interface"
					default:
						var buf bytes.Buffer
						if printer.Fprint(&buf, fset, x.Type) == nil {
							typeKind = buf.String()
						}
					}
					if typeKind != "" {
						fileSigs = append(fileSigs, fmt.Sprintf("type %s %s", x.Name.Name, typeKind))
					}
				case *ast.FuncDecl:
					// Create a shallow copy to clear the body for printing signature only
					declCopy := *x
					declCopy.Body = nil
					var buf bytes.Buffer
					if printer.Fprint(&buf, fset, &declCopy) == nil {
						fileSigs = append(fileSigs, buf.String())
					}
				}
				return true
			})

			relPath, _ := filepath.Rel(workspaceRoot, path)
			if len(fileSigs) > 0 {
				sb.WriteString(fmt.Sprintf("\n### File: %s\n", relPath))
				for _, sig := range fileSigs {
					sb.WriteString(fmt.Sprintf("- %s\n", sig))
				}
			}
			return nil
		})

		if err != nil {
			fmt.Fprintf(os.Stderr, "[AST Mapper] Walk error in %s: %v\n", dir, err)
		}
	}

	return sb.String(), nil
}

// GenerateShallowMap creates a signature-blind directory tree of the workspace
// to provide structural scaffolding to the planner without the latency penalty
// of full AST parsing.
func GenerateShallowMap(workspaceRoot string, maxDepth int) (string, error) {
	var sb strings.Builder
	targetDirs := []string{"cmd", "internal", "pkg", "api", "web", "docs", "scripts"}

	for _, dir := range targetDirs {
		fullPath := filepath.Join(workspaceRoot, dir)
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			continue
		}

		err := filepath.Walk(fullPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Calculate current depth relative to the targetDir
			relToTarget, _ := filepath.Rel(fullPath, path)
			depth := 0
			if relToTarget != "." {
				depth = len(strings.Split(relToTarget, string(filepath.Separator)))
			}

			if depth >= maxDepth {
				if info.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}

			if info.IsDir() {
				// Ignore hidden dirs and noisy dirs
				if strings.HasPrefix(info.Name(), ".") || info.Name() == "vendor" || info.Name() == "node_modules" {
					return filepath.SkipDir
				}

				relPath, _ := filepath.Rel(workspaceRoot, path)
				sb.WriteString(fmt.Sprintf("- %s/\n", relPath))
			}

			return nil
		})

		if err != nil {
			fmt.Fprintf(os.Stderr, "[AST Mapper] Shallow Walk error in %s: %v\n", dir, err)
		}
	}

	return sb.String(), nil
}
