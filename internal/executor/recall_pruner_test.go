package executor

import (
	"context"
	"strings"
	"testing"
)

func TestPruneUpstreamOutput_SmallPassThrough(t *testing.T) {
	ctx := context.Background()
	small := "Short raw output under 2000 chars describing basic architecture."
	out, err := PruneUpstreamOutput(ctx, small, "architecture", 4000)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != small {
		t.Errorf("expected small output to pass through unchanged, got %q", out)
	}
}

func TestPruneUpstreamOutput_SemanticChunkAndKNN(t *testing.T) {
	ctx := context.Background()
	// Create a large multi-section output (>3000 chars) with a target needle section
	var sb strings.Builder
	sb.WriteString("# Project Structure\n\nIntroductory information...\n\n")
	for i := 0; i < 10; i++ {
		sb.WriteString("## Unrelated Package ")
		sb.WriteString(strings.Repeat("filler text about uninteresting components. ", 15))
		sb.WriteString("\n\n")
	}

	sb.WriteString("## Core LRU Cache Implementation\n\n")
	sb.WriteString("Preceding context explaining the cache mutex and initialization.\n\n")
	sb.WriteString("The LRU Cache evicts least-recently-used items when capacity exceeds maxSize using doubly-linked list.\n\n")
	sb.WriteString("Succeeding context explaining thread-safe eviction and TTL expiry.\n\n")

	for i := 0; i < 10; i++ {
		sb.WriteString("## More Unrelated Data ")
		sb.WriteString(strings.Repeat("other background noise components. ", 15))
		sb.WriteString("\n\n")
	}

	large := sb.String()
	goal := "LRU Cache eviction policy and capacity maxSize"

	out, err := PruneUpstreamOutput(ctx, large, goal, 2000)
	if err != nil {
		t.Fatalf("PruneUpstreamOutput failed: %v", err)
	}

	if len(out) > 2500 {
		t.Errorf("expected pruned output length <= ~2000 chars, got %d", len(out))
	}

	// Verify target needle is retained
	if !strings.Contains(out, "Core LRU Cache Implementation") {
		t.Errorf("expected pruned output to contain target section, got:\n%s", out)
	}
	if !strings.Contains(out, "doubly-linked list") {
		t.Errorf("expected pruned output to contain core detail, got:\n%s", out)
	}
	// Verify KNN neighbor expansion retained preceding or succeeding context
	if !strings.Contains(out, "Preceding context") && !strings.Contains(out, "Succeeding context") {
		t.Errorf("expected neighbor expansion to include surrounding context, got:\n%s", out)
	}
}
