package symbols

// imports.go — Language-agnostic import path extraction from tree-sitter AST.
//
// Extracts import/require/use paths from source files to build the import
// affinity map for Exploration Queue rich relevance scoring (ADR-0082).

import (
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// ExtractImports parses the given source code using the appropriate
// tree-sitter grammar and returns all import paths as strings.
// Returns nil, nil for unsupported languages or files with no imports.
func ExtractImports(filename string, source []byte) ([]string, error) {
	if len(source) == 0 {
		return nil, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry == nil {
		return nil, nil
	}

	lang := entry.Language()
	if lang == nil {
		return nil, nil
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return nil, err
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return nil, nil
	}

	langName := strings.ToLower(entry.Name)
	switch langName {
	case "go":
		return extractGoImports(bt, root, source), nil
	case "python":
		return extractPythonImports(bt, root, source), nil
	case "typescript", "tsx", "javascript":
		return extractJSImports(bt, root, source), nil
	case "rust":
		return extractRustImports(bt, root, source), nil
	case "java":
		return extractJavaImports(bt, root, source), nil
	default:
		return nil, nil
	}
}

// extractGoImports walks Go import_declaration nodes and extracts import paths.
// Go tree-sitter grammar: import_declaration → import_spec_list → import_spec → path (interpreted_string_literal)
func extractGoImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) []string {
	var imports []string
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		if nodeType != "import_declaration" {
			return
		}
		// Walk import specs within the declaration
		walkChildrenRecursive(bt, node, func(child *gotreesitter.Node) bool {
			if bt.NodeType(child) == "import_spec" {
				pathNode := bt.ChildByField(child, "path")
				if pathNode != nil {
					path := strings.Trim(bt.NodeText(pathNode), "\"")
					if path != "" {
						imports = append(imports, path)
					}
				}
				return false
			}
			return true
		})
	})
	return imports
}

// extractPythonImports walks Python import_statement and import_from_statement nodes.
func extractPythonImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) []string {
	var imports []string
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "import_statement":
			// import foo, bar
			walkChildrenRecursive(bt, node, func(child *gotreesitter.Node) bool {
				if bt.NodeType(child) == "dotted_name" {
					imports = append(imports, bt.NodeText(child))
					return false
				}
				return true
			})
		case "import_from_statement":
			// from foo.bar import baz
			modNode := bt.ChildByField(node, "module_name")
			if modNode != nil {
				imports = append(imports, bt.NodeText(modNode))
			}
		}
	})
	return imports
}

// extractJSImports walks JavaScript/TypeScript import_statement nodes.
func extractJSImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) []string {
	var imports []string
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		if nodeType != "import_statement" {
			return
		}
		sourceNode := bt.ChildByField(node, "source")
		if sourceNode != nil {
			path := strings.Trim(bt.NodeText(sourceNode), "\"'`")
			if path != "" {
				imports = append(imports, path)
			}
		}
	})
	return imports
}

// extractRustImports walks Rust use_declaration nodes.
func extractRustImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) []string {
	var imports []string
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		if bt.NodeType(node) != "use_declaration" {
			return
		}
		// Extract the path from the use declaration text
		text := bt.NodeText(node)
		// Remove "use " prefix and ";" suffix
		text = strings.TrimPrefix(text, "use ")
		text = strings.TrimSuffix(text, ";")
		text = strings.TrimSpace(text)
		if text != "" {
			// For "use std::io::Read", extract "std::io"
			if idx := strings.LastIndex(text, "::"); idx > 0 {
				text = text[:idx]
			}
			imports = append(imports, strings.ReplaceAll(text, "::", "/"))
		}
	})
	return imports
}

// extractJavaImports walks Java import_declaration nodes.
func extractJavaImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) []string {
	var imports []string
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		if bt.NodeType(node) != "import_declaration" {
			return
		}
		// Extract the scoped identifier
		walkChildrenRecursive(bt, node, func(child *gotreesitter.Node) bool {
			if bt.NodeType(child) == "scoped_identifier" {
				text := bt.NodeText(child)
				// For "com.example.Foo", extract "com.example"
				if idx := strings.LastIndex(text, "."); idx > 0 {
					text = text[:idx]
				}
				imports = append(imports, strings.ReplaceAll(text, ".", "/"))
				return false
			}
			return true
		})
	})
	return imports
}
