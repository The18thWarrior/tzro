package symbols

import (
	"os"
	"path/filepath"
	"strings"
)

// DirectoryManifest is a recursive structural summary of a directory tree.
// Used by "overview" mode probes to build context for DirectSynthesis
// instead of running the full Thought Chain exploration loop.
type DirectoryManifest struct {
	Path       string               `json:"path"`
	FileCount  int                  `json:"fileCount"`
	Symbols    []Symbol             `json:"symbols,omitempty"`
	DocPreview []DocEntry           `json:"docPreview,omitempty"`
	Children   []*DirectoryManifest `json:"children,omitempty"`
}

// DocEntry represents a document file's title and preview lines.
type DocEntry struct {
	File    string   `json:"file"`
	Title   string   `json:"title"`
	Preview []string `json:"preview"` // First 3 non-empty lines
}

// EstimateChars returns a rough character count for the manifest content.
// Used for budget tracking during recursive traversal.
func (m *DirectoryManifest) EstimateChars() int {
	chars := len(m.Path) + 20 // path + file count overhead

	for _, sym := range m.Symbols {
		chars += len(sym.Signature) + len(sym.Name) + 10
	}
	for _, doc := range m.DocPreview {
		chars += len(doc.Title) + len(doc.File) + 10
		for _, line := range doc.Preview {
			chars += len(line) + 1
		}
	}
	for _, child := range m.Children {
		chars += child.EstimateChars()
	}
	return chars
}

// BuildDirectoryManifest builds a recursive structural summary of a directory tree.
//
// For each directory:
//   - Code files → extract exported symbols via tree-sitter (signatures only, no bodies)
//   - Doc files (.md, .txt, .rst) → extract title / first 3 lines as preview
//   - Recurse into subdirectories up to maxDepth
//
// Budget control: symbols and previews are added until charBudget is exhausted.
// No artificial per-directory cap — the budget fills naturally.
func BuildDirectoryManifest(dir string, depth, maxDepth, charBudget int) *DirectoryManifest {
	manifest := &DirectoryManifest{
		Path: dir,
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return manifest
	}

	remainingBudget := charBudget

	// Process files in this directory
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip hidden files and test files
		if strings.HasPrefix(name, ".") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		ext := strings.ToLower(filepath.Ext(name))
		fullPath := filepath.Join(dir, name)

		switch {
		case isCodeFile(ext):
			if remainingBudget <= 0 {
				manifest.FileCount++
				continue
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				manifest.FileCount++
				continue
			}

			syms, err := ExtractSymbols(name, data)
			if err != nil || len(syms) == 0 {
				manifest.FileCount++
				continue
			}

			// Add exported symbols until budget exhausted
			for _, sym := range syms {
				if !sym.Exported {
					continue
				}
				cost := len(sym.Signature) + len(sym.Name) + 10
				if remainingBudget-cost < 0 && len(manifest.Symbols) > 0 {
					break
				}
				manifest.Symbols = append(manifest.Symbols, sym)
				remainingBudget -= cost
			}
			manifest.FileCount++

		case isDocFile(ext):
			if remainingBudget <= 0 {
				manifest.FileCount++
				continue
			}
			data, err := os.ReadFile(fullPath)
			if err != nil {
				manifest.FileCount++
				continue
			}

			docEntry := extractDocPreview(name, string(data))
			cost := len(docEntry.Title) + len(docEntry.File) + 10
			for _, line := range docEntry.Preview {
				cost += len(line) + 1
			}
			manifest.DocPreview = append(manifest.DocPreview, docEntry)
			remainingBudget -= cost
			manifest.FileCount++

		default:
			// Skip unsupported extensions but count the file
			manifest.FileCount++
		}
	}

	// Recurse into subdirectories
	if depth < maxDepth {
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Skip hidden directories and common noise
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" || name == "__pycache__" {
				continue
			}

			childBudget := remainingBudget
			if childBudget < 0 {
				childBudget = 0
			}
			child := BuildDirectoryManifest(
				filepath.Join(dir, name),
				depth+1,
				maxDepth,
				childBudget,
			)
			manifest.Children = append(manifest.Children, child)
			remainingBudget -= child.EstimateChars()
		}
	} else {
		// At depth limit: list subdirectories as empty children (names only)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			manifest.Children = append(manifest.Children, &DirectoryManifest{
				Path: filepath.Join(dir, name),
			})
		}
	}

	return manifest
}

// isCodeFile returns true for source code file extensions.
func isCodeFile(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java", ".kt",
		".rb", ".c", ".cpp", ".h", ".hpp", ".cs", ".swift", ".lua":
		return true
	}
	return false
}

// isDocFile returns true for documentation file extensions.
func isDocFile(ext string) bool {
	switch ext {
	case ".md", ".txt", ".rst":
		return true
	}
	return false
}

// extractDocPreview extracts title and first 3 non-empty lines from a document.
func extractDocPreview(filename, content string) DocEntry {
	lines := strings.Split(content, "\n")
	entry := DocEntry{
		File: filename,
	}

	var previewLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// First non-empty line is the title
		if entry.Title == "" {
			// Strip markdown heading prefix
			entry.Title = strings.TrimLeft(trimmed, "# ")
			continue
		}
		previewLines = append(previewLines, trimmed)
		if len(previewLines) >= 3 {
			break
		}
	}
	entry.Preview = previewLines

	return entry
}
