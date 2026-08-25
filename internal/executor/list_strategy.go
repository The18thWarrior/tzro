package executor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tzro/internal/compiler"
	"tzro/internal/strategy"
	"tzro/internal/stream"
)

// ---------------------------------------------------------------------------
// ListStrategy — extraction-only node type (ADR-0090)
// ---------------------------------------------------------------------------

// ListStrategy implements strategy.NodeStrategy for List Nodes.
// Executes: deterministic Orient (list_dir) → Discover (RichScoreAndSelect)
// → per-file GBNF extraction → verbatim snippet assembly.
// The model's only job is identifying relevant line ranges — the Go harness
// handles all file I/O, range merging, and output assembly deterministically.
type ListStrategy struct {
	strategy.BaseStrategy
	engine       ProbeInferenceEngine
	publishState func(pub interface{ PublishStream(stream.StreamChunk) }, taskID, nodeID, status, output string)
}

// NewListStrategy creates a ListStrategy for the List Node type.
func NewListStrategy(engine *ExecutionEngine, base *strategy.BaseStrategy) *ListStrategy {
	s := &ListStrategy{
		BaseStrategy: *base,
		publishState: publishNodeState,
	}
	if engine != nil {
		s.engine = &ProbeInference{}
	}
	return s
}

// Type returns "list".
func (s *ListStrategy) Type() string { return "list" }

// PlannerCard returns the planner description for List Nodes.
func (s *ListStrategy) PlannerCard() *strategy.PlannerCard {
	return &strategy.PlannerCard{
		Type:      "list",
		WhenToUse: "Extraction and enumeration tasks: list symbols, catalog endpoints, index declarations. Produces verbatim source snippets without synthesis.",
		KeyFields: []strategy.FieldDesc{
			{Name: "probeConfig.goal", Description: "extraction objective — what to find in the files", Required: true},
			{Name: "probeConfig.preloadPaths", Description: "target directories to scan", Required: false},
		},
		CriticalRules: []string{
			"Use 'list' for extraction tasks where source fidelity matters. Use 'probe' when understanding and synthesis are needed.",
			"The model identifies relevant line ranges; the harness copies content verbatim. No synthesis occurs.",
		},
	}
}

// CompilationRules returns nil expansion — List Nodes should not have
// Recall or Semantic Validator injected by the compiler.
func (s *ListStrategy) CompilationRules() *strategy.CompilationRules {
	return &strategy.CompilationRules{
		Expand: func(node *compiler.GraphNode, graph *compiler.ExecutionGraph) (*strategy.ExpansionResult, error) {
			// No expansion — List Nodes produce deterministic output that
			// doesn't benefit from Recall refinement or Validator wrapping.
			return nil, nil
		},
	}
}

// EdgeThoughtPolicy returns nil — List Nodes are deterministic extraction
// and don't participate in Edge Thought evaluation.
func (s *ListStrategy) EdgeThoughtPolicy() *strategy.EdgeThoughtConfig { return nil }

// ContextRole defines how List Node output participates in accumulated context.
func (s *ListStrategy) ContextRole() *strategy.ContextRole {
	return &strategy.ContextRole{
		IsPrimaryDataCarrier: true,  // Output is the primary data
		HasThoughtSteps:      false, // No Thought Chain
		ContextWeight:        0.3,   // Lightweight — raw snippets don't need much budget
		ProducesPlainText:    true,
	}
}

