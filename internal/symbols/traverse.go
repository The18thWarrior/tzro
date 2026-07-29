package symbols

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMaxChars     = 24000 // 24KB target for assembled context
	defaultMaxFunctions = 30    // Maximum functions to include
)

// TraverseSubgraph performs bidirectional N-hop BFS from entry points,
// returning symbols within the hop and function count budget.
// Priority: entry points → hop 1 → hop 2 → ...
func TraverseSubgraph(symbols []CallGraphSymbol, edges []CallEdge, entryNames []string, hops int, maxChars int, maxFunctions int) []CallGraphSymbol {
	if maxChars <= 0 {
		maxChars = defaultMaxChars
	}
	if maxFunctions <= 0 {
		maxFunctions = defaultMaxFunctions
	}

	// Build name→symbol index
	symByName := make(map[string]CallGraphSymbol)
	for _, s := range symbols {
		symByName[s.Name] = s
	}

	// Build adjacency lists (bidirectional)
	callees := make(map[string][]string) // caller→[]callee
	callers := make(map[string][]string) // callee→[]caller
	for _, e := range edges {
		callees[e.CallerName] = append(callees[e.CallerName], e.CalleeName)
		callers[e.CalleeName] = append(callers[e.CalleeName], e.CallerName)
	}

	// BFS from entry points
	visited := make(map[string]bool)
	var result []CallGraphSymbol

	type queueItem struct {
		name string
		hop  int
	}

	queue := make([]queueItem, 0, len(entryNames))
	for _, name := range entryNames {
		queue = append(queue, queueItem{name: name, hop: 0})
	}

	charCount := 0
	for len(queue) > 0 && len(result) < maxFunctions {
		item := queue[0]
		queue = queue[1:]

		if visited[item.name] {
			continue
		}
		if item.hop > hops {
			continue
		}

		sym, exists := symByName[item.name]
		if !exists {
			continue
		}

		// Estimate char cost (signature + body lines)
		estimatedChars := len(sym.Signature) + (sym.EndLine-sym.Line+1)*60 // ~60 chars per line
		if charCount+estimatedChars > maxChars && len(result) > 0 {
			continue // budget exhausted, skip but keep processing queue
		}

		visited[item.name] = true
		result = append(result, sym)
		charCount += estimatedChars

		// Enqueue neighbors at next hop
		if item.hop < hops {
			// Forward edges (callees)
			for _, callee := range callees[item.name] {
				if !visited[callee] {
					queue = append(queue, queueItem{name: callee, hop: item.hop + 1})
				}
			}
			// Reverse edges (callers)
			for _, caller := range callers[item.name] {
				if !visited[caller] {
					queue = append(queue, queueItem{name: caller, hop: item.hop + 1})
				}
			}
		}
	}

	return result
}

// AssembleContext builds a structured context document from symbols and edges.
// If includeBodies is true, reads function bodies from disk using file+line ranges.
// The output is formatted as a markdown document with a graph preamble followed by
// function bodies (if requested).
func AssembleContext(symbols []CallGraphSymbol, edges []CallEdge, dir string, includeBodies bool) (string, error) {
	if len(symbols) == 0 {
		return "", nil
	}

	var b strings.Builder

	// --- Graph Preamble: Signatures + Edges ---
	b.WriteString("# Call Graph Context\n\n")
	b.WriteString("## Symbol Signatures\n\n")
	for _, sym := range symbols {
		sig := sym.Signature
		if sig == "" {
			sig = fmt.Sprintf("%s %s", sym.Kind, sym.Name)
		}
		b.WriteString(fmt.Sprintf("- `%s` (%s:%d)\n", sig, sym.File, sym.Line))
	}

	b.WriteString("\n## Call Edges\n\n")
	if len(edges) > 0 {
		for _, e := range edges {
			b.WriteString(fmt.Sprintf("- %s → %s (%s)\n", e.CallerName, e.CalleeName, e.EdgeKind))
		}
	} else {
		b.WriteString("(no call edges)\n")
	}

	if !includeBodies {
		return b.String(), nil
	}

	// --- Function Bodies ---
	b.WriteString("\n## Function Bodies\n\n")

	// Cache file contents to avoid re-reading
	fileCache := make(map[string][]string) // file → lines

	charBudget := defaultMaxChars
	for _, sym := range symbols {
		if charBudget <= 0 {
			break
		}

		lines, ok := fileCache[sym.File]
		if !ok {
			fullPath := filepath.Join(dir, sym.File)
			content, err := os.ReadFile(fullPath)
			if err != nil {
				continue // skip unreadable files
			}
			lines = strings.Split(string(content), "\n")
			fileCache[sym.File] = lines
		}

		// Extract body using line range
		startIdx := sym.Line - 1 // 0-indexed
		endIdx := sym.EndLine - 1
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx >= len(lines) {
			endIdx = len(lines) - 1
		}
		if startIdx > endIdx {
			continue
		}

		body := strings.Join(lines[startIdx:endIdx+1], "\n")
		if len(body) > charBudget {
			body = body[:charBudget]
		}

		b.WriteString(fmt.Sprintf("### %s (%s:%d-%d)\n\n", sym.Name, sym.File, sym.Line, sym.EndLine))
		b.WriteString("```\n")
		b.WriteString(body)
		b.WriteString("\n```\n\n")

		charBudget -= len(body)
	}

	return b.String(), nil
}
