package index

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	markdownHeaderRe = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	symbolBacktickRe = regexp.MustCompile("`([a-zA-Z0-9_]{3,})`")
)

// ChunkDocument parses markdown or text content into structured DocChunk slices.
func ChunkDocument(relPath string, content []byte) ([]DocChunk, error) {
	if len(content) == 0 {
		return nil, nil
	}

	ext := strings.ToLower(filepath.Ext(relPath))
	switch ext {
	case ".md", ".markdown":
		return chunkMarkdown(relPath, content)
	default:
		return chunkPlainText(relPath, content)
	}
}

func chunkMarkdown(relPath string, content []byte) ([]DocChunk, error) {
	var chunks []DocChunk
	scanner := bufio.NewScanner(bytes.NewReader(content))

	var currentHeader string
	var currentLines []string
	chunkIndex := 0

	flushChunk := func() {
		body := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if body == "" && currentHeader == "" {
			return
		}
		chunkIndex++
		chunkID := fmt.Sprintf("%s#s%d", relPath, chunkIndex)
		refs := extractSymbolReferences(body + " " + currentHeader)

		chunks = append(chunks, DocChunk{
			ID:         chunkID,
			FilePath:   relPath,
			Kind:       "doc_section",
			Header:     currentHeader,
			Content:    body,
			SymbolRefs: refs,
		})
		currentLines = nil
	}

	for scanner.Scan() {
		line := scanner.Text()
		if match := markdownHeaderRe.FindStringSubmatch(line); len(match) == 3 {
			// Found a heading — flush previous section
			flushChunk()
			currentHeader = strings.TrimSpace(match[2])
		} else {
			currentLines = append(currentLines, line)
		}
	}
	flushChunk()

	return chunks, scanner.Err()
}

func chunkPlainText(relPath string, content []byte) ([]DocChunk, error) {
	var chunks []DocChunk
	paragraphs := strings.Split(string(content), "\n\n")

	chunkIndex := 0
	for _, p := range paragraphs {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		chunkIndex++
		chunkID := fmt.Sprintf("%s#p%d", relPath, chunkIndex)
		refs := extractSymbolReferences(trimmed)

		// Use the first 60 chars as a summary header preview
		headerPreview := trimmed
		if len(headerPreview) > 60 {
			headerPreview = headerPreview[:60] + "..."
		}

		chunks = append(chunks, DocChunk{
			ID:         chunkID,
			FilePath:   relPath,
			Kind:       "doc_text",
			Header:     headerPreview,
			Content:    trimmed,
			SymbolRefs: refs,
		})
	}

	return chunks, nil
}

func extractSymbolReferences(text string) []string {
	matches := symbolBacktickRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var refs []string
	for _, m := range matches {
		if len(m) == 2 {
			sym := m[1]
			if !seen[sym] {
				seen[sym] = true
				refs = append(refs, sym)
			}
		}
	}
	return refs
}
