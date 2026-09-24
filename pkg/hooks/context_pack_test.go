//go:build integration

package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tzro/pkg/executor"
	"tzro/pkg/extractor"
	layaPkg "tzro/pkg/laya"
	"tzro/pkg/store"
)

// ---------------------------------------------------------------------------
// Context Pack Builder — assembles pre-analyzed evidence using the graph
// executor and GLiNER before the LLM loop starts.
// ---------------------------------------------------------------------------

// contextPack holds the pre-analyzed evidence ready for injection into the LLM.
type contextPack struct {
	StructureOverview string             // probe results
	Skeletons         map[string]string  // file → skeleton output
	LogExtractions    map[string]string  // log file → extracted error spans
	LayaScores        map[string]float64 // file → bug relevance score (0-1, from Laya)
	BuildTime         time.Duration      // how long the pack took to build
}

// buildContextPack assembles a context pack using the graph executor and GLiNER.
// It probes the workspace for structure, skeletons all source files, and
// extracts error spans from log files using GLiNER.
func buildContextPack(t *testing.T, tzroBin, workspaceDir string, sourceExts []string) contextPack {
	t.Helper()
	start := time.Now()
	ctx := context.Background()

	// Discover the repo root for finding worker scripts (bin/gliner_worker.py, bin/laya_worker.py).
	// The tzroBin may be in a temp dir, so we can't resolve relative to it.
	repoRoot, _ := os.Getwd()
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(repoRoot, "cmd", "tzro", "main.go")); err == nil {
			break
		}
		repoRoot = filepath.Dir(repoRoot)
	}
	t.Logf("context pack: repo root = %s", repoRoot)

	pack := contextPack{
		Skeletons:      make(map[string]string),
		LogExtractions: make(map[string]string),
	}

	// --- Phase 1: Probe for structure ---
	probeCmd := exec.CommandContext(ctx, tzroBin, "probe", "error test bug fail")
	probeCmd.Dir = workspaceDir
	probeOut, err := probeCmd.CombinedOutput()
	if err != nil {
		t.Logf("context pack: probe failed (non-fatal): %v", err)
		pack.StructureOverview = fmt.Sprintf("probe failed: %v", err)
	} else {
		pack.StructureOverview = string(probeOut)
	}

	// --- Phase 2: Skeleton all source files ---
	var sourceFiles []string
	filepath.Walk(workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		for _, ext := range sourceExts {
			// ext is like "*.go", "*.py", etc.
			pattern := strings.TrimPrefix(ext, "*")
			if strings.HasSuffix(path, pattern) {
				relPath, _ := filepath.Rel(workspaceDir, path)
				sourceFiles = append(sourceFiles, relPath)
				break
			}
		}
		return nil
	})

	for _, f := range sourceFiles {
		absPath := filepath.Join(workspaceDir, f)
		skelCmd := exec.CommandContext(ctx, tzroBin, "skeleton", absPath)
		skelCmd.Dir = workspaceDir
		skelOut, err := skelCmd.CombinedOutput()
		if err != nil {
			t.Logf("context pack: skeleton %s failed: %v", f, err)
			continue
		}
		pack.Skeletons[f] = string(skelOut)
	}

	// --- Phase 3: Extract error spans from logs via GLiNER ---
	glinerWorker := filepath.Join(repoRoot, "bin", "gliner_worker.py")

	// Find log files
	var logFiles []string
	filepath.Walk(workspaceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, ".log") {
			relPath, _ := filepath.Rel(workspaceDir, path)
			logFiles = append(logFiles, relPath)
		}
		return nil
	})

	if len(logFiles) > 0 {
		// Try to use GLiNER for extraction
		glinerAvailable := false
		if _, err := os.Stat(glinerWorker); err == nil {
			client := extractor.NewWorkerClient("python3", glinerWorker)
			if err := client.Start(ctx); err == nil {
				glinerAvailable = true
				defer client.Close()

				for _, logFile := range logFiles {
					content, err := os.ReadFile(filepath.Join(workspaceDir, logFile))
					if err != nil {
						continue
					}

					resp, err := client.Extract(ctx, &extractor.ExtractionRequest{
						Text:   string(content),
						Labels: []string{"error_type", "file_path", "function_name", "root_cause", "line_number"},
					})
					if err != nil {
						t.Logf("context pack: GLiNER extract %s failed: %v", logFile, err)
						continue
					}

					if len(resp.Spans) > 0 {
						var sb strings.Builder
						for _, span := range resp.Spans {
							sb.WriteString(fmt.Sprintf("  [%s] %q (confidence: %.2f)\n", span.Label, span.Text, span.Confidence))
						}
						pack.LogExtractions[logFile] = sb.String()
					}
				}
			} else {
				t.Logf("context pack: GLiNER worker failed to start: %v", err)
			}
		}

		// Fallback: if GLiNER not available, use regex-based extraction
		if !glinerAvailable {
			t.Logf("context pack: GLiNER not available, using regex fallback for log extraction")
			for _, logFile := range logFiles {
				content, err := os.ReadFile(filepath.Join(workspaceDir, logFile))
				if err != nil {
					continue
				}
				extracted := extractErrorLines(string(content))
				if extracted != "" {
					pack.LogExtractions[logFile] = extracted
				}
			}
		}
	}

	// --- Phase 4: Multi-signal relevance scoring ---
	// Three signals combined into a composite score:
	//   Signal 1: Keyword overlap (error terms found in skeleton)   — weight 0.40
	//   Signal 2: Laya choice classification (root_cause/affected/test/unrelated) — weight 0.40
	//   Signal 3: File-type heuristic (source > config > test)       — weight 0.20

	// Signal 1: Extract error keywords from log extractions
	errorKeywords := extractErrorKeywords(pack.LogExtractions)
	t.Logf("context pack: extracted %d error keywords: %v", len(errorKeywords), errorKeywords)

	// Compute keyword overlap per file
	keywordScores := make(map[string]float64)
	for file, skeleton := range pack.Skeletons {
		lower := strings.ToLower(skeleton)
		hits := 0
		for _, kw := range errorKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				hits++
			}
		}
		if len(errorKeywords) > 0 {
			keywordScores[file] = float64(hits) / float64(len(errorKeywords))
		}
	}

	// Signal 3: File-type heuristic
	typeScores := make(map[string]float64)
	for file := range pack.Skeletons {
		lf := strings.ToLower(file)
		switch {
		case strings.Contains(lf, "test") || strings.Contains(lf, "spec"):
			typeScores[file] = 0.3
		case strings.Contains(lf, "config") || strings.Contains(lf, "setup") || strings.Contains(lf, "build"):
			typeScores[file] = 0.4
		case strings.Contains(lf, "mod.") || strings.Contains(lf, "lib.") || strings.Contains(lf, "main."):
			typeScores[file] = 0.5
		default:
			typeScores[file] = 0.7 // Source files get higher base score
		}
	}

	// Signal 2: Laya choice classification
	layaWorker := filepath.Join(repoRoot, "bin", "laya_worker.py")
	layaClassScores := make(map[string]float64)

	if _, err := os.Stat(layaWorker); err == nil {
		layaClient := layaPkg.NewDaemonClient("python3", layaWorker)
		layaCtx, layaCancel := context.WithTimeout(ctx, 60*time.Second)
		defer layaCancel()
		if err := layaClient.Start(layaCtx); err == nil {
			defer layaClient.Close()

			// Build a compact error summary (just the key phrases)
			errorSummary := strings.Join(errorKeywords, "; ")
			if errorSummary == "" {
				errorSummary = "unknown errors in codebase"
			}

			for file, skeleton := range pack.Skeletons {
				// Build file-specific state: include keyword match info
				kwHits := int(keywordScores[file] * float64(len(errorKeywords)))

				// Only send the imports + first few signatures (not the full skeleton)
				skelPreview := skeleton
				if len(skelPreview) > 600 {
					skelPreview = skelPreview[:600]
				}

				resp, err := layaClient.Evaluate(layaCtx, &layaPkg.DecisionRequest{
					QuestionType: "choice",
					Prompt:       fmt.Sprintf("What is this file's relationship to these errors: %s", errorSummary),
					Options:      []string{"root_cause", "propagates_error", "affected_by_error", "unrelated"},
					State: map[string]interface{}{
						"file":            file,
						"keyword_matches": kwHits,
						"preview":         skelPreview,
					},
				})
				if err != nil {
					t.Logf("context pack: Laya classify %s failed: %v", file, err)
					continue
				}

				// Map choice to score
				switch resp.Answer {
				case "root_cause":
					layaClassScores[file] = 1.0
				case "propagates_error":
					layaClassScores[file] = 0.7
				case "affected_by_error":
					layaClassScores[file] = 0.4
				default: // "unrelated"
					layaClassScores[file] = 0.1
				}

				t.Logf("context pack: Laya %s → %s (scores: %v)", file, resp.Answer, resp.Scores)
			}
		} else {
			t.Logf("context pack: Laya worker failed to start: %v", err)
		}
	} else {
		t.Logf("context pack: Laya worker not found at %s", layaWorker)
	}

	// Composite score: weighted combination of all signals
	pack.LayaScores = make(map[string]float64)
	for file := range pack.Skeletons {
		kw := keywordScores[file]   // 0-1: fraction of error keywords found in skeleton
		lc := layaClassScores[file] // 0-1: Laya classification (root_cause=1, unrelated=0.1)
		ft := typeScores[file]      // 0-1: file type heuristic

		// If Laya wasn't available, redistribute its weight to keywords
		if len(layaClassScores) == 0 {
			pack.LayaScores[file] = kw*0.70 + ft*0.30
		} else {
			pack.LayaScores[file] = kw*0.40 + lc*0.40 + ft*0.20
		}

		t.Logf("context pack: score %s = %.2f (kw=%.2f, laya=%.2f, type=%.2f)",
			file, pack.LayaScores[file], kw, lc, ft)
	}

	pack.BuildTime = time.Since(start)
	t.Logf("context pack: built in %v (%d skeletons, %d log extractions, %d laya scores, %d probe chars)",
		pack.BuildTime, len(pack.Skeletons), len(pack.LogExtractions), len(pack.LayaScores), len(pack.StructureOverview))

	return pack
}

