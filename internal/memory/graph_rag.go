package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type NeighborhoodParams struct {
	NodeTypes     []string
	EdgeTypes     []string
	MinNodeWeight float64
	MinEdgeWeight float64
	Direction     string // "incoming", "outgoing", "undirected" (default)
	Limit         int    // max total nodes returned
}

type NeighborhoodOption func(*NeighborhoodParams)

func WithNodeTypes(types []string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.NodeTypes = types
	}
}

func WithEdgeTypes(types []string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.EdgeTypes = types
	}
}

func WithMinNodeWeight(w float64) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.MinNodeWeight = w
	}
}

func WithMinEdgeWeight(w float64) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.MinEdgeWeight = w
	}
}

func WithDirection(dir string) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.Direction = dir
	}
}

func WithLimit(limit int) NeighborhoodOption {
	return func(p *NeighborhoodParams) {
		p.Limit = limit
	}
}

// GetEntityNeighborhood traverses connected nodes up to maxHops (Graph-RAG traversal) with customizable filters.
func (sdb *SqliteDatabase) GetEntityNeighborhood(entityID string, maxHops int, opts ...NeighborhoodOption) KGSubGraph {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return KGSubGraph{}
	}

	nodes, err := sdb.getNodesMapLocked()
	if err != nil {
		return KGSubGraph{}
	}
	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		return KGSubGraph{}
	}

	return sdb.getEntityNeighborhoodLocked(entityID, maxHops, nodes, edges, opts...)
}

func (sdb *SqliteDatabase) getEntityNeighborhoodLocked(
	entityID string,
	maxHops int,
	nodes map[string]KGNode,
	edges []KGEdge,
	opts ...NeighborhoodOption,
) KGSubGraph {
	p := &NeighborhoodParams{
		Direction: "undirected",
	}
	for _, opt := range opts {
		opt(p)
	}

	nodeTypeMap := make(map[string]bool)
	for _, t := range p.NodeTypes {
		nodeTypeMap[t] = true
	}

	edgeTypeMap := make(map[string]bool)
	for _, t := range p.EdgeTypes {
		edgeTypeMap[t] = true
	}

	visited := map[string]bool{entityID: true}
	var allNodes []KGNode
	var allEdges []KGEdge

	// Add start node if it exists
	if startNode, exists := nodes[entityID]; exists {
		if len(nodeTypeMap) > 0 && !nodeTypeMap[startNode.NodeType] {
			return KGSubGraph{}
		}
		if p.MinNodeWeight > 0 && startNode.Weight < p.MinNodeWeight {
			return KGSubGraph{}
		}
		allNodes = append(allNodes, startNode)
	} else {
		return KGSubGraph{}
	}

	if p.Limit > 0 && len(allNodes) >= p.Limit {
		return KGSubGraph{Nodes: allNodes, Edges: []KGEdge{}}
	}

	frontier := []string{entityID}

	for hop := 0; hop < maxHops && len(frontier) > 0; hop++ {
		var nextFrontier []string
		reachedLimit := false

		for _, nodeID := range frontier {
			// Find edges connected to nodeID
			for _, edge := range edges {
				if p.MinEdgeWeight > 0 && edge.Weight < p.MinEdgeWeight {
					continue
				}
				if len(edgeTypeMap) > 0 && !edgeTypeMap[edge.EdgeType] {
					continue
				}

				// Identify neighbor node and validate direction
				var neighborID string
				var isValidDir bool
				if edge.SourceID == nodeID {
					neighborID = edge.TargetID
					isValidDir = (p.Direction == "outgoing" || p.Direction == "undirected" || p.Direction == "")
				} else if edge.TargetID == nodeID {
					neighborID = edge.SourceID
					isValidDir = (p.Direction == "incoming" || p.Direction == "undirected" || p.Direction == "")
				}

				if !isValidDir {
					continue
				}

				// Fetch neighbor node
				neighborNode, exists := nodes[neighborID]
				if !exists {
					continue
				}

				// Filter neighbor node
				if len(nodeTypeMap) > 0 && !nodeTypeMap[neighborNode.NodeType] {
					continue
				}
				if p.MinNodeWeight > 0 && neighborNode.Weight < p.MinNodeWeight {
					continue
				}

				// Append edge if not already added
				alreadyAdded := false
				for _, existingEdge := range allEdges {
					if existingEdge.ID == edge.ID {
						alreadyAdded = true
						break
					}
				}
				if !alreadyAdded {
					allEdges = append(allEdges, edge)
				}

				// Visit neighbor
				if !visited[neighborID] {
					visited[neighborID] = true
					nextFrontier = append(nextFrontier, neighborID)
					allNodes = append(allNodes, neighborNode)
					if p.Limit > 0 && len(allNodes) >= p.Limit {
						reachedLimit = true
						break
					}
				}
			}
			if reachedLimit {
				break
			}
		}
		if reachedLimit {
			break
		}
		frontier = nextFrontier
	}

	// Filter edges to only include those whose source and target are in the final nodes set
	finalNodeIDs := make(map[string]bool)
	for _, n := range allNodes {
		finalNodeIDs[n.ID] = true
	}
	var finalEdges []KGEdge
	for _, e := range allEdges {
		if finalNodeIDs[e.SourceID] && finalNodeIDs[e.TargetID] {
			finalEdges = append(finalEdges, e)
		}
	}

	return KGSubGraph{Nodes: allNodes, Edges: finalEdges}
}

