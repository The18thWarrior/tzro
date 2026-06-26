package compiler

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
)

// GenerateMap creates a static graph context representation of the Go workspace
// by parsing source code with Tree-sitter and extracting key declarations.
func GenerateMap(workspaceRoot string) (string, error) {
	var sb strings.Builder
	lang := golang.GetLanguage()

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

			content, err := os.ReadFile(path)
			if err != nil {
				return nil // Skip files we can't read
			}

			parser := sitter.NewParser()
			parser.SetLanguage(lang)
			tree, _ := parser.ParseCtx(context.Background(), nil, content)

			if tree == nil {
				return nil
			}

			relPath, _ := filepath.Rel(workspaceRoot, path)
			fileSigs := extractSignatures(tree.RootNode(), content)

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

func extractSignatures(root *sitter.Node, content []byte) []string {
	var sigs []string

	// Helper to extract node text
	nodeText := func(n *sitter.Node) string {
		if n == nil {
			return ""
		}
		return string(content[n.StartByte():n.EndByte()])
	}

	// We'll use simple tree traversal for now instead of complex queries
	var traverse func(node *sitter.Node)
	traverse = func(node *sitter.Node) {
		if node == nil {
			return
		}
		switch node.Type() {
		case "type_declaration":
			// A type declaration might have multiple type specs, but let's just grab the whole line (up to newline or '{')
			text := nodeText(node)
			firstLine := strings.Split(text, "{")[0]
			firstLine = strings.Split(firstLine, "\n")[0]
			sigs = append(sigs, strings.TrimSpace(firstLine))
		case "function_declaration":
			nameNode := node.ChildByFieldName("name")
			paramsNode := node.ChildByFieldName("parameters")
			if nameNode != nil && paramsNode != nil {
				sig := fmt.Sprintf("func %s%s", nodeText(nameNode), nodeText(paramsNode))
				// Add return type if present
				resNode := node.ChildByFieldName("result")
				if resNode != nil {
					sig += " " + nodeText(resNode)
				}
				sigs = append(sigs, sig)
			}
		case "method_declaration":
			receiverNode := node.ChildByFieldName("receiver")
			nameNode := node.ChildByFieldName("name")
			paramsNode := node.ChildByFieldName("parameters")
			if receiverNode != nil && nameNode != nil && paramsNode != nil {
				sig := fmt.Sprintf("func %s %s%s", nodeText(receiverNode), nodeText(nameNode), nodeText(paramsNode))
				resNode := node.ChildByFieldName("result")
				if resNode != nil {
					sig += " " + nodeText(resNode)
				}
				sigs = append(sigs, sig)
			}
		}

		for i := 0; i < int(node.ChildCount()); i++ {
			traverse(node.Child(i))
		}
	}

	traverse(root)
	return sigs
}
