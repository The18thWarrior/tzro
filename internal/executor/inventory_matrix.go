package executor

import (
	"context"
	"fmt"
	"strings"

	"tzro/internal/config"
	"tzro/internal/embeddings"
	"tzro/internal/inference"
)

// FormatInventoryMatrix formats extracted rows into a compact YAML-style tagged block representation.
func FormatInventoryMatrix(rows []InventoryRow) string {
	var b strings.Builder
	for _, r := range rows {
		if !r.Relevant {
			continue
		}
		b.WriteString("---\n")
		b.WriteString(fmt.Sprintf("file: %s\n", r.File))
		for k, v := range r.Fields {
			// Sanitize single-line representation
			cleanV := strings.ReplaceAll(v, "\n", " ")
			b.WriteString(fmt.Sprintf("%s: %s\n", k, cleanV))
		}
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

var inventoryPrototypes = []string{
	"Read all files and build an exhaustive consolidated decision log or documentation summary",
	"Explore the entire codebase repository structure and produce comprehensive architecture documentation",
	"Read all package definitions and write a complete repository README",
	"Summarize all ADRs and produce a complete index of all architectural decisions",
	"Index all components and generate complete system documentation",
}

var inventoryKeywords = []string{
	"all adr",
	"every adr",
	"decision log",
	"consolidated decision log",
	"consolidated log",
	"entire internal",
	"package index",
	"comprehensive readme",
	"complete package index",
	"all package",
	"all files",
	"across executor, compiler, inference",
}

// IsInventoryGoal detects if a task requires the multi-file Inventory Extractor pipeline.
func IsInventoryGoal(ctx context.Context, goal string) bool {
	lowerGoal := strings.ToLower(goal)

	// Check keyword heuristics first
	for _, kw := range inventoryKeywords {
		if strings.Contains(lowerGoal, kw) {
			return true
		}
	}

	threshold := config.GetInventoryIntentThreshold()
	if threshold <= 0 {
		threshold = 0.65
	}

	// Try neural embedding sidecar if available
	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		goalVec, err := inference.GlobalEmbeddingSidecar.Embed(ctx, goal)
		if err == nil && len(goalVec) > 0 {
			for _, proto := range inventoryPrototypes {
				protoVec, pErr := inference.GlobalEmbeddingSidecar.Embed(ctx, proto)
				if pErr == nil && len(protoVec) > 0 {
					sim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, protoVec)
					if float64(sim) >= threshold {
						return true
					}
				}
			}
		}
	} else {
		// Fallback to pure-Go bag of words cosine similarity
		for _, proto := range inventoryPrototypes {
			sim := embeddings.CosineSimilarity(goal, proto)
			if sim >= threshold {
				return true
			}
		}
	}

	return false
}
