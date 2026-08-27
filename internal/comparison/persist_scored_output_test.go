package comparison

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPersistScoredOutput validates that persistScoredOutput overwrites
// truncated write_file output with the full scored synthesis content.
func TestPersistScoredOutput(t *testing.T) {
	t.Run("OverwritesTruncatedFile", func(t *testing.T) {
		dir := t.TempDir()
		subDir := filepath.Join(dir, "internal", "cache")
		_ = os.MkdirAll(subDir, 0755)

		// Simulate the truncated write_file output (558 bytes)
		truncatedContent := "# Cache Package\n\n## PruneColumns\n- **Sig**: `func PruneColumns(...)`\n"
		truncatedPath := filepath.Join(subDir, "index.md")
		_ = os.WriteFile(truncatedPath, []byte(truncatedContent), 0644)

		// Full synthesis content (9K+ chars)
		fullSynthesis := strings.Repeat("# Full Exported Symbol Index\n\n", 100) + "## CacheStore\n```go\ntype CacheStore interface { ... }\n```\n"

		task := ComparisonTask{
			ID:          "cache_function_index",
			Category:    CategoryDocgen,
			TargetPaths: []string{"internal/cache/"},
		}

		persistScoredOutput(dir, task, fullSynthesis)

		// Verify the file was overwritten with the full synthesis
		data, err := os.ReadFile(truncatedPath)
		if err != nil {
			t.Fatalf("Failed to read overwritten file: %v", err)
		}

		if string(data) != fullSynthesis {
			t.Errorf("File content mismatch: got %d chars, want %d chars", len(data), len(fullSynthesis))
		}

		if len(data) <= len(truncatedContent) {
			t.Errorf("File was not overwritten: got %d chars, truncated was %d chars", len(data), len(truncatedContent))
		}
	})

	t.Run("CreatesFileWhenNoneExists", func(t *testing.T) {
		dir := t.TempDir()
		fullSynthesis := "# Full output content here\n\nDetailed analysis..."

		task := ComparisonTask{
			ID:          "test_task",
			Category:    CategoryDocgen,
			TargetPaths: []string{"docs/"},
		}

		persistScoredOutput(dir, task, fullSynthesis)

		expectedPath := filepath.Join(dir, "docs", "scored_output.md")
		data, err := os.ReadFile(expectedPath)
		if err != nil {
			t.Fatalf("Expected scored_output.md to be created at %s: %v", expectedPath, err)
		}

		if string(data) != fullSynthesis {
			t.Errorf("File content mismatch: got %q, want %q", string(data), fullSynthesis)
		}
	})

	t.Run("NoopOnEmptyContent", func(t *testing.T) {
		dir := t.TempDir()
		task := ComparisonTask{ID: "test_task", Category: CategoryDocgen}

		// Should not panic or create files
		persistScoredOutput(dir, task, "")
		persistScoredOutput("", task, "some content")

		entries, _ := os.ReadDir(dir)
		if len(entries) > 0 {
			t.Errorf("Expected empty dir, got %d entries", len(entries))
		}
	})
}
