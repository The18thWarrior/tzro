package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Slice 1: ≤5 subdirs → not breadth mode ────────────────────────────────

func TestDetectBreadthMode_FewSubdirs_NotBreadth(t *testing.T) {
	// Create a temp dir with ≤5 subdirs
	dir := t.TempDir()
	for i := 0; i < 4; i++ {
		os.Mkdir(filepath.Join(dir, fmt.Sprintf("pkg%d", i)), 0o755)
	}

	isBreadth, subdirCount, _ := DetectBreadthMode([]string{dir})

	if isBreadth {
		t.Fatal("expected isBreadth=false for ≤5 subdirs")
	}
	if subdirCount != 4 {
		t.Fatalf("expected subdirCount=4, got %d", subdirCount)
	}
}

// ─── Slice 2: >5 subdirs → breadth mode with manifest ──────────────────────

func TestDetectBreadthMode_ManySubdirs_IsBreadth(t *testing.T) {
	dir := t.TempDir()
	subdirNames := []string{"api", "auth", "billing", "config", "db", "handlers", "middleware", "models"}
	for _, name := range subdirNames {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}

	isBreadth, subdirCount, manifest := DetectBreadthMode([]string{dir})

	if !isBreadth {
		t.Fatal("expected isBreadth=true for >5 subdirs")
	}
	if subdirCount != 8 {
		t.Fatalf("expected subdirCount=8, got %d", subdirCount)
	}

	// Manifest should contain all subdir names
	for _, name := range subdirNames {
		if !strings.Contains(manifest, name) {
			t.Fatalf("manifest missing subdir %q, got: %s", name, manifest)
		}
	}
}

// ─── Slice 3: ScaleStepBudget scales correctly ─────────────────────────────

func TestScaleStepBudget_ScalesCorrectly(t *testing.T) {
	result := ScaleStepBudget(24, 15, 60)
	expected := 24 + 15*2 // = 54

	if result != expected {
		t.Fatalf("expected %d, got %d", expected, result)
	}
}

// ─── Slice 4: ScaleStepBudget caps at max ───────────────────────────────────

func TestScaleStepBudget_CapsAtMax(t *testing.T) {
	result := ScaleStepBudget(24, 30, 60)

	if result != 60 {
		t.Fatalf("expected cap at 60, got %d", result)
	}
}

// ─── Slice 5: Non-existent path → graceful degradation ─────────────────────

func TestDetectBreadthMode_NonExistentPath_Degrades(t *testing.T) {
	isBreadth, subdirCount, manifest := DetectBreadthMode([]string{"/nonexistent/path/that/doesnt/exist"})

	if isBreadth {
		t.Fatal("expected isBreadth=false for non-existent path")
	}
	if subdirCount != 0 {
		t.Fatalf("expected subdirCount=0, got %d", subdirCount)
	}
	if manifest != "" {
		t.Fatalf("expected empty manifest, got: %q", manifest)
	}
}

// ─── Slice 6: Manifest format validation ────────────────────────────────────

func TestDetectBreadthMode_ManifestFormat(t *testing.T) {
	dir := t.TempDir()
	names := []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"}
	for _, name := range names {
		os.Mkdir(filepath.Join(dir, name), 0o755)
	}
	// Also create a file (should not appear in manifest)
	os.WriteFile(filepath.Join(dir, "README.md"), []byte("hello"), 0o644)

	_, _, manifest := DetectBreadthMode([]string{dir})

	// Each subdir should be on its own line
	lines := strings.Split(strings.TrimSpace(manifest), "\n")
	if len(lines) != len(names) {
		t.Fatalf("expected %d lines in manifest, got %d: %q", len(names), len(lines), manifest)
	}

	// Manifest should NOT contain the file
	if strings.Contains(manifest, "README.md") {
		t.Fatal("manifest should only list directories, not files")
	}
}
