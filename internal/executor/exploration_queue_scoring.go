package executor

// exploration_queue_scoring.go — Multi-signal rich relevance scoring for
// Exploration Queue file selection.
//
// Closes the ADR-0082 §1 gap: replaces the single-signal filepath embedding
// pruning (ScoreAndPrune) with a composite scorer using three per-file-type
// signals plus 1-hop import affinity boosting.
//
// Signals:
//   - Code files:  AST symbol name similarity (0.65) + path similarity (0.35) × import affinity (1.25×)
//   - Text files:  Semantic content similarity (0.65) + path similarity (0.35)
//
// See CONTEXT.md: Exploration Queue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tzro/internal/embeddings"
	"tzro/internal/inference"
	"tzro/internal/symbols"
)

const (
	// Weights for composite scoring.
	weightPrimary = 0.65
	weightPath    = 0.35

	// importAffinityMultiplier boosts files that are imported by high-scoring
	// candidates. Applied as a post-multiplier on code files only, 1-hop.
	importAffinityMultiplier = 1.25

	// absoluteFloor is the minimum composite score for a file to be
	// eligible for deep-read. Files below this are always excluded.
	absoluteFloor = 0.10

	// maxTextPreviewLines is the number of lines read from text/doc files
	// for semantic embedding.
	maxTextPreviewLines = 20
)

// ScoredFile represents a candidate file with its multi-signal relevance score.
type ScoredFile struct {
	Path           string
	ASTScore       float32 // Code: symbol name similarity to goal
	SemanticScore  float32 // Text: content embedding similarity to goal
	PathScore      float32 // All: path/filename embedding similarity
	ImportBoosted  bool    // Whether import affinity was applied
	CompositeScore float32 // Weighted combination with import multiplier
}

// scoringFileInfo holds per-file metadata extracted during Phase 1 classification.
type scoringFileInfo struct {
	path      string
	isCode    bool
	isText    bool
	embedText string   // Text to embed for the primary signal
	imports   []string // Import paths (code files only)
}

// textExtensions maps file extensions to whether they are text/doc files.
var textExtensions = map[string]bool{
	".md":  true,
	".txt": true,
	".rst": true,
}

// RichScoreAndSelect scores all candidate files using multi-signal relevance
// and returns them sorted by composite score descending. The caller uses the
// result to select top-3 for Discover and top-K for Deep-Read.
//
// Replaces ExplorationQueue.ScoreAndPrune (ADR-0082 gap closure).
func RichScoreAndSelect(ctx context.Context, goal string, files []string, goalType string) []ScoredFile {
	if len(files) == 0 || goal == "" {
		return nil
	}

	// Determine max K based on goal type
	maxK := deepReadK(goalType)

	// Phase 1: Classify files and extract per-type content for embedding
	infos := make([]scoringFileInfo, len(files))
	for i, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		info := scoringFileInfo{path: f}

		if codeExtensions[ext] {
			info.isCode = true
			source, err := os.ReadFile(f)
			if err == nil && len(source) > 0 {
				// Extract symbol names for AST signal
				syms, _ := symbols.ExtractSymbols(filepath.Base(f), source)
				var names []string
				for _, s := range syms {
					names = append(names, s.Name)
					if s.Signature != "" {
						names = append(names, s.Signature)
					}
				}
				info.embedText = strings.Join(names, " ")

				// Extract import paths for affinity map
				imps, _ := symbols.ExtractImports(filepath.Base(f), source)
				info.imports = imps
			}
		} else if textExtensions[ext] {
			info.isText = true
			content, err := os.ReadFile(f)
			if err == nil && len(content) > 0 {
				// Read first N lines for semantic signal
				lines := strings.SplitN(string(content), "\n", maxTextPreviewLines+1)
				if len(lines) > maxTextPreviewLines {
					lines = lines[:maxTextPreviewLines]
				}
				info.embedText = strings.Join(lines, "\n")
			}
		}

		infos[i] = info
	}

	// Phase 2: Batch embed all texts (goal + primary signals + paths)
	scored := make([]ScoredFile, len(files))

	if inference.GlobalEmbeddingSidecar != nil && inference.GlobalEmbeddingSidecar.IsAvailable() {
		// Build batch: [goal, primary_0, primary_1, ..., path_0, path_1, ...]
		allTexts := make([]string, 0, 1+2*len(files))
		allTexts = append(allTexts, goal)

		// Primary signal texts (AST names for code, header for text, path for fallback)
		for _, info := range infos {
			if info.embedText != "" {
				allTexts = append(allTexts, info.embedText)
			} else {
				allTexts = append(allTexts, filepath.Base(info.path))
			}
		}

		// Path signal texts
		for _, info := range infos {
			allTexts = append(allTexts, info.path)
		}

		vecs, err := inference.GlobalEmbeddingSidecar.EmbedBatch(ctx, allTexts)
		if err == nil && len(vecs) == 1+2*len(files) {
			goalVec := vecs[0]
			primaryVecs := vecs[1 : 1+len(files)]
			pathVecs := vecs[1+len(files):]

			for i, info := range infos {
				primarySim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, primaryVecs[i])
				pathSim := inference.GlobalEmbeddingSidecar.CosineSimilarity(goalVec, pathVecs[i])

				scored[i] = ScoredFile{
					Path:      info.path,
					PathScore: pathSim,
				}
				if info.isCode {
					scored[i].ASTScore = primarySim
				} else {
					scored[i].SemanticScore = primarySim
				}
				scored[i].CompositeScore = weightPrimary*primarySim + weightPath*pathSim
			}
		} else {
			// Fallback to bag-of-words
			scoreBagOfWords(goal, infos, scored)
		}
	} else {
		// Bag-of-words fallback when embedding sidecar unavailable
		scoreBagOfWords(goal, infos, scored)
	}

	// Phase 3: Build import affinity map and apply 1-hop boost
	applyImportAffinity(infos, scored)

	// Phase 4: Sort by composite score descending, apply floor and K cap
	sort.SliceStable(scored, func(i, j int) bool {
		return scored[i].CompositeScore > scored[j].CompositeScore
	})

	// Apply absolute floor
	var result []ScoredFile
	for _, s := range scored {
		if s.CompositeScore >= absoluteFloor {
			result = append(result, s)
		}
	}

	// Cap at 3 (Discover) + maxK (Deep-Read)
	totalCap := 3 + maxK
	if len(result) > totalCap {
		result = result[:totalCap]
	}

	fmt.Fprintf(os.Stderr, "[RichScore] %d candidates → %d selected (goal=%q, K=%d, floor=%.2f)\n",
		len(files), len(result), goalType, maxK, absoluteFloor)

	return result
}

