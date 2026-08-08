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
// sql_cached_data operates on materialized tables in the ephemeral query DB. Without this enrichment,
// the model generates filters like `.dataProfile.columns[]` which fail with
// "Cannot index array with string".
//
// This function bridges the gap by showing the model what the cached data actually
// looks like before it generates a SQL query.
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
The cached data for cacheId '%s' is stored in a SQL table named '%s'.
Query it using standard SQL via the sql_cached_data tool.

Example SQL patterns:
- Count records: SELECT COUNT(*) FROM %s
- Group by field: SELECT FieldName, COUNT(*) as cnt FROM %s GROUP BY FieldName ORDER BY cnt DESC
- Filter: SELECT * FROM %s WHERE FieldName = 'value'
- Unique values: SELECT DISTINCT FieldName FROM %s

Schema introspection result:
%s`, match, match, match, match, match, match, schema)

	// FM2: Append per-column enrichment (cardinality, non-null counts, top values)
	// from the ephemeral query DB. Helps the 4B model understand data distribution
	// before writing queries — prevents wrong column references and lazy LIMIT queries.
	if qdb := cache.QueryDB(); qdb != nil {
		if columnEnrichments, err := cache.EnrichSchema(qdb, match); err == nil && len(columnEnrichments) > 0 {
			enrichment += "\n\n## COLUMN STATISTICS\n"
			for _, col := range columnEnrichments {
				enrichment += fmt.Sprintf("- **%s** (%s): %d rows, %d non-null, %d unique values",
					col.ColumnName, col.DataType, col.TotalCount, col.NonNullCount, col.Cardinality)
				if len(col.TopValues) > 0 {
					topStrs := make([]string, 0, len(col.TopValues))
					for _, tv := range col.TopValues {
						topStrs = append(topStrs, fmt.Sprintf("%q (%d)", tv.Value, tv.Count))
					}
					enrichment += fmt.Sprintf(" | Top: %s", strings.Join(topStrs, ", "))
				}
				enrichment += "\n"
			}
			fmt.Fprintf(os.Stderr, "[Executor] Enriched cache bridge with column statistics for %s (%d columns)\n", match, len(columnEnrichments))
		}
	}

	fmt.Fprintf(os.Stderr, "[Executor] Enriched cache bridge context with introspect_cache for %s\n", match)
	return accumulatedCtx + enrichment
}

// cacheToolNames are the tools that indicate cache access capability.
var cacheToolNames = []string{"introspect_cache", "sql_cached_data"}

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
		Action: "sql_cached_data",
		Instructions: "Query the cached tabular data from the upstream node's Data Profile. " +
			"Use the cacheId from the upstream output. " +
			"Execute: SELECT * FROM cache_<id> LIMIT 5 to return a representative sample.",
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
