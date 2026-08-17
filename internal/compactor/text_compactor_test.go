package compactor

import (
	"strings"
	"testing"
)

func TestBM25_Scoring(t *testing.T) {
	chunks := []string{
		"Redis is an in-memory database cache used for fast key-value retrieval and session caching.",
		"Nginx is a high performance HTTP web server and reverse proxy for load balancing.",
		"The weather in Seattle is rainy and overcast with a high of 55 degrees.",
	}

	scorer := NewBM25Scorer(chunks)
	scores := scorer.Score("database cache retrieval")

	if len(scores) != len(chunks) {
		t.Fatalf("expected %d scores, got %d", len(chunks), len(scores))
	}

	// Chunk 0 (Redis database cache) should have the highest score
	if scores[0] <= scores[1] || scores[0] <= scores[2] {
		t.Errorf("expected chunk 0 to have highest score, got scores: %v", scores)
	}

	// Chunk 2 (weather) should have 0 or lowest score
	if scores[2] > scores[1] {
		t.Errorf("expected chunk 2 to have lower score than chunk 1, got scores: %v", scores)
	}
}

func TestHybridTextScoring_KeywordsAndSynonyms(t *testing.T) {
	chunks := []string{
		"Cache indexing and eviction strategies in database memory management.",
		"Storing query results in memory for low latency retrieval and fast lookup.",
		"The national football tournament scheduled matches for next weekend.",
	}

	goal := "database caching and fast query retrieval"

	scores := ScoreChunksHybrid(chunks, goal)

	if len(scores) != len(chunks) {
		t.Fatalf("expected %d scores, got %d", len(chunks), len(scores))
	}

	// Both chunk 0 (exact terms) and chunk 1 (synonyms/concepts) should score well above chunk 2
	if scores[0] <= scores[2] {
		t.Errorf("expected chunk 0 (exact) score > chunk 2 (unrelated), got %f vs %f", scores[0], scores[2])
	}
	if scores[1] <= scores[2] {
		t.Errorf("expected chunk 1 (semantic synonym) score > chunk 2 (unrelated), got %f vs %f", scores[1], scores[2])
	}
}

func TestCompactTextHybrid_BudgetAndOrderPreservation(t *testing.T) {
	doc := `## Overview
This document covers various system architecture concepts and unrelated notes.

## Section 1: Database Memory Indexing
Database memory indexing structures like B-Trees and LSM Trees allow high speed lookups for key value queries.

## Section 2: Regional Weather Trends
The forecast for tomorrow predicts sunny skies with mild westerly winds across the valley.

## Section 3: Cache Eviction Policies
LRU and LFU cache eviction algorithms determine which cache entries to discard when memory capacity is reached.

## Section 4: Baking Recipes
To bake sourdough bread, mix flour and water and allow fermentation for 24 hours at room temperature.`

	goal := "cache eviction algorithms and memory indexing"
	budget := 400

	compacted := CompactTextHybrid(doc, goal, budget)

	if len(compacted) > budget+100 {
		t.Errorf("expected compacted length <= %d, got %d", budget+100, len(compacted))
	}

	// Should contain Section 1 (Indexing) and Section 3 (Cache Eviction)
	if !strings.Contains(compacted, "Database Memory Indexing") && !strings.Contains(compacted, "indexing") {
		t.Errorf("expected compacted output to contain Section 1 (Indexing), got:\n%s", compacted)
	}
	if !strings.Contains(compacted, "Cache Eviction Policies") && !strings.Contains(compacted, "eviction") {
		t.Errorf("expected compacted output to contain Section 3 (Cache Eviction), got:\n%s", compacted)
	}

	// Should NOT contain Section 2 (Weather) or Section 4 (Baking)
	if strings.Contains(compacted, "Regional Weather Trends") || strings.Contains(compacted, "forecast") {
		t.Errorf("expected compacted output to omit Section 2 (Weather)")
	}
	if strings.Contains(compacted, "Baking Recipes") || strings.Contains(compacted, "sourdough") {
		t.Errorf("expected compacted output to omit Section 4 (Baking)")
	}

	// Verify order: Section 1 should appear before Section 3
	idx1 := strings.Index(compacted, "Database Memory Indexing")
	idx3 := strings.Index(compacted, "Cache Eviction Policies")
	if idx1 != -1 && idx3 != -1 && idx1 > idx3 {
		t.Errorf("expected original document order preserved (Section 1 before Section 3), but got idx1=%d, idx3=%d", idx1, idx3)
	}
}
