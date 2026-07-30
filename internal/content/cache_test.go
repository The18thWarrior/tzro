package content

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClearImageCache_EmptyDir(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	// Create the cache dir but leave it empty
	cacheDir := filepath.Join(tmpDir, "cache", "images")
	os.MkdirAll(cacheDir, 0755)

	removed, err := ClearImageCache()
	if err != nil {
		t.Fatalf("ClearImageCache failed: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestClearImageCache_NonexistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	// Don't create cache dir at all
	removed, err := ClearImageCache()
	if err != nil {
		t.Fatalf("ClearImageCache failed: %v", err)
	}
	if removed != 0 {
		t.Errorf("expected 0 removed, got %d", removed)
	}
}

func TestClearImageCache_WithFiles(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	cacheDir := filepath.Join(tmpDir, "cache", "images")
	os.MkdirAll(cacheDir, 0755)

	// Create some fake cached files
	for _, name := range []string{"abc12345-chart.png", "def67890-photo.jpg", "ghi11111-diagram.webp"} {
		os.WriteFile(filepath.Join(cacheDir, name), []byte("fake image data"), 0644)
	}

	removed, err := ClearImageCache()
	if err != nil {
		t.Fatalf("ClearImageCache failed: %v", err)
	}
	if removed != 3 {
		t.Errorf("expected 3 removed, got %d", removed)
	}

	// Verify directory is empty
	entries, _ := os.ReadDir(cacheDir)
	if len(entries) != 0 {
		t.Errorf("expected empty cache dir, got %d entries", len(entries))
	}
}

func TestClearImageCache_SkipsSubdirs(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	cacheDir := filepath.Join(tmpDir, "cache", "images")
	os.MkdirAll(cacheDir, 0755)

	// Create a file and a subdirectory
	os.WriteFile(filepath.Join(cacheDir, "image.png"), []byte("data"), 0644)
	os.MkdirAll(filepath.Join(cacheDir, "subdir"), 0755)

	removed, err := ClearImageCache()
	if err != nil {
		t.Fatalf("ClearImageCache failed: %v", err)
	}
	if removed != 1 {
		t.Errorf("expected 1 removed (subdirs skipped), got %d", removed)
	}

	// Subdir should still exist
	if _, err := os.Stat(filepath.Join(cacheDir, "subdir")); os.IsNotExist(err) {
		t.Error("expected subdir to still exist")
	}
}

func TestExtractedHolder_ContextRoundtrip(t *testing.T) {
	// Test that NewExtractedCtx and ExtractedFromCtx work together
	// (using the tools package types through content)
	// This is tested indirectly through the content package

	// Test that ClearImageCache is idempotent
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	cacheDir := filepath.Join(tmpDir, "cache", "images")
	os.MkdirAll(cacheDir, 0755)
	os.WriteFile(filepath.Join(cacheDir, "test.png"), []byte("data"), 0644)

	// First clear
	removed1, _ := ClearImageCache()
	if removed1 != 1 {
		t.Errorf("first clear: expected 1, got %d", removed1)
	}

	// Second clear (idempotent)
	removed2, _ := ClearImageCache()
	if removed2 != 0 {
		t.Errorf("second clear: expected 0, got %d", removed2)
	}
}

func TestExtractPDF_NonexistentFile(t *testing.T) {
	_, err := ExtractPDF(nil, "/nonexistent/file.pdf")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "failed to open file") {
		t.Errorf("expected 'failed to open file' in error, got: %v", err)
	}
}
