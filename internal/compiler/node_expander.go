package compiler

// NodeExpander allows strategy-defined compilation rules to participate in
// graph expansion. The compiler calls Expand for each node type during
// ExpandToSCTGraph. When no expander is registered or when Expand returns
// nil, the compiler falls back to its built-in expansion logic.
//
// This interface is defined in the compiler package (not strategy) to avoid
// import cycles: strategy → compiler is the dependency direction.
//
// Implemented by the executor package, which has access to both the strategy
// registry and the compiler.
type NodeExpander interface {
	// Expand applies strategy-defined compilation rules to a node.
	// Returns nil to fall through to the compiler's built-in logic.
	Expand(node *GraphNode, graph *ExecutionGraph) (*NodeExpansionResult, error)
}

// NodeExpansionResult describes how the compiler should transform a node.
// Mirrors strategy.ExpansionResult but lives in the compiler package to
// avoid import cycles.
type NodeExpansionResult struct {
	// ReplacementNodes replaces the original node entirely.
	// When empty, the original node is kept.
	ReplacementNodes []GraphNode

	// AdditionalNodes are injected alongside the original node.
	AdditionalNodes []GraphNode

	// AdditionalEdges are new edges for the injected nodes.
	AdditionalEdges []GraphEdge

	// ModifiedNode is the original node with mutations applied.
	// When nil, the original node is used unchanged.
	ModifiedNode *GraphNode
}
