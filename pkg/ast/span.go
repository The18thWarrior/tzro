package ast

import (
	"fmt"
	"path/filepath"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
	"tzro/pkg/store"
)

// DeclarationSpan holds a concise AST-extracted declaration for a symbol match.
type DeclarationSpan struct {
	FilePath        string `json:"file_path"`
	SymbolName      string `json:"symbol_name"`
	Kind            string `json:"kind"`
	StartLine       int    `json:"start_line"`
	EndLine         int    `json:"end_line"`
	EnclosingHeader string `json:"enclosing_header,omitempty"`
	Signature       string `json:"signature"`
	Docstring       string `json:"docstring,omitempty"`
	BodyHash        string `json:"body_hash,omitempty"`
	UsageSnippet    string `json:"usage_snippet,omitempty"`
	Code            string `json:"code"`
	TokenWeight     int    `json:"token_weight"`
}

// EstimateTokens provides a deterministic rule-of-thumb estimate (~4 chars per token).
func EstimateTokens(text string) int {
	tokens := len(text) / 4
	if tokens == 0 && len(text) > 0 {
		return 1
	}
	return tokens
}

// ExtractDeclarationSpan extracts a concise AST declaration span for a symbol at targetLine.
// It preserves the signature, docstring, and body elision tag, storing the full body in the store.
// Falls back to a bounded 25-line window when Tree-sitter is unavailable.
func ExtractDeclarationSpan(
	filePath string,
	source []byte,
	targetLine int,
	symbolName string,
	s *store.Store,
) (*DeclarationSpan, error) {
	if len(source) == 0 {
		return nil, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filePath))
	if entry == nil || entry.Language() == nil {
		return extractFallbackSpan(filePath, source, targetLine, symbolName, s)
	}

	parser := gotreesitter.NewParser(entry.Language())
	tree, err := parser.Parse(source)
	if err != nil {
		return extractFallbackSpan(filePath, source, targetLine, symbolName, s)
	}
	defer tree.Release()

	bt := gotreesitter.Bind(tree)
	root := bt.RootNode()
	if root == nil {
		return extractFallbackSpan(filePath, source, targetLine, symbolName, s)
	}

	langName := strings.ToLower(entry.Name)
	commentPrefix := "//"
	if langName == "python" || langName == "ruby" || langName == "bash" {
		commentPrefix = "#"
	}

	// Walk the AST to find the declaration node enclosing targetLine.
	type declInfo struct {
		node     *gotreesitter.Node
		bodyNode *gotreesitter.Node
		nameNode *gotreesitter.Node
		kind     string
	}

	var bestDecl *declInfo

	var walk func(node *gotreesitter.Node)
	walk = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}

		nodeStartLine := int(node.StartPoint().Row) + 1
		nodeEndLine := int(node.EndPoint().Row) + 1

		// Skip nodes that don't contain the target line
		if targetLine < nodeStartLine || targetLine > nodeEndLine {
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
		}

		if bodyNode != nil {
			bestDecl = &declInfo{
				node:     node,
				bodyNode: bodyNode,
				nameNode: nameNode,
				kind:     kind,
			}
		}

		// Descend into children to find the tightest enclosing declaration
		for i := 0; i < node.ChildCount(); i++ {
			walk(node.Child(i))
		}
	}
	walk(root)

	if bestDecl == nil {
		// No declaration found at target line — fall back
		return extractFallbackSpan(filePath, source, targetLine, symbolName, s)
	}

	// Extract the declaration signature (everything before the body)
	declStart := bestDecl.node.StartByte()
	bodyStart := bestDecl.bodyNode.StartByte()
	bodyEnd := bestDecl.bodyNode.EndByte()
	declEnd := bestDecl.node.EndByte()

	sigBytes := strings.TrimRight(string(source[declStart:bodyStart]), " \t")
	origBody := string(source[bodyStart:bodyEnd])

	startLine := int(bestDecl.node.StartPoint().Row) + 1
	endLine := int(bestDecl.node.EndPoint().Row) + 1
	bodyStartLine := int(bestDecl.bodyNode.StartPoint().Row) + 1
	bodyEndLine := int(bestDecl.bodyNode.EndPoint().Row) + 1

	// Extract symbol name
	symName := symbolName
	if bestDecl.nameNode != nil && bestDecl.nameNode.EndByte() > bestDecl.nameNode.StartByte() {
		symName = string(source[bestDecl.nameNode.StartByte():bestDecl.nameNode.EndByte()])
	}

	// Extract preceding doc comments
	docstring := extractDocComment(source, startLine, commentPrefix)

	// Compute body hash and create elision tag
	bodyHash := store.ComputeHash(fmt.Sprintf("%s:%d:%d:%s", filePath, bodyStartLine, bodyEndLine, origBody))

	var elidedBody string
	if langName == "python" {
		elidedBody = fmt.Sprintf("%s [body elided: #%s]\n\tpass", commentPrefix, bodyHash)
	} else {
		elidedBody = fmt.Sprintf("{\n\t%s [body elided: #%s]\n}", commentPrefix, bodyHash)
	}

	// Build rendered code
	var codeBuilder strings.Builder
	if docstring != "" {
		codeBuilder.WriteString(docstring)
		codeBuilder.WriteString("\n")
	}
	codeBuilder.WriteString(sigBytes)
	codeBuilder.WriteString(" ")
	codeBuilder.WriteString(elidedBody)

	// Look for enclosing struct/class/type header
	enclosingHeader := findEnclosingHeader(bt, bestDecl.node, source, langName)

	if enclosingHeader != "" {
		code := codeBuilder.String()
		codeBuilder.Reset()
		codeBuilder.WriteString(enclosingHeader)
		codeBuilder.WriteString("\n\n")
		codeBuilder.WriteString(code)
	}

	renderedCode := codeBuilder.String()

	// Store the body in the Content-Hash Store
	if s != nil {
		_, _ = s.PutBlob(filePath, bodyStartLine, bodyEndLine, origBody)
	}

	_ = declEnd // used only for bounds checking

	return &DeclarationSpan{
		FilePath:        filePath,
		SymbolName:      symName,
		Kind:            bestDecl.kind,
		StartLine:       startLine,
		EndLine:         endLine,
		EnclosingHeader: enclosingHeader,
		Signature:       sigBytes,
		Docstring:       docstring,
		BodyHash:        bodyHash,
		Code:            renderedCode,
		TokenWeight:     EstimateTokens(renderedCode),
	}, nil
}

