package symbols

import (
	"path/filepath"
	"sort"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

type bodyStubReplacement struct {
	startByte   uint32
	endByte     uint32
	replacement string
}

// StubCodeBodies performs Context-Role Aware AST Body Stubbing (ADR-0092).
// It preserves all imports, types, structs, interfaces, type aliases, and function/method
// signatures, while replacing function bodies with compact stubs.
// Returns the stubbed source code. Falls back to compactor.ExtractSkeleton if AST parsing fails.
func StubCodeBodies(filename string, source []byte) string {
	if len(source) == 0 {
		return ""
	}

	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry == nil || entry.Language() == nil {
		return string(source)
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return string(source)
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return string(source)
	}

	langName := strings.ToLower(entry.Name)
	var replacements []bodyStubReplacement

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nodeType := bt.NodeType(node)

		switch langName {
		case "go":
			if nodeType == "function_declaration" || nodeType == "method_declaration" {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil {
					replacements = append(replacements, bodyStubReplacement{
						startByte:   bodyNode.StartByte(),
						endByte:     bodyNode.EndByte(),
						replacement: "{\n\t/* ... */\n}",
					})
					return // do not walk inside the replaced body
				}
			}
		case "python":
			if nodeType == "function_definition" {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil {
					replacements = append(replacements, bodyStubReplacement{
						startByte:   bodyNode.StartByte(),
						endByte:     bodyNode.EndByte(),
						replacement: "pass",
					})
					return
				}
			}
		case "typescript", "tsx", "javascript":
			if nodeType == "function_declaration" || nodeType == "method_definition" || nodeType == "arrow_function" {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil && bt.NodeType(bodyNode) == "statement_block" {
					replacements = append(replacements, bodyStubReplacement{
						startByte:   bodyNode.StartByte(),
						endByte:     bodyNode.EndByte(),
						replacement: "{\n\t/* ... */\n}",
					})
					return
				}
			}
		case "rust":
			if nodeType == "function_item" {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil {
					replacements = append(replacements, bodyStubReplacement{
						startByte:   bodyNode.StartByte(),
						endByte:     bodyNode.EndByte(),
						replacement: "{\n\t/* ... */\n}",
					})
					return
				}
			}
		case "java", "c", "cpp":
			if nodeType == "method_declaration" || nodeType == "function_definition" {
				bodyNode := bt.ChildByField(node, "body")
				if bodyNode != nil {
					replacements = append(replacements, bodyStubReplacement{
						startByte:   bodyNode.StartByte(),
						endByte:     bodyNode.EndByte(),
						replacement: "{\n\t/* ... */\n}",
					})
					return
				}
			}
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)

	if len(replacements) == 0 {
		return string(source)
	}

	// Sort replacements by ascending startByte
	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].startByte < replacements[j].startByte
	})

	var result strings.Builder
	var lastOffset uint32 = 0
	srcLen := uint32(len(source))

	for _, rep := range replacements {
		if rep.startByte > srcLen || rep.endByte > srcLen || rep.startByte < lastOffset {
			continue
		}
		result.Write(source[lastOffset:rep.startByte])
		result.WriteString(rep.replacement)
		lastOffset = rep.endByte
	}

	if lastOffset < srcLen {
		result.Write(source[lastOffset:])
	}

	return result.String()
}
