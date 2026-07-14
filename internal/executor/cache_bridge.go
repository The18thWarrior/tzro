package executor

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"tzro/internal/cache"
	"tzro/internal/compiler"
	"tzro/internal/memory"
)

// enrichCacheBridgeContext extracts the cacheId from the accumulated context
// or interpolated prompt, calls introspect_cache to get the actual data schema,
// and returns an enriched context string that tells the model the real data shape.
//
// Problem: The upstream read_file output contains a `dataProfile` envelope, but
// jq_cached_data operates on a flat JSON array of records. Without this enrichment,
// the model generates filters like `.dataProfile.columns[]` which fail with
// "Cannot index array with string".
//
// This function bridges the gap by showing the model what the cached data actually
// looks like before it generates a jq filter.
func enrichCacheBridgeContext(ctx context.Context, accumulatedCtx, interpolatedPrompt string) string {
	// Extract cacheId from accumulated context or interpolated prompt
	cacheIdRe := regexp.MustCompile(`cache_\d{10,}`)
	combined := accumulatedCtx + "\n" + interpolatedPrompt

	match := cacheIdRe.FindString(combined)
	if match == "" {
		return accumulatedCtx
	}

	// Call introspect_cache to get the actual schema
	schema := cache.DefaultStore.Introspect(ctx, match)
	if strings.HasPrefix(schema, "Error:") || schema == "" {
		return accumulatedCtx
	}

	// Build enrichment block
	enrichment := fmt.Sprintf(`

## CACHE DATA SCHEMA (from introspect_cache)
The cached data for cacheId '%s' is a **flat JSON array of record objects**.
It is NOT wrapped in a dataProfile envelope. Do NOT use .dataProfile in jq filters.

Correct jq filter patterns:
- Count records: '. | length'
- Group by field: 'group_by(.FieldName) | map({key: .[0].FieldName, count: length})'
- Filter: '[.[] | select(.FieldName == "value")]'
- Unique values: '[.[].FieldName] | unique'

Schema introspection result:
%s`, match, schema)

	fmt.Fprintf(os.Stderr, "[Executor] Enriched cache bridge context with introspect_cache for %s\n", match)
	return accumulatedCtx + enrichment
}


// cacheToolNames are the tools that indicate cache access capability.
var cacheToolNames = []string{"introspect_cache", "read_cached_data", "jq_cached_data"}

// maybeInjectCacheBridge checks if a completed node's output contains cacheId
// and dataProfile markers. If so, and no downstream node already has cache tools,
// and no compile-time cache bridge exists, it spawns a cache bridge node.
// Spec §4.3 — Runtime Cache Bridge injection.
func (e *ExecutionEngine) maybeInjectCacheBridge(
	graph *compiler.ExecutionGraph,
	nodeIndex map[string]*compiler.GraphNode,
	node *compiler.GraphNode,
	nodeID string,
) {
	// Quick check: does the output contain profiler markers?
	output := node.Output
	if !strings.Contains(output, "cacheId") || !strings.Contains(output, "dataProfile") {
		return
	}

	// Check if a compile-time bridge already exists for this node
	bridgeID := "cache_bridge_" + nodeID
	if _, exists := nodeIndex[bridgeID]; exists {
		return // Compiler already handled it
	}

	// Check if any downstream node already has cache tools
	for _, edge := range graph.Edges {
		if edge.SourceID == nodeID {
			if downstream, ok := nodeIndex[edge.TargetID]; ok {
				if nodeHasCacheTools(downstream) {
					return // Downstream already equipped
				}
			}
		}
	}

	// Inject cache bridge node
	bridgeNode := compiler.GraphNode{
		ID:     bridgeID,
		Type:   "action",
		Action: "jq_cached_data",
		Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
			"Use the cacheId from the upstream output to access the data via jq_cached_data. " +
			"Return the most relevant subset of data for the downstream task.",
		AllowedTools:        cacheToolNames,
		Status:              "pending",
		ActivationThreshold: 0.0,
	}

	spawnErr := ApplySpawn(graph, nodeID, bridgeNode)
	if spawnErr != nil {
		fmt.Fprintf(os.Stderr, "[Executor/RQ] Cache bridge spawn failed for %s: %v\n", nodeID, spawnErr)
		return
	}

	// Register in node index and persist state (best-effort — may not be initialized in tests)
	nodeIndex[bridgeID] = &graph.Nodes[len(graph.Nodes)-1]
	func() {
		defer func() { recover() }() // Guard against uninitialized DB in tests
		if memory.DB != nil {
			_ = memory.DB.SetNodeState(graph.TaskID, bridgeID, "pending", "")
		}
	}()

	fmt.Fprintf(os.Stderr, "[Executor/RQ] Runtime cache bridge injected: %s after %s\n", bridgeID, nodeID)
	e.getPublisher().PublishEvent("cache_bridge_injected", graph.TaskID, bridgeID,
		fmt.Sprintf("Runtime cache bridge injected after %s (output contained dataProfile)", nodeID))
}

// nodeHasCacheTools returns true if the node has any cache-related tools.
func nodeHasCacheTools(node *compiler.GraphNode) bool {
	for _, tool := range node.AllowedTools {
		for _, ct := range cacheToolNames {
			if tool == ct {
				return true
			}
		}
	}
	return false
}