// extractDocComment scans lines above startLine for contiguous comment lines.
func extractDocComment(source []byte, startLine int, commentPrefix string) string {
	lines := strings.Split(string(source), "\n")
	if startLine < 1 || startLine > len(lines) {
		return ""
	}

	var docLines []string
	for i := startLine - 2; i >= 0; i-- { // startLine is 1-indexed
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, commentPrefix) {
			docLines = append([]string{trimmed}, docLines...)
		} else {
			break
		}
	}
	return strings.Join(docLines, "\n")
}

// findEnclosingHeader looks for a parent struct/class/type declaration and returns its header line.
func findEnclosingHeader(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, langName string) string {
	parent := node.Parent()
	for parent != nil {
		parentType := bt.NodeType(parent)

		switch langName {
		case "go":
			// Check if parent is a type_declaration containing a struct_type or interface_type
			if parentType == "method_declaration" || parentType == "function_declaration" {
				// This is a peer, not enclosing — skip
			}
			// Look for type declarations that contain a struct body
			if parentType == "type_declaration" {
				headerLine := int(parent.StartPoint().Row)
				lines := strings.Split(string(source), "\n")
				if headerLine < len(lines) {
					return strings.TrimSpace(lines[headerLine])
				}
			}
		case "typescript", "tsx", "javascript":
			if parentType == "class_declaration" || parentType == "class" {
				headerLine := int(parent.StartPoint().Row)
				lines := strings.Split(string(source), "\n")
				if headerLine < len(lines) {
					return strings.TrimSpace(lines[headerLine])
				}
			}
		case "python":
			if parentType == "class_definition" {
				headerLine := int(parent.StartPoint().Row)
				lines := strings.Split(string(source), "\n")
				if headerLine < len(lines) {
					return strings.TrimSpace(lines[headerLine])
				}
			}
		case "java", "c_sharp":
			if parentType == "class_declaration" {
				headerLine := int(parent.StartPoint().Row)
				lines := strings.Split(string(source), "\n")
				if headerLine < len(lines) {
					return strings.TrimSpace(lines[headerLine])
				}
			}
		}

		parent = parent.Parent()
	}
	return ""
}

// extractFallbackSpan creates a bounded 25-line sliding window around targetLine.
// Used when Tree-sitter is unavailable or fails to parse.
func extractFallbackSpan(
	filePath string,
	source []byte,
	targetLine int,
	symbolName string,
	s *store.Store,
) (*DeclarationSpan, error) {
	lines := strings.Split(string(source), "\n")
	if len(lines) == 0 {
		return nil, nil
	}

	const maxWindow = 25

	// Clamp targetLine to valid range (1-indexed)
	if targetLine < 1 {
		targetLine = 1
	}
	if targetLine > len(lines) {
		targetLine = len(lines)
	}

	targetIdx := targetLine - 1 // 0-indexed

	// Scan backward for doc comments or blank line boundary
	startIdx := targetIdx
	commentPrefix := "//"
	if strings.HasSuffix(filePath, ".py") || strings.HasSuffix(filePath, ".rb") || strings.HasSuffix(filePath, ".sh") {
		commentPrefix = "#"
	}
	for startIdx > 0 && (targetIdx-startIdx) < maxWindow/2 {
		prevTrimmed := strings.TrimSpace(lines[startIdx-1])
		if strings.HasPrefix(prevTrimmed, commentPrefix) || prevTrimmed == "" {
			startIdx--
			if prevTrimmed == "" {
				break
			}
		} else {
			break
		}
	}

	// Scan forward for the opening delimiter or maxWindow
	endIdx := targetIdx
	for endIdx < len(lines)-1 && (endIdx-startIdx) < maxWindow-1 {
		endIdx++
		line := lines[endIdx]
		if strings.Contains(line, "{") || strings.Contains(line, ":") {
			break
		}
	}

	// Clamp to maxWindow
	if endIdx-startIdx >= maxWindow {
		endIdx = startIdx + maxWindow - 1
	}
	if endIdx >= len(lines) {
		endIdx = len(lines) - 1
	}

	windowLines := lines[startIdx : endIdx+1]
	code := strings.Join(windowLines, "\n")

	return &DeclarationSpan{
		FilePath:    filePath,
		SymbolName:  symbolName,
		Kind:        "unknown",
		StartLine:   startIdx + 1,
		EndLine:     endIdx + 1,
		Signature:   strings.TrimSpace(lines[targetIdx]),
		Code:        code,
		TokenWeight: EstimateTokens(code),
	}, nil
}