// deepReadK returns the maximum number of deep-read files based on goal type.
func deepReadK(goalType string) int {
	switch goalType {
	case "overview":
		return 8
	case "focused":
		return 5
	default:
		return 5
	}
}

// scoreBagOfWords computes bag-of-words cosine similarity as a fallback
// when the embedding sidecar is unavailable.
func scoreBagOfWords(goal string, infos []scoringFileInfo, scored []ScoredFile) {
	for i, info := range infos {
		primaryText := info.embedText
		if primaryText == "" {
			primaryText = filepath.Base(info.path)
		}
		primarySim := float32(embeddings.CosineSimilarity(goal, primaryText))
		pathSim := float32(embeddings.CosineSimilarity(goal, info.path))

		scored[i] = ScoredFile{
			Path:      info.path,
			PathScore: pathSim,
		}
		if info.isCode {
			scored[i].ASTScore = primarySim
		} else {
			scored[i].SemanticScore = primarySim
		}
		scored[i].CompositeScore = weightPrimary*primarySim + weightPath*pathSim
	}
}

// applyImportAffinity builds a reverse import index and applies the 1.25×
// multiplicative boost to files that are imported by high-scoring candidates.
// Only applies to code files. 1-hop only.
func applyImportAffinity(infos []scoringFileInfo, scored []ScoredFile) {
	if len(scored) == 0 {
		return
	}

	// For each code file with imports, check if any import path matches
	// another candidate file's path. If so, boost the imported file.
	boosted := make(map[int]bool)
	for i, info := range infos {
		if !info.isCode || len(info.imports) == 0 {
			continue
		}
		// Only boost from files that themselves scored decently
		if scored[i].CompositeScore < absoluteFloor {
			continue
		}

		for _, imp := range info.imports {
			for j, target := range scored {
				if j == i || boosted[j] {
					continue
				}
				if importPathMatches(imp, target.Path) {
					scored[j].CompositeScore *= importAffinityMultiplier
					scored[j].ImportBoosted = true
					boosted[j] = true
				}
			}
		}
	}
}

// importPathMatches returns true if an import path semantically matches a file path.
// For Go: import "tzro/internal/memory" matches a file path containing "/internal/memory/"
// For Python: import "foo.bar" → "foo/bar" matches "/foo/bar/"
// For JS/TS: import "./utils" matches a file path containing "/utils."
// General strategy: normalize the import path to use "/" separators, then check
// if the file's directory path contains it as a suffix.
func importPathMatches(importPath, filePath string) bool {
	if importPath == "" || filePath == "" {
		return false
	}

	// Normalize: dots to slashes (Python), trim leading "./" (JS relative)
	normalized := importPath
	// Skip stdlib imports (single-segment like "fmt", "os", "sys")
	if !strings.Contains(normalized, "/") && !strings.Contains(normalized, ".") {
		return false
	}
	normalized = strings.TrimPrefix(normalized, "./")

	if normalized == "" {
		return false
	}

	// Get the directory of the file path
	dir := filepath.Dir(filePath)

	// Check if the directory contains the import path as a suffix
	// e.g., dir="/Users/jp/Desktop/Repos/tzro/internal/memory" contains "internal/memory"
	return strings.Contains(dir, normalized)
}
