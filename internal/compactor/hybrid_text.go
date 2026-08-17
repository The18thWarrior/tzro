package compactor

import (
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"tzro/internal/embeddings"
)

// ScoreChunksHybrid computes a normalized hybrid score for each chunk against the goal
// using parallel BM25 keyword density and Dense/BoW Cosine Similarity.
func ScoreChunksHybrid(chunks []string, goal string) []float64 {
	scores := make([]float64, len(chunks))
	if len(chunks) == 0 || goal == "" {
		return scores
	}

	// 1. BM25 Scores
	bm25Scorer := NewBM25Scorer(chunks)
	bm25Scores := bm25Scorer.Score(goal)

	// Normalize BM25 scores to [0, 1]
	maxBM25 := 0.0
	for _, s := range bm25Scores {
		if s > maxBM25 {
			maxBM25 = s
		}
	}

	normBM25 := make([]float64, len(chunks))
	if maxBM25 > 0 {
		for i, s := range bm25Scores {
			normBM25[i] = s / maxBM25
		}
	}

	// 2. Cosine Similarity Scores
	cosineScores := make([]float64, len(chunks))
	for i, chunk := range chunks {
		sim := embeddings.CosineSimilarity(chunk, goal)
		if math.IsNaN(sim) || sim < 0 {
			sim = 0
		}
		cosineScores[i] = sim
	}

	// 3. Hybrid Fusion: 0.5 * BM25_norm + 0.5 * Cosine
	for i := range chunks {
		scores[i] = 0.5*normBM25[i] + 0.5*cosineScores[i]
	}

	return scores
}

// SplitSemanticChunks breaks unstructured text into natural semantic blocks
// (markdown sections, paragraphs, list groups).
func SplitSemanticChunks(content string) []string {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	// Split by double newlines or markdown headers
	lines := strings.Split(content, "\n")
	var chunks []string
	var currentChunk strings.Builder

	flush := func() {
		text := strings.TrimSpace(currentChunk.String())
		if text != "" {
			chunks = append(chunks, text)
		}
		currentChunk.Reset()
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isHeader := strings.HasPrefix(trimmed, "#")
		isEmpty := trimmed == ""

		if isHeader && currentChunk.Len() > 0 {
			flush()
		}

		if isEmpty && currentChunk.Len() > 400 {
			flush()
			continue
		}

		currentChunk.WriteString(line)
		currentChunk.WriteString("\n")
	}
	flush()

	// Merge micro-chunks (< 60 chars) ONLY if neither is a markdown header
	var merged []string
	for i := 0; i < len(chunks); i++ {
		c := chunks[i]
		for len(c) < 60 && i+1 < len(chunks) && !strings.HasPrefix(strings.TrimSpace(chunks[i+1]), "#") {
			i++
			c = c + "\n\n" + chunks[i]
		}
		merged = append(merged, c)
	}

	return merged
}

type scoredIndex struct {
	index int
	score float64
	len   int
}

// CompactTextHybrid reduces large unstructured text down to a given character budget
// by selecting the highest-scoring semantic chunks via parallel BM25 and Cosine Similarity.
func CompactTextHybrid(content, goal string, budget int) string {
	if budget <= 0 || utf8.RuneCountInString(content) <= budget {
		return content
	}

	if strings.TrimSpace(goal) == "" {
		return TruncateTextMiddleOut(content, budget)
	}

	chunks := SplitSemanticChunks(content)
	if len(chunks) <= 1 {
		return TruncateTextMiddleOut(content, budget)
	}

	scores := ScoreChunksHybrid(chunks, goal)

	items := make([]scoredIndex, len(chunks))
	for i, c := range chunks {
		items[i] = scoredIndex{
			index: i,
			score: scores[i],
			len:   utf8.RuneCountInString(c),
		}
	}

	// Sort by score descending
	sort.Slice(items, func(i, j int) bool {
		return items[i].score > items[j].score
	})

	// Greedily pick top-scoring items until budget
	var pickedIndices []int
	currentChars := 0

	for _, item := range items {
		// Omission separator overhead ~30 chars
		if currentChars+item.len+30 > budget && len(pickedIndices) > 0 {
			continue
		}
		pickedIndices = append(pickedIndices, item.index)
		currentChars += item.len
		if currentChars >= budget {
			break
		}
	}

	if len(pickedIndices) == 0 {
		return TruncateTextMiddleOut(content, budget)
	}

	// Re-sort picked indices chronologically to preserve document reading flow
	sort.Ints(pickedIndices)

	var result strings.Builder
	for idx, pIdx := range pickedIndices {
		if idx > 0 {
			prevIdx := pickedIndices[idx-1]
			if pIdx > prevIdx+1 {
				result.WriteString("\n\n[... sections omitted ...]\n\n")
			} else {
				result.WriteString("\n\n")
			}
		}
		result.WriteString(chunks[pIdx])
	}

	res := result.String()
	if utf8.RuneCountInString(res) > budget+50 {
		runes := []rune(res)
		cutoff := budget
		if cutoff > len(runes) {
			cutoff = len(runes)
		}
		return string(runes[:cutoff])
	}

	return res
}
