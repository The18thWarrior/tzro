package executor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tzro/internal/symbols"
)

// EntryPointSelector determines which functions are relevant entry points for a goal.
type EntryPointSelector interface {
	SelectEntryPoints(signatures []string, goal string) ([]string, error)
}

// isCodeDominantDirectory checks if > 30% of files in the directories are parseable code files.
func isCodeDominantDirectory(paths []string) bool {
	var totalFiles, codeFiles int

	for _, dir := range paths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			totalFiles++
			ext := strings.ToLower(filepath.Ext(e.Name()))
			switch ext {
			case ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".rs", ".java":
				codeFiles++
			}
		}
	}

	if totalFiles == 0 {
		return false
	}

	return float64(codeFiles)/float64(totalFiles) > 0.30
}

// buildGraphDrivenContext builds a structured context using the call graph.
// Steps: build/refresh graph → select entry points → traverse → assemble
func buildGraphDrivenContext(dir string, goal string, dbPath string, selector EntryPointSelector) (string, error) {
	// Step 1: Build the call graph
	graphSymbols, graphEdges, err := symbols.BuildCallGraph(dir)
	if err != nil {
		return "", fmt.Errorf("building call graph: %w", err)
	}

	if len(graphSymbols) == 0 {
		return "", fmt.Errorf("no symbols found in %s", dir)
	}

	// Step 2: Persist to store (for caching/staleness)
	store, err := symbols.NewCallGraphStore(dbPath)
	if err != nil {
		// Non-fatal: proceed without persistence
		fmt.Fprintf(os.Stderr, "[CallGraph] Warning: failed to open store: %v\n", err)
	} else {
		defer store.Close()
		if err := store.SaveGraph(dir, graphSymbols, graphEdges); err != nil {
			fmt.Fprintf(os.Stderr, "[CallGraph] Warning: failed to save graph: %v\n", err)
		}
	}

	// Step 3: Select entry points via LLM or heuristic
	var entryNames []string
	if selector != nil {
		// Collect all signatures
		var sigs []string
		for _, s := range graphSymbols {
			if s.Signature != "" {
				sigs = append(sigs, s.Signature)
			}
		}

		selected, err := selector.SelectEntryPoints(sigs, goal)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[CallGraph] Entry point selection failed: %v — using all exported\n", err)
			// Fallback: use all exported functions
			for _, s := range graphSymbols {
				if s.Exported {
					entryNames = append(entryNames, s.Name)
				}
			}
		} else {
			// Map selected signatures back to symbol names
			sigToName := make(map[string]string)
			for _, s := range graphSymbols {
				sigToName[s.Signature] = s.Name
			}
			for _, sig := range selected {
				if name, ok := sigToName[sig]; ok {
					entryNames = append(entryNames, name)
				} else {
					// Try partial match
					for storedSig, name := range sigToName {
						if strings.Contains(storedSig, sig) || strings.Contains(sig, name) {
							entryNames = append(entryNames, name)
							break
						}
					}
				}
			}
		}
	}

	// Fallback: if no entry points selected, use all exported functions
	if len(entryNames) == 0 {
		for _, s := range graphSymbols {
			if s.Exported {
				entryNames = append(entryNames, s.Name)
			}
		}
	}

	// Step 4: Traverse subgraph from entry points
	traversed := symbols.TraverseSubgraph(graphSymbols, graphEdges, entryNames, 2, 24000, 30)

	// Step 5: Assemble context
	context, err := symbols.AssembleContext(traversed, graphEdges, dir, true)
	if err != nil {
		return "", fmt.Errorf("assembling context: %w", err)
	}

	return context, nil
}
