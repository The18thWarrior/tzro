package ast

import gotreesitter "github.com/odvcencio/gotreesitter"

// cCallableName follows only the declarator spine. Parameter identifiers and
// nested callback signatures cannot become the enclosing callable's name.
func cCallableName(bt *gotreesitter.BoundTree, declaration *gotreesitter.Node) *gotreesitter.Node {
	node := bt.ChildByField(declaration, "declarator")
	for depth := 0; node != nil && depth < 32; depth++ {
		switch bt.NodeType(node) {
		case "identifier", "field_identifier", "qualified_identifier", "operator_name", "destructor_name":
			return node
		}
		if next := bt.ChildByField(node, "declarator"); next != nil {
			node = next
			continue
		}
		// Parentheses contain one declarator. Do not search arbitrary children,
		// which could include names from parameters, attributes or return types.
		if bt.NodeType(node) == "parenthesized_declarator" && node.NamedChildCount() == 1 {
			node = node.NamedChild(0)
			continue
		}
		return nil
	}
	return nil
}
