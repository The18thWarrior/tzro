package ast

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	"tzro/pkg/store"
)

type bodyReplacement struct {
	startByte   uint32
	endByte     uint32
	startLine   int
	endLine     int
	original    string
	replacement string
	hash        string
	symbolName  string
	kind        string
}

// SkeletonResult holds the transformed code and compaction metrics.
type SkeletonResult struct {
	SkeletonCode string
	OriginalSize int
	SkeletonSize int
	SavingsRatio float64
	ElidedBlocks int
	Hashes       []string
}

// Skeletonize parses a source file with Tree-sitter, stubs function bodies with hash markers,
// saves the elided bodies into the provided Store, and returns the SkeletonResult.
func Skeletonize(filePath string, source []byte, s *store.Store) (*SkeletonResult, error) {
	originalLen := len(source)
	if originalLen == 0 {
		return &SkeletonResult{
			SkeletonCode: "",
			OriginalSize: 0,
			SkeletonSize: 0,
			SavingsRatio: 0,
		}, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		// Fallback for unsupported file types: return original source
		return &SkeletonResult{
			SkeletonCode: string(source),
			OriginalSize: originalLen,
			SkeletonSize: originalLen,
			SavingsRatio: 0,
		}, nil
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return &SkeletonResult{
			SkeletonCode: string(source),
			OriginalSize: originalLen,
			SkeletonSize: originalLen,
			SavingsRatio: 0,
		}, nil
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return &SkeletonResult{
			SkeletonCode: string(source),
			OriginalSize: originalLen,
			SkeletonSize: originalLen,
			SavingsRatio: 0,
		}, nil
	}

	langName := strings.ToLower(entry.Name)
	var replacements []bodyReplacement

	commentPrefix := "//"
	if langName == "python" || langName == "ruby" || langName == "bash" {
		commentPrefix = "#"
	}

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		nodeType := bt.NodeType(node)

		var bodyNode *gotreesitter.Node
		var nameNode *gotreesitter.Node
		kind := "function"

		switch langName {
		case "go":
			if nodeType == "function_declaration" || nodeType == "method_declaration" {
				bodyNode = bt.ChildByField(node, "body")
				nameNode = bt.ChildByField(node, "name")
				if nodeType == "method_declaration" {
					kind = "method"
				}
			}
		case "python":
			if nodeType == "function_definition" {
				bodyNode = bt.ChildByField(node, "body")
				nameNode = bt.ChildByField(node, "name")
			}
		case "typescript", "tsx", "javascript":
			if nodeType == "function_declaration" || nodeType == "method_definition" || nodeType == "arrow_function" {
				bodyNode = bt.ChildByField(node, "body")
				nameNode = bt.ChildByField(node, "name")
				if nodeType == "method_definition" {
					kind = "method"
				}
			}
		case "rust":
			if nodeType == "function_item" {
				bodyNode = bt.ChildByField(node, "body")
				nameNode = bt.ChildByField(node, "name")
			}
		case "java", "c", "cpp", "c_sharp":
			if nodeType == "method_declaration" || nodeType == "function_definition" {
				bodyNode = bt.ChildByField(node, "body")
				nameNode = bt.ChildByField(node, "name")
			}
		case "markdown":
			// Markdown skeletonization: elide heavy content blocks, preserve document spine
			switch nodeType {
			case "fenced_code_block":
				// Elide the entire fenced code block, preserving the info string hint
				startByte := node.StartByte()
				endByte := node.EndByte()
				origContent := string(source[startByte:endByte])
				if len(origContent) > 80 { // Only elide non-trivial blocks
					startLine := int(node.StartPoint().Row) + 1
					endLine := int(node.EndPoint().Row) + 1
					hash := store.ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, startLine, endLine, origContent))

					// Extract info string (language hint) from first line
					firstLine := origContent
					if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
						firstLine = firstLine[:idx]
					}

					repStr := fmt.Sprintf("%s\n<!-- [body elided: #%s] -->\n```", firstLine, hash)
					replacements = append(replacements, bodyReplacement{
						startByte:   startByte,
						endByte:     endByte,
						startLine:   startLine,
						endLine:     endLine,
						original:    origContent,
						replacement: repStr,
						hash:        hash,
						symbolName:  "code_block",
						kind:        "fenced_code_block",
					})
					return
				}
			case "html_block":
				startByte := node.StartByte()
				endByte := node.EndByte()
				origContent := string(source[startByte:endByte])
				if len(origContent) > 80 {
					startLine := int(node.StartPoint().Row) + 1
					endLine := int(node.EndPoint().Row) + 1
					hash := store.ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, startLine, endLine, origContent))
					repStr := fmt.Sprintf("<!-- [html block elided: #%s] -->", hash)
					replacements = append(replacements, bodyReplacement{
						startByte:   startByte,
						endByte:     endByte,
						startLine:   startLine,
						endLine:     endLine,
						original:    origContent,
						replacement: repStr,
						hash:        hash,
						symbolName:  "html_block",
						kind:        "html_block",
					})
					return
				}
			case "paragraph":
				startByte := node.StartByte()
				endByte := node.EndByte()
				origContent := string(source[startByte:endByte])
				if len(origContent) > 500 {
					startLine := int(node.StartPoint().Row) + 1
					endLine := int(node.EndPoint().Row) + 1
					hash := store.ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, startLine, endLine, origContent))
					// Preserve the first sentence as a preview
					preview := origContent
					if idx := strings.Index(preview, ". "); idx >= 0 && idx < 120 {
						preview = preview[:idx+1]
					} else if len(preview) > 120 {
						preview = preview[:120]
					}
					repStr := fmt.Sprintf("%s… <!-- [paragraph elided: #%s] -->", preview, hash)
					replacements = append(replacements, bodyReplacement{
						startByte:   startByte,
						endByte:     endByte,
						startLine:   startLine,
						endLine:     endLine,
						original:    origContent,
						replacement: repStr,
						hash:        hash,
						symbolName:  "paragraph",
						kind:        "paragraph",
					})
					return
				}
			}
			// For all other markdown nodes (headings, lists, etc.), descend into children
			for i := 0; i < node.ChildCount(); i++ {
				walk(node.Child(i))
			}
			return
		}

		if bodyNode != nil && bodyNode.EndByte() > bodyNode.StartByte() {
			startByte := bodyNode.StartByte()
			endByte := bodyNode.EndByte()
			origBody := string(source[startByte:endByte])

			// Calculate line numbers
			startLine := int(bodyNode.StartPoint().Row) + 1
			endLine := int(bodyNode.EndPoint().Row) + 1

			symName := ""
			if nameNode != nil && nameNode.EndByte() > nameNode.StartByte() {
				symName = string(source[nameNode.StartByte():nameNode.EndByte()])
			}

			hash := store.ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, startLine, endLine, origBody))

			var repStr string
			if langName == "python" {
				repStr = fmt.Sprintf("%s [body elided: #%s]\n\tpass", commentPrefix, hash)
			} else {
				repStr = fmt.Sprintf("{\n\t%s [body elided: #%s]\n}", commentPrefix, hash)
			}

			replacements = append(replacements, bodyReplacement{
				startByte:   startByte,
				endByte:     endByte,
				startLine:   startLine,
				endLine:     endLine,
				original:    origBody,
				replacement: repStr,
				hash:        hash,
				symbolName:  symName,
				kind:        kind,
			})
			return // Don't descend into elided body
		}

		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}

	walk(root)

	if len(replacements) == 0 {
		return &SkeletonResult{
			SkeletonCode: string(source),
			OriginalSize: originalLen,
			SkeletonSize: originalLen,
			SavingsRatio: 0,
		}, nil
	}

	// Sort replacements by startByte
	sort.Slice(replacements, func(i, j int) bool {
		return replacements[i].startByte < replacements[j].startByte
	})

	var hashes []string
	var out strings.Builder
	var lastOffset uint32 = 0
	srcLen := uint32(len(source))

	for _, rep := range replacements {
		if rep.startByte > srcLen || rep.endByte > srcLen || rep.startByte < lastOffset {
			continue
		}
		out.Write(source[lastOffset:rep.startByte])
		out.WriteString(rep.replacement)
		lastOffset = rep.endByte
		hashes = append(hashes, rep.hash)

		// Store in SQLite if available
		if s != nil {
			_, _ = s.PutBlob(filePath, rep.startLine, rep.endLine, rep.original)
			if rep.symbolName != "" {
				_ = s.IndexSymbol(rep.symbolName, rep.kind, filePath, rep.startLine, rep.hash)
			}
		}
	}

	if lastOffset < srcLen {
		out.Write(source[lastOffset:])
	}

	skeletonStr := out.String()
	skeletonLen := len(skeletonStr)
	savings := float64(originalLen-skeletonLen) / float64(originalLen)
	if savings < 0 {
		savings = 0
	}

	return &SkeletonResult{
		SkeletonCode: skeletonStr,
		OriginalSize: originalLen,
		SkeletonSize: skeletonLen,
		SavingsRatio: savings,
		ElidedBlocks: len(hashes),
		Hashes:       hashes,
	}, nil
}