// extractErrorLines does regex-free keyword extraction from log content.
// Falls back when GLiNER is not available.
func extractErrorLines(content string) string {
	errorKeywords := []string{"FAIL", "ERROR", "error:", "panic", "FAILED", "exception", "traceback", "assert", "fatal"}
	var extracted []string
	for _, line := range strings.Split(content, "\n") {
		lower := strings.ToLower(line)
		for _, kw := range errorKeywords {
			if strings.Contains(lower, strings.ToLower(kw)) {
				extracted = append(extracted, strings.TrimSpace(line))
				break
			}
		}
	}
	if len(extracted) > 15 {
		// Cap at 15 most relevant lines
		extracted = append(extracted[:10], append([]string{"... [truncated] ..."}, extracted[len(extracted)-5:]...)...)
	}
	if len(extracted) == 0 {
		return ""
	}
	return strings.Join(extracted, "\n")
}

// extractErrorKeywords pulls distinctive technical terms from log extractions
// for keyword-overlap scoring. Returns terms that differentiate files (identifiers,
// type names, function names) — not generic words like "error" or "failed".
func extractErrorKeywords(logExtractions map[string]string) []string {
	// Generic words to skip — these appear everywhere and don't discriminate
	stopWords := map[string]bool{
		"error": true, "fail": true, "failed": true, "failure": true,
		"test": true, "assert": true, "expected": true, "actual": true,
		"panic": true, "fatal": true, "exception": true, "traceback": true,
		"the": true, "and": true, "for": true, "not": true, "with": true,
		"from": true, "this": true, "that": true, "was": true, "but": true,
		"line": true, "file": true, "code": true, "exit": true, "status": true,
	}

	seen := make(map[string]bool)
	var keywords []string

	for _, extraction := range logExtractions {
		for _, line := range strings.Split(extraction, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || line == "... [truncated] ..." {
				continue
			}

			// Extract words that look like identifiers, types, or technical terms
			// Split on common delimiters
			for _, sep := range []string{" ", ":", "'", "\"", "(", ")", "[", "]", ",", "=", "|", "/", "\\", "{", "}", "<", ">", "`"} {
				line = strings.ReplaceAll(line, sep, " ")
			}

			for _, word := range strings.Fields(line) {
				word = strings.Trim(word, ".,;!?#*-+")
				lower := strings.ToLower(word)

				// Skip too short, too long, or generic
				if len(word) < 3 || len(word) > 50 || stopWords[lower] {
					continue
				}

				// Skip pure numbers
				isNum := true
				for _, c := range word {
					if c < '0' || c > '9' {
						isNum = false
						break
					}
				}
				if isNum {
					continue
				}

				// Keep: camelCase, snake_case, dotted.paths, type names, technical terms
				interesting := false
				// Has underscore (snake_case identifier)
				if strings.Contains(word, "_") {
					interesting = true
				}
				// Has dot (module.path or file.ext)
				if strings.Contains(word, ".") {
					interesting = true
				}
				// Has mixed case (CamelCase or camelCase)
				hasUpper, hasLower := false, false
				for _, c := range word {
					if c >= 'A' && c <= 'Z' {
						hasUpper = true
					}
					if c >= 'a' && c <= 'z' {
						hasLower = true
					}
				}
				if hasUpper && hasLower {
					interesting = true
				}
				// Contains digits mixed with letters (e.g., "uint32", "i64", "mat1")
				hasDigit := false
				for _, c := range word {
					if c >= '0' && c <= '9' {
						hasDigit = true
						break
					}
				}
				if hasDigit && (hasUpper || hasLower) {
					interesting = true
				}
				// Known technical patterns
				techPatterns := []string{"mismatch", "overflow", "underflow", "null", "nil",
					"undefined", "nan", "inf", "timeout", "deadlock", "race",
					"leak", "corrupt", "invalid", "missing", "duplicate",
					"shape", "dimension", "tensor", "matrix", "gradient",
					"dropout", "learning_rate", "batch", "epoch", "loss",
					"serialize", "deserialize", "encode", "decode", "proto",
					"token", "refresh", "auth", "middleware", "handler"}
				for _, pat := range techPatterns {
					if strings.Contains(lower, pat) {
						interesting = true
						break
					}
				}

				if interesting && !seen[lower] {
					seen[lower] = true
					keywords = append(keywords, word)
				}
			}
		}
	}

	// Cap at 30 keywords to keep the signal dense
	if len(keywords) > 30 {
		keywords = keywords[:30]
	}
	return keywords
}