// Execute runs the List Node's deterministic extraction pipeline:
// 1. Orient — list_dir on target paths
// 2. Discover — score and select relevant files
// 3. Extract — per-file GBNF-constrained line-range inference
// 4. Assemble — concatenate verbatim snippets with annotated dividers
func (s *ListStrategy) Execute(ctx context.Context, nr *strategy.NodeRuntime) (*strategy.ExecutionResult, error) {
	node := nr.Node()
	taskID := nr.TaskID()

	// Set initial running state
	_ = nr.State().SetNodeState("running", "")
	nr.Publisher().PublishEvent("node_started", taskID, node.ID, "List: "+node.Instructions)
	s.publishState(nr.Publisher(), taskID, node.ID, "running", "")

	// Get the extraction goal from ProbeConfig or Instructions
	goal := node.Instructions
	if node.ProbeConfig != nil && node.ProbeConfig.Goal != "" {
		goal = node.ProbeConfig.Goal
	}

	// --- Orient: determine target directories ---
	var targetPaths []string
	if node.ProbeConfig != nil && len(node.ProbeConfig.PreloadPaths) > 0 {
		targetPaths = node.ProbeConfig.PreloadPaths
	}

	if len(targetPaths) == 0 {
		// Fallback: detect paths from goal text
		targetPaths = detectPreloadPaths(goal, "")
	}

	if len(targetPaths) == 0 {
		return &strategy.ExecutionResult{
			Output:    "[List] No target paths found for extraction",
			Directive: strategy.DirectiveContinue,
		}, nil
	}

	// --- Discover: find relevant files in target directories ---
	var allFiles []string
	for _, dir := range targetPaths {
		entries, err := os.ReadDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ListNode] Failed to read directory %s: %v\n", dir, err)
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			// Include source code and documentation files
			ext := strings.ToLower(filepath.Ext(name))
			if isExtractableFile(ext) {
				allFiles = append(allFiles, filepath.Join(dir, name))
			}
		}
	}

	if len(allFiles) == 0 {
		return &strategy.ExecutionResult{
			Output:    "[List] No extractable files found in target directories",
			Directive: strategy.DirectiveContinue,
		}, nil
	}

	fmt.Fprintf(os.Stderr, "[ListNode] Found %d files across %d target paths\n", len(allFiles), len(targetPaths))

	// --- Extract: per-file GBNF-constrained line-range inference ---
	var snippets []string
	engine := s.engine
	if engine == nil {
		engine = &ProbeInference{}
	}

	for _, filePath := range allFiles {
		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[ListNode] Failed to read file %s: %v\n", filePath, err)
			continue
		}

		lines := strings.Split(string(content), "\n")
		lineCount := len(lines)

		// Chunk large files
		chunks := ChunkFile(lines, 800, 50)

		var fileRanges []LineRange
		for _, chunk := range chunks {
			numbered := NumberFileContent(chunk.Lines)
			ranges, err := ExtractLineRanges(ctx, engine, goal, filePath, numbered, len(chunk.Lines))
			if err != nil {
				fmt.Fprintf(os.Stderr, "[ListNode] Extraction error for %s: %v\n", filePath, err)
				continue
			}

			// Remap chunk-local line numbers to file-global line numbers
			for _, r := range ranges {
				fileRanges = append(fileRanges, LineRange{
					StartLine: r.StartLine + chunk.StartOffset,
					EndLine:   r.EndLine + chunk.StartOffset,
				})
			}
		}

		// Merge and clamp
		merged := MergeAndClampRanges(fileRanges, lineCount)
		if len(merged) == 0 {
			continue
		}

		// Format with annotated dividers
		snippet := FormatExtractedSnippets(filePath, lines, merged)
		if snippet != "" {
			snippets = append(snippets, snippet)
		}
	}

	// --- Assemble: concatenate all file snippets ---
	output := strings.Join(snippets, "\n\n")
	if output == "" {
		output = "[List] No relevant content found in any files"
	}

	fmt.Fprintf(os.Stderr, "[ListNode] Extraction complete: %d files with matches, %d chars output\n", len(snippets), len(output))

	return &strategy.ExecutionResult{
		Output:    output,
		Directive: strategy.DirectiveContinue,
	}, nil
}

// isExtractableFile returns true for file extensions that should be included
// in List Node extraction.
func isExtractableFile(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".rs",
		".dart", ".cpp", ".c", ".cc", ".h", ".hpp", ".cs", ".kt",
		".swift", ".rb", ".md", ".txt", ".rst", ".yaml", ".yml",
		".json", ".toml":
		return true
	}
	return false
}

// Compile-time interface check.
var _ strategy.NodeStrategy = (*ListStrategy)(nil)
