package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"tzro/pkg/ast"
	"tzro/pkg/compactor"
	"tzro/pkg/store"
)

// ProcessToolOutput preserves explicit slices. Whole source reads can become
// structural views only when every elided body is recoverable from the store.
func ProcessToolOutput(name string, args map[string]any, input string, s *store.Store) string {
	start := time.Now()
	output, status, transform := input, "unchanged", "none"
	defer func() {
		path := os.Getenv("TZRO_RUNTIME_TRACE")
		if path == "" {
			return
		}
		event, _ := json.Marshal(map[string]any{"runtime": "hook:post-tool", "status": status, "tool_name": name,
			"transform": transform, "input_bytes": len(input), "output_bytes": len(output), "duration_ms": time.Since(start).Milliseconds()})
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
			_, _ = f.Write(append(event, '\n'))
			_ = f.Close()
		}
	}()
	_, offset := args["offset"]
	_, limit := args["limit"]
	if compactor.IsFileReadTool(name) && (offset || limit) {
		return output
	}
	path, _ := args["path"].(string)
	if path == "" {
		path, _ = args["file_path"].(string)
	}
	if s != nil && compactor.IsFileReadTool(name) && path != "" && len(input) >= 8192 && strings.Count(input, "\n") >= 200 {
		// A truncated or decorated native read is not a complete source file.
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() == int64(len(input)) {
			cwd, _ := os.Getwd()
			result, err := ast.Skeletonize(path, []byte(input), s, cwd)
			if err == nil && result.ElidedBlocks > 0 {
				// The skeletonizer can return a view after a store write fails.
				// Automatic substitution requires verified recovery for every body.
				for _, hash := range result.Hashes {
					if _, err := s.GetBlob(hash); err != nil {
						status, transform = "failed", "raw_fallback"
						return output
					}
				}
				view := fmt.Sprintf("// Tzro structural view of %s. Function bodies are retained locally.\n// Use tzro expand <hash> for an implementation, or read with offset/limit for exact source lines.\n%s", path, result.SkeletonCode)
				if len(view) < len(input) {
					output, status, transform = view, "transformed", "source_skeleton"
					return output
				}
			}
		}
	}
	processed := CompactOrIntercept(input, name, s)
	if len(processed) < len(input) {
		output, status, transform = processed, "transformed", "compact_or_tabular"
	}
	return output
}
