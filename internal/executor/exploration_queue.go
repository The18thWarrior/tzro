package executor

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

// ReplaceFiles replaces the queue's file list with a new set (used after
// rich relevance scoring). Clears visited entries for files no longer in the queue.
func (eq *ExplorationQueue) ReplaceFiles(files []string) {
	newSet := make(map[string]bool, len(files))
	for _, f := range files {
		newSet[f] = true
	}
	// Remove visited entries for files no longer in the queue
	for path := range eq.visited {
		if !newSet[path] {
			delete(eq.visited, path)
		}
	}
	eq.files = files
}

// IsEmpty returns true if there are no files in the queue.
func (eq *ExplorationQueue) IsEmpty() bool {
	return len(eq.files) == 0
}