// formatContextPack renders the context pack into a string for LLM injection.
func formatContextPack(pack contextPack) string {
	var sb strings.Builder

	sb.WriteString("=== PRE-ANALYZED CONTEXT PACK ===\n")
	sb.WriteString("(Built locally in " + pack.BuildTime.Round(time.Millisecond).String() + " using graph executor + GLiNER)\n\n")

	// Structure
	sb.WriteString("--- CODEBASE STRUCTURE (probe) ---\n")
	sb.WriteString(pack.StructureOverview)
	sb.WriteString("\n\n")

	// Skeletons — sorted by relevance score, full skeleton for top files only
	sb.WriteString("--- FILE SKELETONS (ranked by bug relevance, signatures with bodies elided) ---\n")

	// Sort files by score descending
	type rankedFile struct {
		file  string
		score float64
	}
	var ranked []rankedFile
	for file := range pack.Skeletons {
		ranked = append(ranked, rankedFile{file, pack.LayaScores[file]})
	}
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].score > ranked[i].score {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}

	// Include full skeletons for top 8 files, names-only for the rest
	const maxFullSkeletons = 8
	for i, rf := range ranked {
		if i < maxFullSkeletons {
			marker := ""
			if rf.score > 0.5 {
				marker = " ⚠️ HIGH SUSPICION"
			}
			sb.WriteString(fmt.Sprintf("\n### %s (score: %.2f)%s\n", rf.file, rf.score, marker))
			sb.WriteString(pack.Skeletons[rf.file])
			sb.WriteString("\n")
		} else {
			if i == maxFullSkeletons {
				sb.WriteString("\n### (remaining files — lower relevance, skeletons omitted)\n")
			}
			sb.WriteString(fmt.Sprintf("  - %s (score: %.2f)\n", rf.file, rf.score))
		}
	}

	// Log extractions
	if len(pack.LogExtractions) > 0 {
		sb.WriteString("\n--- EXTRACTED ERROR SPANS (GLiNER) ---\n")
		for logFile, extraction := range pack.LogExtractions {
			sb.WriteString(fmt.Sprintf("\n### %s\n", logFile))
			sb.WriteString(extraction)
			sb.WriteString("\n")
		}
	}

	// Laya relevance scores
	if len(pack.LayaScores) > 0 {
		sb.WriteString("\n--- LAYA BUG RELEVANCE SCORES (System 1 local inference) ---\n")
		sb.WriteString("Files ranked by likelihood of containing a bug (0.0 = clean, 1.0 = suspicious):\n")
		// Sort by score descending for the LLM to focus on top files
		type fs struct {
			file  string
			score float64
		}
		var sorted []fs
		for f, s := range pack.LayaScores {
			sorted = append(sorted, fs{f, s})
		}
		for i := 0; i < len(sorted); i++ {
			for j := i + 1; j < len(sorted); j++ {
				if sorted[j].score > sorted[i].score {
					sorted[i], sorted[j] = sorted[j], sorted[i]
				}
			}
		}
		for _, s := range sorted {
			marker := "  "
			if s.score > 0.6 {
				marker = "⚠️"
			}
			sb.WriteString(fmt.Sprintf("  %s %.2f  %s\n", marker, s.score, s.file))
		}
	}

	sb.WriteString("\n=== END CONTEXT PACK ===\n")
	sb.WriteString("Use tzro_expand to inspect specific function bodies by hash.\n")
	sb.WriteString("Use read_file for configs, READMEs, and full log files if needed.\n")

	return sb.String()
}

