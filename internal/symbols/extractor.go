package symbols

// extractor.go — Deterministic AST-based symbol extraction.
//
// Uses gotreesitter (pure-Go tree-sitter runtime) to parse source files
// and extract public declarations as structured tuples. Runs as a post-read_file
// hook inside Probe Node Thought Chains (ADR-0047).
//
// This complements the heuristic Code Skeleton (internal/compactor/skeleton.go)
// with precise, AST-verified symbol identification.

import (
	"path/filepath"
	"strings"
	"unicode"

	gotreesitter "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars"
)

// SymbolKind categorizes extracted declarations.
type SymbolKind string

const (
	SymbolFunc      SymbolKind = "func"
	SymbolType      SymbolKind = "type"
	SymbolInterface SymbolKind = "interface"
	SymbolConst     SymbolKind = "const"
	SymbolVar       SymbolKind = "var"
	SymbolMethod    SymbolKind = "method"
	SymbolClass     SymbolKind = "class"
	SymbolEnum      SymbolKind = "enum"
	SymbolTrait     SymbolKind = "trait"
)

// Symbol represents a single extracted declaration from a source file.
type Symbol struct {
	Name       string     `json:"name"`
	Kind       SymbolKind `json:"kind"`
	Signature  string     `json:"signature"`
	DocComment string     `json:"docComment,omitempty"` // First line of the doc comment preceding the declaration
	File       string     `json:"file"`
	Line       int        `json:"line"`
	EndLine    int        `json:"endLine"`              // Last line of the declaration (1-indexed)
	Exported   bool       `json:"exported"`
}

// ExtractSymbols parses the given source code using the appropriate
// tree-sitter grammar (detected from filename) and returns all public
// declarations as Symbol tuples. Returns nil, nil for unsupported languages.
func ExtractSymbols(filename string, source []byte) ([]Symbol, error) {
	if len(source) == 0 {
		return nil, nil
	}

	entry := grammars.DetectLanguage(filepath.Base(filename))
	if entry == nil {
		return nil, nil // unsupported language — graceful degradation
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
	extractor := getLanguageExtractor(langName)
	if extractor == nil {
		return nil, nil
	}

	return extractor(bt, root, source, filename), nil
}

// ExtractAllSymbols is like ExtractSymbols but includes unexported/private declarations.
// Used by the Call Graph Builder where internal functions participate in call edges.
func ExtractAllSymbols(filename string, source []byte) ([]Symbol, error) {
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
	extractor := getAllLanguageExtractor(langName)
	if extractor == nil {
		return nil, nil
	}

	return extractor(bt, root, source, filename), nil
}

// languageExtractor is a function that walks the AST for a specific language
// and extracts public symbols.
type languageExtractor func(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol

// getLanguageExtractor returns the extraction function for a language name.
func getLanguageExtractor(langName string) languageExtractor {
	switch langName {
	case "go":
		return extractGo
	case "python":
		return extractPython
	case "typescript", "tsx":
		return extractTypeScript
	case "javascript":
		return extractJavaScript
	case "rust":
		return extractRust
	case "java":
		return extractJava
	default:
		return nil
	}
}

// getAllLanguageExtractor returns extractors that include unexported/private symbols.
func getAllLanguageExtractor(langName string) languageExtractor {
	switch langName {
	case "go":
		return extractGoAll
	case "python":
		return extractPython // Python: _ prefix filtering is for public API; for call graph, include all
	case "typescript", "tsx":
		return extractTypeScript
	case "javascript":
		return extractJavaScript
	case "rust":
		return extractRust
	case "java":
		return extractJava
	default:
		return nil
	}
}

// --- Go Extractor ---

func extractGo(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "function_declaration":
			if sym := extractGoFunc(bt, node, source, filename); sym != nil && sym.Exported {
				symbols = append(symbols, *sym)
			}
		case "method_declaration":
			if sym := extractGoMethod(bt, node, source, filename); sym != nil && sym.Exported {
				symbols = append(symbols, *sym)
			}
		case "type_declaration":
			specs := collectNamedChildren(bt, node, "type_spec")
			for _, spec := range specs {
				if sym := extractGoTypeSpec(bt, spec, source, filename); sym != nil && sym.Exported {
					symbols = append(symbols, *sym)
				}
			}
		case "const_declaration", "var_declaration":
			kind := SymbolConst
			if nodeType == "var_declaration" {
				kind = SymbolVar
			}
			specs := collectNamedChildren(bt, node, "const_spec")
			if len(specs) == 0 {
				specs = collectNamedChildren(bt, node, "var_spec")
			}
			for _, spec := range specs {
				if sym := extractGoVarConstSpec(bt, spec, source, filename, kind); sym != nil && sym.Exported {
					symbols = append(symbols, *sym)
				}
			}
		}
	})
	return symbols
}

