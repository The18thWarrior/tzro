package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"tzro/internal/compiler"
	"tzro/internal/config"
	"tzro/internal/index"
	"tzro/internal/inference"
)

// TryIndexPreflight queries the Repository Pre-Index for the probe's goal.
// If high-confidence matches are found, it packs the context and returns (true, packedContent, nil).
// ADR-0086: Repository Pre-Index, Dual-Plane Indexing, and Context Budget Packing
func TryIndexPreflight(ctx context.Context, probeConfig *compiler.ProbeConfig, embedder index.Embedder) (bool, string, error) {
	if probeConfig == nil || probeConfig.Goal == "" {
		return false, "", nil
	}

	// Skip for web-only or cache-bridge analyze nodes
	if probeConfig.SourceHint == "web" || probeConfig.SourceHint == "cache" {
		return false, "", nil
	}

	idxStore := index.GetGlobalIndex()
	if idxStore == nil {
		return false, "", nil
	}

	if embedder == nil && inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.Status == "Active" {
		embedder = inference.GlobalEmbeddingSidecar
	}

	// Search pre-index
	results, err := idxStore.HybridSearch(ctx, probeConfig.Goal, embedder, 15)
	if err != nil || len(results) == 0 {
		return false, "", err
	}

	// Dynamic confidence check: RRF threshold floor
	const minRRFScore = 0.012 // Equivalent to top-30 rank in FTS or vector search
	topScore := results[0].Score
	if topScore < minRRFScore {
		return false, "", nil // Below confidence threshold — fall back to PhaseRunner/ThoughtChain
	}

	// Reserve-Ratio Packing: allocate 70% of the active context window for packed chunks
	ctxSize := config.GetContextSize()
	if ctxSize <= 0 {
		ctxSize = 8192
	}
	maxTokens := (ctxSize * 7) / 10

	packed := index.PackContextBudget(results, minRRFScore, maxTokens)
	if packed.ItemsCount == 0 || len(packed.Buffer) == 0 {
		return false, "", nil
	}

	return true, packed.Buffer, nil
}

// ApplyIndexPreflightToProbe attempts to promote a ProbeConfig to DirectSynthesis using the Repository Pre-Index.
func ApplyIndexPreflightToProbe(ctx context.Context, probeConfig *compiler.ProbeConfig, taskID, nodeID string) bool {
	promoted, content, err := TryIndexPreflight(ctx, probeConfig, nil)
	if err != nil || !promoted || content == "" {
		return false
	}

	// Write packed context to temporary context file for DirectSynthesis
	tmpDir := os.TempDir()
	ctxFile := filepath.Join(tmpDir, fmt.Sprintf("tzro_probe_%s_%s_index.md", taskID, nodeID))
	if err := os.WriteFile(ctxFile, []byte(content), 0644); err != nil {
		return false
	}

	probeConfig.DirectSynthesis = true
	probeConfig.ContextFile = ctxFile
	fmt.Fprintf(os.Stderr, "[Executor] Probe %s: Promoted to DirectSynthesis via Repository Pre-Index (%d items packed)\n", nodeID, len(content))
	return true
}