// GetGraphRAGContext scans a natural language prompt, matches active entity Names or IDs,
// traverses up to 2-hop neighborhoods, and outputs a formatted Markdown Graph-RAG context.
// maxChars controls the maximum character length of the output (0 = unlimited).
func (sdb *SqliteDatabase) GetGraphRAGContext(prompt string, maxChars ...int) string {
	sdb.mutex.RLock()
	defer sdb.mutex.RUnlock()

	if sdb.db == nil {
		return ""
	}

	nodes, err := sdb.getNodesMapLocked()
	if err != nil || len(nodes) == 0 {
		return ""
	}
	edges, err := sdb.getEdgesSliceLocked()
	if err != nil {
		return ""
	}

	// 1. Identify matched nodes in the prompt via Hybrid Vector Search
	var matchedIDs []string
	matchedIDsMap := make(map[string]bool)

	// First, check exact word matches (FTS5 / literal candidate pool fallback)
	for id, node := range nodes {
		if isWordMatch(prompt, id) || isWordMatch(prompt, node.Name) {
			matchedIDs = append(matchedIDs, id)
			matchedIDsMap[id] = true
		}
	}

	// Second, if EmbeddingEngine is available, calculate semantic similarity for candidates
	if sdb.EmbeddingEngine != nil {
		promptVec, err := sdb.EmbeddingEngine.Embed(context.Background(), prompt)
		if err == nil {
			for id, node := range nodes {
				if matchedIDsMap[id] {
					continue
				}
				if len(node.Embedding) > 0 {
					sim := sdb.EmbeddingEngine.CosineSimilarity(promptVec, node.Embedding)
					// Threshold of 0.30 indicates strong semantic alignment for sparse vectors
					if sim >= 0.30 {
						matchedIDs = append(matchedIDs, id)
						matchedIDsMap[id] = true
					}
				}
			}
		}
	}

	if len(matchedIDs) == 0 {
		return ""
	}

	// 2. Traverse neighborhood (2 hops) for all matched nodes and deduplicate
	dedupNodes := make(map[string]KGNode)
	dedupEdges := make(map[string]KGEdge)

	for _, matchedID := range matchedIDs {
		sub := sdb.getEntityNeighborhoodLocked(matchedID, 2, nodes, edges)
		for _, n := range sub.Nodes {
			dedupNodes[n.ID] = n
		}
		for _, e := range sub.Edges {
			dedupEdges[e.ID] = e
		}
	}

	// Resolve the effective character limit
	charLimit := 0
	if len(maxChars) > 0 {
		charLimit = maxChars[0]
	}

	// 3. Format into Markdown (with optional truncation)
	return sdb.formatRAGContext(dedupNodes, dedupEdges, charLimit)
}

// formatRAGContext builds the Markdown output, applying weight-based truncation if charLimit > 0.
func (sdb *SqliteDatabase) formatRAGContext(dedupNodes map[string]KGNode, dedupEdges map[string]KGEdge, charLimit int) string {
	totalEntityCount := len(dedupNodes)

	// Sort node IDs by weight descending (highest relevance first), breaking ties by ID
	var sortedNodeIDs []string
	for id := range dedupNodes {
		sortedNodeIDs = append(sortedNodeIDs, id)
	}
	sort.Slice(sortedNodeIDs, func(i, j int) bool {
		wi := dedupNodes[sortedNodeIDs[i]].Weight
		wj := dedupNodes[sortedNodeIDs[j]].Weight
		if wi != wj {
			return wi > wj // higher weight first
		}
		return sortedNodeIDs[i] < sortedNodeIDs[j] // alphabetical tie-break
	})

	// Try rendering with all entities first; if over limit, progressively drop lowest-weight entities
	for {
		output := sdb.renderRAGMarkdown(sortedNodeIDs, dedupNodes, dedupEdges, charLimit > 0 && len(sortedNodeIDs) < totalEntityCount, totalEntityCount)
		if charLimit <= 0 || len(output) <= charLimit || len(sortedNodeIDs) <= 1 {
			return output
		}
		// Drop the last entity (lowest weight) and retry
		sortedNodeIDs = sortedNodeIDs[:len(sortedNodeIDs)-1]
	}
}