// extractGoAll extracts ALL Go declarations (exported + unexported).
// Used by the Call Graph Builder for complete call edge resolution.
func extractGoAll(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "function_declaration":
			if sym := extractGoFuncAll(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "method_declaration":
			if sym := extractGoMethodAll(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "type_declaration":
			specs := collectNamedChildren(bt, node, "type_spec")
			for _, spec := range specs {
				if sym := extractGoTypeSpecAll(bt, spec, source, filename); sym != nil {
					symbols = append(symbols, *sym)
				}
			}
		case "const_declaration", "var_declaration":
			kind := SymbolConst
			if nodeType == "var_declaration" {
				kind = SymbolVar
			}
			specs := collectNamedChildren(bt, node, "const_spec")
			if len(specs) == 0 {
				specs = collectNamedChildren(bt, node, "var_spec")
			}
			for _, spec := range specs {
				if sym := extractGoVarConstSpecAll(bt, spec, source, filename, kind); sym != nil {
					symbols = append(symbols, *sym)
				}
			}
		}
	})
	return symbols
}

func extractGoFunc(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if !isGoExported(name) {
		return nil
	}
	return &Symbol{
		Name:       name,
		Kind:       SymbolFunc,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   true,
	}
}

func extractGoMethod(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if !isGoExported(name) {
		return nil
	}

	// Build receiver-qualified signature
	sig := extractNodeSignature(node, source)

	return &Symbol{
		Name:       name,
		Kind:       SymbolMethod,
		Signature:  sig,
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   true,
	}
}

func extractGoTypeSpec(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if !isGoExported(name) {
		return nil
	}

	kind := SymbolType
	typeNode := bt.ChildByField(node, "type")
	if typeNode != nil {
		switch bt.NodeType(typeNode) {
		case "interface_type":
			kind = SymbolInterface
		case "struct_type":
			kind = SymbolType
		}
	}

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   true,
	}
}

func extractGoVarConstSpec(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string, kind SymbolKind) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		// const/var specs can have multiple names; try first named child
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child != nil && bt.NodeType(child) == "identifier" {
				nameNode = child
				break
			}
		}
	}
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if !isGoExported(name) {
		return nil
	}

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   true,
	}
}

func isGoExported(name string) bool {
	if name == "" {
		return false
	}
	r := rune(name[0])
	return unicode.IsUpper(r)
}

// --- Go All-Symbol Extractors (include unexported) ---

func extractGoFuncAll(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	return &Symbol{
		Name:       name,
		Kind:       SymbolFunc,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   isGoExported(name),
	}
}

func extractGoMethodAll(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	sig := extractNodeSignature(node, source)
	return &Symbol{
		Name:       name,
		Kind:       SymbolMethod,
		Signature:  sig,
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   isGoExported(name),
	}
}

func extractGoTypeSpecAll(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)

	kind := SymbolType
	typeNode := bt.ChildByField(node, "type")
	if typeNode != nil {
		switch bt.NodeType(typeNode) {
		case "interface_type":
			kind = SymbolInterface
		case "struct_type":
			kind = SymbolType
		}
	}

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   isGoExported(name),
	}
}

func extractGoVarConstSpecAll(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string, kind SymbolKind) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		for i := 0; i < node.ChildCount(); i++ {
			child := node.Child(i)
			if child != nil && bt.NodeType(child) == "identifier" {
				nameNode = child
				break
			}
		}
	}
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)

	return &Symbol{
		Name:       name,
		Kind:       kind,
		Signature:  extractNodeSignature(node, source),
		DocComment: extractDocComment(bt, node, source),
		File:       filename,
		Line:       int(node.StartPoint().Row) + 1,
		EndLine:    int(node.EndPoint().Row) + 1,
		Exported:   isGoExported(name),
	}
}

// --- Python Extractor ---

func extractPython(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "function_definition":
			if sym := extractPythonFunc(bt, node, source, filename); sym != nil && sym.Exported {
				symbols = append(symbols, *sym)
			}
		case "class_definition":
			if sym := extractPythonClass(bt, node, source, filename); sym != nil && sym.Exported {
				symbols = append(symbols, *sym)
			}
		}
	})
	return symbols
}

