package context

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

var commonKeywords = map[string]bool{
	"if": true, "else": true, "for": true, "while": true, "switch": true,
	"case": true, "select": true, "return": true, "range": true, "break": true,
	"continue": true, "default": true, "fallthrough": true, "goto": true,
	"defer": true, "go": true, "make": true, "new": true, "len": true,
	"cap": true, "append": true, "copy": true, "delete": true, "close": true,
	"panic": true, "recover": true, "print": true, "println": true,
	"sizeof": true, "typeof": true, "catch": true, "except": true, "finally": true,
	"try": true, "throw": true, "match": true, "let": true, "var": true,
	"const": true, "function": true, "func": true, "def": true, "fn": true,
	"import": true, "from": true, "as": true, "class": true, "struct": true,
	"type": true, "interface": true, "trait": true, "impl": true, "pub": true,
}

// ExtractOutgoingCalls finds functions or methods invoked inside lines [startLine, endLine] of source.
func ExtractOutgoingCalls(filePath string, source []byte, startLine, endLine int) []string {
	if len(source) == 0 || startLine <= 0 || endLine < startLine {
		return nil
	}

	seen := make(map[string]bool)
	var callees []string

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry != nil && entry.Language() != nil {
		parser := gotreesitter.NewParser(entry.Language())
		tree, err := parser.Parse(source)
		if err == nil && tree != nil {
			defer tree.Release()
			bt := gotreesitter.Bind(tree)
			root := bt.RootNode()
			if root != nil {
				langName := strings.ToLower(entry.Name)
				var walk func(n *gotreesitter.Node)
				walk = func(n *gotreesitter.Node) {
					if n == nil {
						return
					}
					nodeStart := int(n.StartPoint().Row) + 1
					nodeEnd := int(n.EndPoint().Row) + 1
					if nodeEnd < startLine || nodeStart > endLine {
						return
					}

					nodeType := bt.NodeType(n)
					var fnNode *gotreesitter.Node
					if nodeType == "call_expression" || nodeType == "call" {
						fnNode = bt.ChildByField(n, "function")
						if fnNode == nil {
							// For TS/JS, function child might be named "expression"
							fnNode = bt.ChildByField(n, "expression")
						}
					}

					if fnNode != nil {
						calleeName := extractCalleeName(bt, fnNode, source, langName)
						if calleeName != "" && !commonKeywords[calleeName] && !seen[calleeName] {
							seen[calleeName] = true
							callees = append(callees, calleeName)
						}
					}

					for i := 0; i < n.ChildCount(); i++ {
						walk(n.Child(i))
					}
				}
				walk(root)
			}
		}
	}

	// Fallback regex if Tree-sitter found nothing or was unavailable
	if len(callees) == 0 {
		lines := strings.Split(string(source), "\n")
		sl := startLine - 1
		if sl < 0 {
			sl = 0
		}
		el := endLine
		if el > len(lines) {
			el = len(lines)
		}
		subText := strings.Join(lines[sl:el], "\n")
		re := regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_]*)\s*\(`)
		matches := re.FindAllStringSubmatch(subText, -1)
		for _, m := range matches {
			name := m[1]
			if !commonKeywords[name] && !seen[name] {
				seen[name] = true
				callees = append(callees, name)
			}
		}
	}

	sort.Strings(callees)
	return callees
}

func extractCalleeName(bt *gotreesitter.BoundTree, n *gotreesitter.Node, source []byte, langName string) string {
	if n == nil {
		return ""
	}
	nt := bt.NodeType(n)
	if nt == "identifier" || nt == "type_identifier" {
		return string(source[n.StartByte():n.EndByte()])
	}

	switch langName {
	case "go":
		if nt == "selector_expression" {
			field := bt.ChildByField(n, "field")
			if field != nil {
				return string(source[field.StartByte():field.EndByte()])
			}
		}
	case "typescript", "tsx", "javascript":
		if nt == "member_expression" {
			prop := bt.ChildByField(n, "property")
			if prop != nil {
				return string(source[prop.StartByte():prop.EndByte()])
			}
		}
	case "python":
		if nt == "attribute" {
			attr := bt.ChildByField(n, "attribute")
			if attr != nil {
				return string(source[attr.StartByte():attr.EndByte()])
			}
		}
	case "rust":
		if nt == "field_expression" {
			field := bt.ChildByField(n, "field")
			if field != nil {
				return string(source[field.StartByte():field.EndByte()])
			}
		} else if nt == "scoped_identifier" {
			name := bt.ChildByField(n, "name")
			if name != nil {
				return string(source[name.StartByte():name.EndByte()])
			}
		}
	}

	// Try child named field, property, name, or last identifier child
	for i := n.ChildCount() - 1; i >= 0; i-- {
		ch := n.Child(i)
		if bt.NodeType(ch) == "identifier" {
			return string(source[ch.StartByte():ch.EndByte()])
		}
	}
	return ""
}
