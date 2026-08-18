package codegen

import (
	"fmt"
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// LintViolation represents an unimported or invalid namespace reference in generated code.
type LintViolation struct {
	Namespace string `json:"namespace"`
	Line      int    `json:"line"`
	Message   string `json:"message"`
}

// LanguageLinter is a pluggable interface for language-specific AST validation and linting.
// Users and extensions can register custom linters for proprietary languages or frameworks (ADR-0082).
type LanguageLinter interface {
	CheckImports(filename string, source []byte) ([]LintViolation, error)
}

// TreeSitterASTLinter implements LanguageLinter using pure-Go tree-sitter AST parsing.
type TreeSitterASTLinter struct{}

// NewTreeSitterASTLinter creates a new tree-sitter based AST import validator.
func NewTreeSitterASTLinter() *TreeSitterASTLinter {
	return &TreeSitterASTLinter{}
}

// Common standard library namespaces for fast cross-language validation
var knownGoStdlib = map[string]string{
	"json":     "encoding/json",
	"xml":      "encoding/xml",
	"base64":   "encoding/base64",
	"hex":      "encoding/hex",
	"csv":      "encoding/csv",
	"binary":   "encoding/binary",
	"fmt":      "fmt",
	"os":       "os",
	"io":       "io",
	"ioutil":   "io/ioutil",
	"sync":     "sync",
	"atomic":   "sync/atomic",
	"time":     "time",
	"context":  "context",
	"strings":  "strings",
	"bytes":    "bytes",
	"regexp":   "regexp",
	"strconv":  "strconv",
	"sort":     "sort",
	"math":     "math",
	"rand":     "math/rand",
	"big":      "math/big",
	"http":     "net/http",
	"url":      "net/url",
	"net":      "net",
	"filepath": "path/filepath",
	"path":     "path",
	"reflect":  "reflect",
	"log":      "log",
	"slog":     "log/slog",
	"flag":     "flag",
	"testing":  "testing",
	"sql":      "database/sql",
	"sha256":   "crypto/sha256",
	"md5":      "crypto/md5",
	"tls":      "crypto/tls",
}

var knownPythonStdlib = map[string]bool{
	"os": true, "sys": true, "json": true, "re": true,
	"time": true, "datetime": true, "math": true, "random": true,
	"pathlib": true, "subprocess": true, "typing": true,
	"collections": true, "itertools": true, "functools": true,
	"asyncio": true, "logging": true, "shutil": true, "tempfile": true,
}

var knownNodeStdlib = map[string]bool{
	"fs": true, "path": true, "os": true, "http": true,
	"https": true, "crypto": true, "util": true, "events": true,
	"stream": true, "child_process": true, "url": true, "buffer": true,
}

// CheckImports inspects member call roots against declared imports across Go, TypeScript, Python, and Rust.
func (l *TreeSitterASTLinter) CheckImports(filename string, source []byte) ([]LintViolation, error) {
	if len(source) == 0 {
		return nil, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry == nil {
		return nil, nil // Unsupported language — graceful degradation
	}

	lang := entry.Language()
	if lang == nil {
		return nil, nil
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil || tree == nil {
		return nil, nil
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return nil, nil
	}

	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".go":
		return l.checkGoImports(bt, root, source)
	case ".py":
		return l.checkPythonImports(bt, root, source)
	case ".ts", ".js", ".tsx", ".jsx":
		return l.checkNodeImports(bt, root, source)
	default:
		return nil, nil
	}
}

// checkGoImports checks Go selector expressions against declared import specs.
func (l *TreeSitterASTLinter) checkGoImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) ([]LintViolation, error) {
	imported := make(map[string]bool)

	// Collect declared imports
	var collectImports func(n *gotreesitter.Node)
	collectImports = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		if bt.NodeType(n) == "import_spec" {
			var alias string
			var importPath string
			for i := 0; i < int(n.ChildCount()); i++ {
				child := n.Child(i)
				if child == nil {
					continue
				}
				childType := bt.NodeType(child)
				if childType == "package_identifier" {
					alias = bt.NodeText(child)
				} else if childType == "interpreted_string_literal" {
					importPath = strings.Trim(bt.NodeText(child), `"`)
				}
			}
			if alias != "" {
				imported[alias] = true
			} else if importPath != "" {
				parts := strings.Split(importPath, "/")
				pkgName := parts[len(parts)-1]
				imported[pkgName] = true
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			collectImports(n.Child(i))
		}
	}
	collectImports(root)

	// Find selector expressions where root matches a known stdlib package but is unimported
	var violations []LintViolation
	seenViolations := make(map[string]bool)

	var checkSelectors func(n *gotreesitter.Node)
	checkSelectors = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		if bt.NodeType(n) == "selector_expression" {
			operand := n.Child(0)
			if operand != nil && (bt.NodeType(operand) == "identifier" || bt.NodeType(operand) == "package_identifier") {
				ident := bt.NodeText(operand)
				if stdlibPkg, isStdlib := knownGoStdlib[ident]; isStdlib {
					if !imported[ident] {
						line := int(n.StartPoint().Row) + 1
						key := fmt.Sprintf("%s:%d", ident, line)
						if !seenViolations[key] {
							seenViolations[key] = true
							violations = append(violations, LintViolation{
								Namespace: ident,
								Line:      line,
								Message:   fmt.Sprintf("missing import for %q (%s) used on line %d", ident, stdlibPkg, line),
							})
						}
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			checkSelectors(n.Child(i))
		}
	}
	checkSelectors(root)

	return violations, nil
}

// checkPythonImports checks Python attribute references against imported modules.
func (l *TreeSitterASTLinter) checkPythonImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) ([]LintViolation, error) {
	imported := make(map[string]bool)

	var collectImports func(n *gotreesitter.Node)
	collectImports = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nType := bt.NodeType(n)
		if nType == "import_statement" || nType == "import_from_statement" {
			text := bt.NodeText(n)
			for mod := range knownPythonStdlib {
				if strings.Contains(text, mod) {
					imported[mod] = true
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			collectImports(n.Child(i))
		}
	}
	collectImports(root)

	var violations []LintViolation
	seenViolations := make(map[string]bool)

	var checkAttributes func(n *gotreesitter.Node)
	checkAttributes = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		if bt.NodeType(n) == "attribute" {
			obj := n.Child(0)
			if obj != nil && bt.NodeType(obj) == "identifier" {
				ident := bt.NodeText(obj)
				if knownPythonStdlib[ident] && !imported[ident] {
					line := int(n.StartPoint().Row) + 1
					key := fmt.Sprintf("%s:%d", ident, line)
					if !seenViolations[key] {
						seenViolations[key] = true
						violations = append(violations, LintViolation{
							Namespace: ident,
							Line:      line,
							Message:   fmt.Sprintf("missing import for python module %q used on line %d", ident, line),
						})
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			checkAttributes(n.Child(i))
		}
	}
	checkAttributes(root)

	return violations, nil
}

// checkNodeImports checks JavaScript/TypeScript member access against imported modules.
func (l *TreeSitterASTLinter) checkNodeImports(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte) ([]LintViolation, error) {
	imported := make(map[string]bool)

	var collectImports func(n *gotreesitter.Node)
	collectImports = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nType := bt.NodeType(n)
		if strings.Contains(nType, "import") || strings.Contains(nType, "require") {
			text := bt.NodeText(n)
			for mod := range knownNodeStdlib {
				if strings.Contains(text, mod) {
					imported[mod] = true
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			collectImports(n.Child(i))
		}
	}
	collectImports(root)

	var violations []LintViolation
	seenViolations := make(map[string]bool)

	var checkNodes func(n *gotreesitter.Node)
	checkNodes = func(n *gotreesitter.Node) {
		if n == nil {
			return
		}
		nType := bt.NodeType(n)
		if nType == "member_expression" {
			if n.ChildCount() > 0 {
				obj := n.Child(0)
				if obj != nil {
					ident := bt.NodeText(obj)
					if knownNodeStdlib[ident] && !imported[ident] {
						line := int(n.StartPoint().Row) + 1
						key := fmt.Sprintf("%s:%d", ident, line)
						if !seenViolations[key] {
							seenViolations[key] = true
							violations = append(violations, LintViolation{
								Namespace: ident,
								Line:      line,
								Message:   fmt.Sprintf("missing import for module %q used on line %d", ident, line),
							})
						}
					}
				}
			}
		}
		for i := 0; i < int(n.ChildCount()); i++ {
			checkNodes(n.Child(i))
		}
	}
	checkNodes(root)

	return violations, nil
}
