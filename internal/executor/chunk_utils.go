package executor

import (
	"fmt"
	"os"
	"strings"
)

// ListFileChunk represents a single file's extracted content from a List Node output.
// Exported for use by both Recall compaction (non-docgen paths) and
// Sectioned Synthesis embedding dedup (docgen paths, ADR-0094).
type ListFileChunk struct {
	FilePath string
	Content  string
}

// SplitListOutputIntoFileChunks splits List node output into per-file chunks
// by detecting "--- file:" boundary markers. Each chunk contains the content
// for a single source file, enabling per-file processing that fits within the
// 4B model's context window.
func SplitListOutputIntoFileChunks(output string) []ListFileChunk {
	const divider = "--- file: "
	lines := strings.Split(output, "\n")

	var chunks []ListFileChunk
	var currentPath string
	var currentLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, divider) {
			// Flush previous chunk
			if currentPath != "" && len(currentLines) > 0 {
				chunks = append(chunks, ListFileChunk{
					FilePath: currentPath,
					Content:  strings.Join(currentLines, "\n"),
				})
			}
			// Extract file path from divider line: "--- file: /path/to/file lines: N-M ---"
			rest := line[len(divider):]
			if idx := strings.Index(rest, " lines: "); idx > 0 {
				currentPath = rest[:idx]
			} else {
				currentPath = strings.TrimSuffix(strings.TrimSpace(rest), " ---")
			}
			currentLines = []string{line} // Include the divider itself
		} else {
			currentLines = append(currentLines, line)
		}
	}

	// Flush final chunk
	if currentPath != "" && len(currentLines) > 0 {
		chunks = append(chunks, ListFileChunk{
			FilePath: currentPath,
			Content:  strings.Join(currentLines, "\n"),
		})
	}

	// Merge chunks from the same file (List node may have multiple ranges per file)
	merged := make(map[string]*ListFileChunk)
	var order []string
	for _, chunk := range chunks {
		if existing, ok := merged[chunk.FilePath]; ok {
			existing.Content += "\n" + chunk.Content
		} else {
			merged[chunk.FilePath] = &ListFileChunk{
				FilePath: chunk.FilePath,
				Content:  chunk.Content,
			}
			order = append(order, chunk.FilePath)
		}
	}

	var result []ListFileChunk
	for _, path := range order {
		result = append(result, *merged[path])
	}
	return result
}

// ExpandChunksIntraFile splits any chunk exceeding the threshold into sub-chunks
// at paragraph boundaries (\n\n). This handles single-file and multi-file cases
// where large files would produce prompts too big for the 4B model.
func ExpandChunksIntraFile(chunks []ListFileChunk, threshold int) []ListFileChunk {
	if threshold <= 0 {
		threshold = 8000
	}
	var expanded []ListFileChunk
	for _, chunk := range chunks {
		if len(chunk.Content) <= threshold {
			expanded = append(expanded, chunk)
			continue
		}
		parts := strings.Split(chunk.Content, "\n\n")
		partCount := 0
		var current strings.Builder
		for _, part := range parts {
			if current.Len()+len(part) > threshold && current.Len() > 0 {
				partCount++
				expanded = append(expanded, ListFileChunk{
					FilePath: chunk.FilePath,
					Content:  current.String(),
				})
				current.Reset()
			}
			if current.Len() > 0 {
				current.WriteString("\n\n")
			}
			current.WriteString(part)
		}
		if current.Len() > 0 {
			partCount++
			expanded = append(expanded, ListFileChunk{
				FilePath: chunk.FilePath,
				Content:  current.String(),
			})
		}
		fmt.Fprintf(os.Stderr, "[ChunkUtils] Split oversized chunk %s (%d chars) into %d sub-chunks\n", chunk.FilePath, len(chunk.Content), partCount)
	}
	return expanded
}