func extractPythonFunc(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if strings.HasPrefix(name, "_") {
		return nil
	}
	return &Symbol{
		Name:      name,
		Kind:      SymbolFunc,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractPythonClass(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	name := bt.NodeText(nameNode)
	if strings.HasPrefix(name, "_") {
		return nil
	}
	return &Symbol{
		Name:      name,
		Kind:      SymbolClass,
		Signature: "class " + name,
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

// --- TypeScript / JavaScript Extractor ---

func extractTypeScript(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildren(bt, root, func(node *gotreesitter.Node) {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "export_statement":
			// Walk export children for the actual declaration
			for i := 0; i < node.ChildCount(); i++ {
				child := node.Child(i)
				if child == nil {
					continue
				}
				childType := bt.NodeType(child)
				switch childType {
				case "function_declaration":
					if sym := extractTSFunc(bt, child, source, filename); sym != nil {
						symbols = append(symbols, *sym)
					}
				case "class_declaration":
					if sym := extractTSClass(bt, child, source, filename); sym != nil {
						symbols = append(symbols, *sym)
					}
				case "interface_declaration":
					if sym := extractTSInterface(bt, child, source, filename); sym != nil {
						symbols = append(symbols, *sym)
					}
				case "type_alias_declaration":
					if sym := extractTSTypeAlias(bt, child, source, filename); sym != nil {
						symbols = append(symbols, *sym)
					}
				case "lexical_declaration":
					// export const/let
					syms := extractTSLexical(bt, child, source, filename)
					symbols = append(symbols, syms...)
				}
			}
		}
	})
	return symbols
}

func extractJavaScript(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	// JavaScript uses the same extraction as TypeScript minus interface/type
	return extractTypeScript(bt, root, source, filename)
}

func extractTSFunc(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolFunc,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractTSClass(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolClass,
		Signature: "class " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractTSInterface(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolInterface,
		Signature: "interface " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractTSTypeAlias(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolType,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractTSLexical(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child == nil {
			continue
		}
		if bt.NodeType(child) == "variable_declarator" {
			nameNode := bt.ChildByField(child, "name")
			if nameNode != nil {
				symbols = append(symbols, Symbol{
					Name:      bt.NodeText(nameNode),
					Kind:      SymbolConst,
					Signature: extractNodeSignature(node, source),
					File:      filename,
					Line:      int(node.StartPoint().Row) + 1,
					Exported:  true,
				})
			}
		}
	}
	return symbols
}

// --- Rust Extractor ---

func extractRust(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildrenRecursive(bt, root, func(node *gotreesitter.Node) bool {
		nodeType := bt.NodeType(node)
		if !isRustPub(bt, node) {
			// Only extract pub items — but still recurse into modules
			return nodeType == "mod_item" || nodeType == "source_file" || nodeType == "impl_item"
		}
		switch nodeType {
		case "function_item":
			if sym := extractRustFunc(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "struct_item":
			if sym := extractRustStruct(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "trait_item":
			if sym := extractRustTrait(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "enum_item":
			if sym := extractRustEnum(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		case "type_item":
			if sym := extractRustTypeAlias(bt, node, source, filename); sym != nil {
				symbols = append(symbols, *sym)
			}
		}
		return false
	})
	return symbols
}

func isRustPub(bt *gotreesitter.BoundTree, node *gotreesitter.Node) bool {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && bt.NodeType(child) == "visibility_modifier" {
			return true
		}
	}
	return false
}

func extractRustFunc(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolFunc,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractRustStruct(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolType,
		Signature: "pub struct " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractRustTrait(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolTrait,
		Signature: "pub trait " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractRustEnum(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolEnum,
		Signature: "pub enum " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractRustTypeAlias(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolType,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

// --- Java Extractor ---

func extractJava(bt *gotreesitter.BoundTree, root *gotreesitter.Node, source []byte, filename string) []Symbol {
	var symbols []Symbol
	walkChildrenRecursive(bt, root, func(node *gotreesitter.Node) bool {
		nodeType := bt.NodeType(node)
		switch nodeType {
		case "class_declaration":
			if isJavaPublic(bt, node) {
				if sym := extractJavaClass(bt, node, source, filename); sym != nil {
					symbols = append(symbols, *sym)
				}
			}
			return true // recurse into class body
		case "interface_declaration":
			if isJavaPublic(bt, node) {
				if sym := extractJavaInterface(bt, node, source, filename); sym != nil {
					symbols = append(symbols, *sym)
				}
			}
			return true // recurse into interface body
		case "method_declaration":
			if isJavaPublic(bt, node) {
				if sym := extractJavaMethod(bt, node, source, filename); sym != nil {
					symbols = append(symbols, *sym)
				}
			}
			return false
		case "class_body", "interface_body":
			return true // recurse into bodies
		default:
			return false
		}
	})
	return symbols
}

func isJavaPublic(bt *gotreesitter.BoundTree, node *gotreesitter.Node) bool {
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && bt.NodeType(child) == "modifiers" {
			modText := bt.NodeText(child)
			return strings.Contains(modText, "public")
		}
	}
	return false
}

func extractJavaClass(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolClass,
		Signature: "public class " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractJavaInterface(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolInterface,
		Signature: "public interface " + bt.NodeText(nameNode),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

func extractJavaMethod(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte, filename string) *Symbol {
	nameNode := bt.ChildByField(node, "name")
	if nameNode == nil {
		return nil
	}
	return &Symbol{
		Name:      bt.NodeText(nameNode),
		Kind:      SymbolMethod,
		Signature: extractNodeSignature(node, source),
		File:      filename,
		Line:      int(node.StartPoint().Row) + 1,
		Exported:  true,
	}
}

// --- Helpers ---

// extractDocComment looks for comment nodes immediately preceding the given node
// and extracts the first line of the doc comment block (stripping the // prefix).
// Handles consecutive comment lines (multi-line doc comments) by walking backwards.
func extractDocComment(bt *gotreesitter.BoundTree, node *gotreesitter.Node, source []byte) string {
	// For type_spec inside type_declaration, look at the type_declaration's siblings
	targetNode := node
	parent := node.Parent()
	if parent != nil && bt.NodeType(parent) == "type_declaration" {
		targetNode = parent
		parent = parent.Parent()
	}
	if parent == nil {
		return ""
	}

	// Find the index of this node in its parent's children
	var nodeIdx int = -1
	for i := 0; i < parent.ChildCount(); i++ {
		child := parent.Child(i)
		if child != nil && child.StartByte() == targetNode.StartByte() && child.EndByte() == targetNode.EndByte() {
			nodeIdx = i
			break
		}
	}
	if nodeIdx <= 0 {
		return ""
	}

	// Walk backwards through consecutive comment siblings
	var commentNodes []*gotreesitter.Node
	for i := nodeIdx - 1; i >= 0; i-- {
		sibling := parent.Child(i)
		if sibling == nil {
			break
		}
		if bt.NodeType(sibling) != "comment" {
			break
		}
		commentNodes = append(commentNodes, sibling)
	}
	if len(commentNodes) == 0 {
		return ""
	}

	// The last comment in our reversed list is the first comment line
	firstComment := commentNodes[len(commentNodes)-1]

	// Verify the last comment (closest to the node) ends on the line before the node
	lastComment := commentNodes[0]
	commentEndLine := int(lastComment.EndPoint().Row)
	nodeStartLine := int(targetNode.StartPoint().Row)
	if commentEndLine+1 != nodeStartLine {
		return "" // not immediately preceding
	}

	commentText := string(source[firstComment.StartByte():firstComment.EndByte()])
	firstLine := strings.TrimSpace(commentText)
	if strings.HasPrefix(firstLine, "//") {
		return strings.TrimSpace(firstLine[2:])
	}
	return firstLine
}

// walkChildren iterates over all top-level named children of root.
func walkChildren(bt *gotreesitter.BoundTree, root *gotreesitter.Node, fn func(*gotreesitter.Node)) {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child != nil {
			fn(child)
		}
	}
}

// walkChildrenRecursive walks nodes. The callback returns true if children should be visited.
func walkChildrenRecursive(bt *gotreesitter.BoundTree, root *gotreesitter.Node, fn func(*gotreesitter.Node) bool) {
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child == nil {
			continue
		}
		if fn(child) {
			walkChildrenRecursive(bt, child, fn)
		}
	}
}

// collectNamedChildren collects children matching a specific node type.
func collectNamedChildren(bt *gotreesitter.BoundTree, node *gotreesitter.Node, nodeType string) []*gotreesitter.Node {
	var result []*gotreesitter.Node
	for i := 0; i < node.ChildCount(); i++ {
		child := node.Child(i)
		if child != nil && bt.NodeType(child) == nodeType {
			result = append(result, child)
		}
	}
	return result
}

// extractNodeSignature extracts the first line of a node's source text
// as a signature. For multi-line declarations, captures up to the opening brace.
func extractNodeSignature(node *gotreesitter.Node, source []byte) string {
	start := node.StartByte()
	end := node.EndByte()
	if int(end) > len(source) {
		end = uint32(len(source))
	}
	text := string(source[start:end])

	// Find the first opening brace and truncate there
	if idx := strings.Index(text, "{"); idx >= 0 {
		text = strings.TrimSpace(text[:idx])
	}

	// Collapse multi-line signatures into a single line
	lines := strings.Split(text, "\n")
	if len(lines) > 1 {
		var parts []string
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		text = strings.Join(parts, " ")
	}

	return text
}