// ---------------------------------------------------------------------------
// Graph-based discovery using the executor engine
// ---------------------------------------------------------------------------

// buildDiscoveryGraph creates a DAG that probes + skeletons in parallel.
// This is used when the full graph executor is available.
func buildDiscoveryGraph(sourceFiles []string) *executor.Graph {
	nodes := []executor.Node{
		{
			ID:   "probe_errors",
			Type: executor.NodeTypeTool,
			Tool: "probe",
			Args: map[string]interface{}{
				"query": "error test bug fail panic FAIL",
			},
		},
	}

	// Add skeleton nodes for each source file, depending on probe
	for i, f := range sourceFiles {
		nodes = append(nodes, executor.Node{
			ID:        fmt.Sprintf("skeleton_%d", i),
			Type:      executor.NodeTypeTool,
			Tool:      "skeleton",
			DependsOn: []string{"probe_errors"},
			Args: map[string]interface{}{
				"file": f,
			},
		})
	}

	returns := []string{"probe_errors"}
	for i := range sourceFiles {
		returns = append(returns, fmt.Sprintf("skeleton_%d", i))
	}

	return &executor.Graph{
		Version: "1.0",
		TaskID:  "audit_discovery",
		Nodes:   nodes,
		Returns: returns,
	}
}

// runDiscoveryWithEngine uses the graph executor for parallel discovery.
// Falls back gracefully if store initialization fails.
func runDiscoveryWithEngine(t *testing.T, workspaceDir string, sourceFiles []string) map[string]string {
	t.Helper()
	ctx := context.Background()

	// Initialize the store for skeleton hash storage
	dbPath := filepath.Join(t.TempDir(), "discovery.db")
	s, err := store.OpenStore(dbPath)
	if err != nil {
		t.Logf("discovery engine: store open failed, skipping engine: %v", err)
		return nil
	}
	defer s.Close()

	dispatcher := executor.NewBuiltinDispatcher(workspaceDir, s)
	engine := executor.NewEngine(
		executor.WithMaxConcurrency(4),
		executor.WithToolDispatcher(dispatcher),
	)

	graph := buildDiscoveryGraph(sourceFiles)
	result, err := engine.Execute(ctx, graph)
	if err != nil {
		t.Logf("discovery engine: execution failed: %v", err)
		return nil
	}

	skeletons := make(map[string]string)
	for id, output := range result.Outputs {
		if strings.HasPrefix(id, "skeleton_") && output.Status == "completed" {
			if stdout, ok := output.Data["stdout"].(string); ok {
				// Extract the file name from the skeleton node
				for _, node := range graph.Nodes {
					if node.ID == id {
						if f, ok := node.Args["file"].(string); ok {
							skeletons[f] = stdout
						}
					}
				}
			}
		}
	}

	t.Logf("discovery engine: completed %d/%d nodes", len(result.Outputs), len(graph.Nodes))
	return skeletons
}

// Ensure the executor and store imports are used.
var _ = (*executor.Engine)(nil)
var _ = (*store.Store)(nil)
var _ json.Marshaler = nil
