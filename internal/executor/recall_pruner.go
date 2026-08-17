package executor

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"tzro/internal/compactor"
)

// PruneUpstreamOutput semantically compresses large upstream RawOutput strings
// for Recall Node manifests using hybrid BM25 + Cosine scoring with KNN neighbor expansion.
func PruneUpstreamOutput(ctx context.Context, rawOutput string, goal string, maxChars int) (string, error) {
	if len(rawOutput) <= 2000 {
		return rawOutput, nil
	}

	if maxChars <= 0 {
		maxChars = 4000
	}

	chunks := compactor.SplitSemanticChunks(rawOutput)
	if len(chunks) <= 1 {
		return compactor.TruncateTextMiddleOut(rawOutput, maxChars), nil
	}

	// Sub-divide oversized chunks (>1000 chars) with 100 char overlap
	var refinedChunks []string
	for _, c := range chunks {
		if len(c) > 1000 {
			sub := splitWithOverlap(c, 700, 100)
			refinedChunks = append(refinedChunks, sub...)
		} else {
			refinedChunks = append(refinedChunks, c)
		}
	}
	chunks = refinedChunks

	if len(chunks) == 0 {
		return compactor.TruncateTextMiddleOut(rawOutput, maxChars), nil
	}

	scores := compactor.ScoreChunksHybrid(chunks, goal)

	type chunkCandidate struct {
		index int
		score float64
		len   int
	}

	candidates := make([]chunkCandidate, len(chunks))
	for i, c := range chunks {
		candidates[i] = chunkCandidate{
			index: i,
			score: scores[i],
			len:   utf8.RuneCountInString(c),
		}
	}

	// Sort by score descending to find top matching seeds
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].score > candidates[j].score
	})

	// Pick top scoring seeds and expand to neighbors (i-1, i, i+1)
	pickedMap := make(map[int]bool)
	for _, cand := range candidates {
		if cand.score <= 0 && len(pickedMap) > 0 {
			break
		}
		// Add seed
		pickedMap[cand.index] = true
		// Add predecessor
		if cand.index > 0 {
			pickedMap[cand.index-1] = true
		}
		// Add successor
		if cand.index+1 < len(chunks) {
			pickedMap[cand.index+1] = true
		}

		// Calculate total chars
		total := 0
		for idx := range pickedMap {
			total += utf8.RuneCountInString(chunks[idx])
		}
		if total >= maxChars {
			break
		}
	}

	if len(pickedMap) == 0 {
		return compactor.TruncateTextMiddleOut(rawOutput, maxChars), nil
	}

	var sortedIndices []int
	for idx := range pickedMap {
		sortedIndices = append(sortedIndices, idx)
	}
	sort.Ints(sortedIndices)

	var sb strings.Builder
	currentChars := 0

	for i, idx := range sortedIndices {
		c := chunks[idx]
		cLen := utf8.RuneCountInString(c)

		if currentChars+cLen > maxChars && currentChars > 0 {
			break
		}

		if i > 0 {
			prevIdx := sortedIndices[i-1]
			if idx > prevIdx+1 {
				sb.WriteString("\n\n[... sections omitted ...]\n\n")
			} else {
				sb.WriteString("\n\n")
			}
		}
		sb.WriteString(c)
		currentChars += cLen
	}

	return sb.String(), nil
}

// splitWithOverlap splits text into chunks of targetSize with overlap
func splitWithOverlap(text string, targetSize, overlap int) []string {
	if len(text) <= targetSize {
		return []string{text}
	}
	var chunks []string
	start := 0
	for start < len(text) {
		end := start + targetSize
		if end >= len(text) {
			chunks = append(chunks, text[start:])
			break
		}
		// Try to break at a newline or space
		breakPoint := strings.LastIndexAny(text[start:end], "\n. ")
		if breakPoint > targetSize/2 {
			end = start + breakPoint + 1
		}
		chunks = append(chunks, text[start:end])
		start = end - overlap
		if start < 0 {
			start = 0
		}
	}
	return chunks
}
