package executor

import (
	"context"
	"sort"

	"tzro/internal/embeddings"
	"tzro/internal/inference"
)

// ExplorationQueue is a side-channel file list built from PreloadPaths at
// Probe Node start. Tracks visited and unvisited files across Thought Chain
// steps. Used for deterministic loop-breaking: when a duplicate read_file
// call is detected, the execution layer substitutes tool arguments with the
// next unvisited file from the queue. (ADR-0058, CONTEXT.md: Exploration Queue)
type ExplorationQueue struct {
	files   []string
	visited map[string]bool
}

// NewExplorationQueue creates a queue from a sorted file list.
func NewExplorationQueue(files []string) *ExplorationQueue {
	return &ExplorationQueue{
		files:   files,
		visited: make(map[string]bool),
	}
}

// MarkVisited records that a file has been successfully read.
func (eq *ExplorationQueue) MarkVisited(path string) {
	eq.visited[path] = true
}

// NextUnvisited returns the next file that hasn't been visited.
// Returns ("", false) when all files have been visited.
func (eq *ExplorationQueue) NextUnvisited() (string, bool) {
	for _, f := range eq.files {
		if !eq.visited[f] {
			return f, true
		}
	}
	return "", false
}

// Stats returns (visited count, total count) for observability logging.
func (eq *ExplorationQueue) Stats() (int, int) {
	return len(eq.visited), len(eq.files)
}

// ScoreAndPrune ranks candidate files against the probe goal embedding and
// prunes the queue to the top-K most relevant files, preventing repository-wide
// crawl thrashing on targeted exploration tasks (ADR-0082).
func (eq *ExplorationQueue) ScoreAndPrune(ctx context.Context, goal string, maxK int) {
	if len(eq.files) <= maxK || maxK <= 0 || goal == "" {
		return
	}

	type scoredFile struct {
		file  string
		score float32
	}
	scored := make([]scoredFile, len(eq.files))

	if inference.GlobalEmbeddingSidecar.IsAvailable() {
		allTexts := make([]string, 0, len(eq.files)+1)
		allTexts = append(allTexts, goal)
		allTexts = append(allTexts, eq.files...)

		vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, allTexts)
		if err == nil && len(vecs) == len(allTexts) {
			goalVec := vecs[0]
			fileVecs := vecs[1:]
			for i, f := range eq.files {
				scored[i] = scoredFile{
					file:  f,
					score: inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, fileVecs[i]),
				}
			}
		} else {
			// Fall back to bag-of-words
			for i, f := range eq.files {
				scored[i] = scoredFile{
					file:  f,
					score: float32(embeddings.CosineSimilarity(goal, f)),
				}
			}
		}
	} else {
		// Bag-of-words cosine fallback
		for i, f := range eq.files {
			scored[i] = scoredFile{
				file:  f,
				score: float32(embeddings.CosineSimilarity(goal, f)),
			}
		}
	}

	// Sort descending by similarity score
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	pruned := make([]string, maxK)
	for i := 0; i < maxK; i++ {
		pruned[i] = scored[i].file
	}
	eq.files = pruned
}

// IsEmpty returns true if there are no files in the queue.
func (eq *ExplorationQueue) IsEmpty() bool {
	return len(eq.files) == 0
}
