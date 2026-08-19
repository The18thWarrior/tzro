package index

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
)

// Embedder generates vector embeddings for a given query text.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// HybridSearch executes both FTS5 keyword search and in-memory vector cosine similarity,
// combining the rankings with Reciprocal Rank Fusion (RRF).
func (s *IndexStore) HybridSearch(ctx context.Context, query string, embedder Embedder, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	// 1. FTS5 Search (BM25)
	ftsResults, err := s.SearchFTS(query, limit*2)
	if err != nil {
		ftsResults = nil // Graceful degradation on FTS syntax errors
	}

	// 2. Vector Search (if embedder provided)
	var vecResults []SearchResult
	if embedder != nil {
		queryEmb, err := embedder.Embed(ctx, query)
		if err == nil && len(queryEmb) > 0 {
			vecResults, _ = s.searchVectors(queryEmb, limit*2)
		}
	}

	// 3. Reciprocal Rank Fusion (RRF)
	return mergeRRF(ftsResults, vecResults, limit), nil
}

func (s *IndexStore) searchVectors(queryEmb []float32, limit int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT id, file_path, kind, header, content, symbol_refs, embedding_json FROM index_doc_chunks WHERE embedding_json IS NOT NULL AND embedding_json != ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type scoredChunk struct {
		res   SearchResult
		score float64
	}

	var candidates []scoredChunk
	for rows.Next() {
		var id, filePath, kind, header, content, symRefsRaw, embJSON string
		if err := rows.Scan(&id, &filePath, &kind, &header, &content, &symRefsRaw, &embJSON); err != nil {
			continue
		}

		var chunkEmb []float32
		if err := json.Unmarshal([]byte(embJSON), &chunkEmb); err != nil || len(chunkEmb) != len(queryEmb) {
			continue
		}

		sim := cosineSimilarity(queryEmb, chunkEmb)
		if sim > 0 {
			var symRefs []string
			_ = json.Unmarshal([]byte(symRefsRaw), &symRefs)

			candidates = append(candidates, scoredChunk{
				res: SearchResult{
					ID:         id,
					FilePath:   filePath,
					Kind:       kind,
					Title:      header,
					Signature:  strings.Join(symRefs, " "),
					Content:    content,
					Score:      sim,
					SourceType: "doc",
					SymbolRefs: symRefs,
				},
				score: sim,
			})
		}
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	var results []SearchResult
	for i := 0; i < len(candidates) && i < limit; i++ {
		results = append(results, candidates[i].res)
	}
	return results, nil
}

func mergeRRF(listA []SearchResult, listB []SearchResult, limit int) []SearchResult {
	const k = 60.0 // Standard RRF smoothing constant

	scores := make(map[string]float64)
	items := make(map[string]SearchResult)

	for rank, item := range listA {
		scores[item.ID] += 1.0 / (k + float64(rank+1))
		items[item.ID] = item
	}

	for rank, item := range listB {
		scores[item.ID] += 1.0 / (k + float64(rank+1))
		if _, exists := items[item.ID]; !exists {
			items[item.ID] = item
		}
	}

	type rankedItem struct {
		item  SearchResult
		score float64
	}

	var ranked []rankedItem
	for id, score := range scores {
		it := items[id]
		it.Score = score
		ranked = append(ranked, rankedItem{item: it, score: score})
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	var merged []SearchResult
	for i := 0; i < len(ranked) && i < limit; i++ {
		merged = append(merged, ranked[i].item)
	}
	return merged
}

func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < len(a); i++ {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