// renderRAGMarkdown produces the final Markdown string for the given entity subset.
func (sdb *SqliteDatabase) renderRAGMarkdown(sortedNodeIDs []string, dedupNodes map[string]KGNode, dedupEdges map[string]KGEdge, truncated bool, totalEntityCount int) string {
	var sb strings.Builder
	sb.WriteString("### RELATIONAL KNOWLEDGE GRAPH CONTEXT (Graph-RAG)\n")
	sb.WriteString("Based on active entities detected in your request, the following local sub-graph has been retrieved:\n\n")

	sb.WriteString("#### Connected Entities\n")
	sb.WriteString("| ID | Type | Name | Weight | Source | Metadata |\n")
	sb.WriteString("| --- | --- | --- | --- | --- | --- |\n")

	retainedIDs := make(map[string]bool)
	for _, id := range sortedNodeIDs {
		retainedIDs[id] = true
		n := dedupNodes[id]
		metaBytes, _ := json.Marshal(n.Metadata)
		metaStr := string(metaBytes)
		if metaStr == "" {
			metaStr = "{}"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %.2f | %s | %s |\n", n.ID, n.NodeType, n.Name, n.Weight, n.Source, metaStr))
	}
	sb.WriteString("\n")

	// Filter edges to only include those whose source AND target are in the retained set
	sb.WriteString("#### Relationships\n")
	var validEdges []KGEdge
	for _, e := range dedupEdges {
		if retainedIDs[e.SourceID] && retainedIDs[e.TargetID] {
			validEdges = append(validEdges, e)
		}
	}

	if len(validEdges) == 0 {
		sb.WriteString("No active relationships within the retrieved neighborhood.\n")
	} else {
		var sortedEdgeIDs []string
		edgeMap := make(map[string]KGEdge)
		for _, e := range validEdges {
			sortedEdgeIDs = append(sortedEdgeIDs, e.ID)
			edgeMap[e.ID] = e
		}
		sort.Strings(sortedEdgeIDs)

		for _, id := range sortedEdgeIDs {
			e := edgeMap[id]
			srcName := e.SourceID
			if sn, exists := dedupNodes[e.SourceID]; exists {
				srcName = sn.Name
			}
			tgtName := e.TargetID
			if tn, exists := dedupNodes[e.TargetID]; exists {
				tgtName = tn.Name
			}
			metaBytes, _ := json.Marshal(e.Metadata)
			metaStr := string(metaBytes)
			if metaStr == "" {
				metaStr = "{}"
			}
			sb.WriteString(fmt.Sprintf("- **%s** (`%s`) --[%s (Weight: %.2f)]--> **%s** (`%s`) | Metadata: %s\n",
				srcName, e.SourceID, e.EdgeType, e.Weight, tgtName, e.TargetID, metaStr))
		}
	}

	if truncated {
		sb.WriteString(fmt.Sprintf("\n> ⚠️ Context truncated: showing top %d of %d entities by relevance weight.\n", len(sortedNodeIDs), totalEntityCount))
	}

	return sb.String()
}

// isWordMatch helper to perform a precise, case-insensitive word-boundary search
func isWordMatch(text, word string) bool {
	if len(word) == 0 {
		return false
	}
	textLower := strings.ToLower(text)
	wordLower := strings.ToLower(word)

	start := 0
	for {
		idx := strings.Index(textLower[start:], wordLower)
		if idx == -1 {
			return false
		}
		pos := start + idx

		// Check boundary before
		beforeWord := true
		if pos > 0 {
			rBefore := rune(textLower[pos-1])
			if unicode.IsLetter(rBefore) || unicode.IsDigit(rBefore) || rBefore == '_' {
				beforeWord = false
			}
		}

		// Check boundary after
		afterWord := true
		endPos := pos + len(wordLower)
		if endPos < len(textLower) {
			rAfter := rune(textLower[endPos])
			if unicode.IsLetter(rAfter) || unicode.IsDigit(rAfter) || rAfter == '_' {
				afterWord = false
			}
		}

		if beforeWord && afterWord {
			return true
		}

		start = pos + 1
	}
}
