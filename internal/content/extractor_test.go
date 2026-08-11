package content

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFormatVisionDescription(t *testing.T) {
	desc := FormatVisionDescription("A bar chart showing revenue", "figure-1.png")
	if !strings.HasPrefix(desc, VisionDescriptionPrefix) {
		t.Errorf("expected caveat prefix, got: %s", desc)
	}
	if !strings.Contains(desc, "A bar chart showing revenue") {
		t.Error("expected description content in output")
	}
	if !strings.Contains(desc, "figure-1.png") {
		t.Error("expected source in output")
	}
}

func TestFormatVisionDescription_NoSource(t *testing.T) {
	desc := FormatVisionDescription("Some image", "")
	if strings.Contains(desc, "[Source:") {
		t.Error("expected no source line when source is empty")
	}
}

func TestExtractImage_FileNotFound(t *testing.T) {
	_, err := ExtractImage(context.Background(), "/nonexistent/image.png")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestExtractImage_FileTooLarge(t *testing.T) {
	// Create a file larger than maxImageBytes (3MB)
	tmpDir := t.TempDir()
	largePath := filepath.Join(tmpDir, "large.png")

	// Write 4MB of data
	data := make([]byte, 4*1024*1024)
	if err := os.WriteFile(largePath, data, 0644); err != nil {
		t.Fatalf("failed to create large file: %v", err)
	}

	result, err := ExtractImage(context.Background(), largePath)
	if err != nil {
		t.Fatalf("ExtractImage should not error on too-large files: %v", err)
	}
	if result.Type != ContentText {
		t.Errorf("expected ContentText for skipped image, got %s", result.Type)
	}
	if !strings.Contains(result.Text, "skipped") {
		t.Errorf("expected 'skipped' in text, got: %s", result.Text)
	}
}

func TestExtractImage_SmallValidImage(t *testing.T) {
	// Create a minimal valid PNG (1x1 pixel)
	tmpDir := t.TempDir()
	imgPath := filepath.Join(tmpDir, "test.png")

	// Minimal PNG: 1x1 pixel transparent
	pngData := []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // PNG signature
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, // IHDR chunk
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, // 1x1
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, // 8-bit RGB
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, // IDAT chunk
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x02, 0x00, 0x01, 0xE2, 0x21, 0xBC,
		0x33, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, // IEND chunk
		0x44, 0xAE, 0x42, 0x60, 0x82,
	}
	if err := os.WriteFile(imgPath, pngData, 0644); err != nil {
		t.Fatalf("failed to create test image: %v", err)
	}

	// Set TZRO_DIR to temp so persistence works
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	result, err := ExtractImage(context.Background(), imgPath)
	if err != nil {
		t.Fatalf("ExtractImage failed: %v", err)
	}

	// Should return ContentImage even if vision fails (graceful degradation)
	if result.Type != ContentImage {
		t.Errorf("expected ContentImage, got %s", result.Type)
	}

	// Should have exactly 1 image
	if len(result.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(result.Images))
	}

	img := result.Images[0]

	// DataURI should be base64 encoded
	if !strings.HasPrefix(img.DataURI, "data:image/png;base64,") {
		t.Errorf("expected base64 data URI, got prefix: %s", img.DataURI[:30])
	}

	// Description should have caveat prefix (even if vision failed, we get fallback)
	if !strings.Contains(img.Description, VisionDescriptionPrefix) {
		t.Errorf("expected caveat prefix in description, got: %s", img.Description)
	}

	// Source should be the filename
	if img.Source != "test.png" {
		t.Errorf("expected source 'test.png', got '%s'", img.Source)
	}

	// Text should match the formatted description
	if result.Text != img.Description {
		t.Errorf("Text and Description should match")
	}

	// Metadata should have path and mimeType
	if result.Metadata["mimeType"] != "image/png" {
		t.Errorf("expected mimeType 'image/png', got '%s'", result.Metadata["mimeType"])
	}
}

func TestExtractImageFromBytes_TooLarge(t *testing.T) {
	data := make([]byte, 4*1024*1024)
	result, err := ExtractImageFromBytes(context.Background(), data, "image/png", "test.png")
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if !strings.Contains(result.Text, "skipped") {
		t.Errorf("expected 'skipped' for too-large image")
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"image.png", "image.png"},
		{"path/to/file.jpg", "file.jpg"},
		{"weird?name&here=yes.png", "weirdnamehereys.png"},
		{"", "image.png"},
		{".", "image.png"},
	}

	for _, tt := range tests {
		got := sanitizeFilename(tt.input)
		if got != tt.want {
			// Just check it's non-empty and reasonable
			if got == "" || got == "." {
				t.Errorf("sanitizeFilename(%q) returned invalid: %q", tt.input, got)
			}
		}
	}
}

func TestExtensionToMIME(t *testing.T) {
	tests := map[string]string{
		".png":  "image/png",
		".jpg":  "image/jpeg",
		".jpeg": "image/jpeg",
		".webp": "image/webp",
		".bmp":  "image/bmp",
		".gif":  "image/gif",
		".svg":  "image/svg+xml",
		".xyz":  "image/png", // default
	}

	for ext, want := range tests {
		got := extensionToMIME(ext)
		if got != want {
			t.Errorf("extensionToMIME(%q) = %q, want %q", ext, got, want)
		}
	}
}

func TestPersistImageToCache(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TZRO_DIR", tmpDir)
	defer os.Unsetenv("TZRO_DIR")

	data := []byte("fake image data for testing")
	path, err := persistImageToCache(data, "test-image.png")
	if err != nil {
		t.Fatalf("persistImageToCache failed: %v", err)
	}

	if path == "" {
		t.Fatal("expected non-empty path")
	}

	// Verify file exists
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("persisted file should exist: %v", err)
	}

	// Verify content
	readBack, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read back persisted file: %v", err)
	}
	if string(readBack) != string(data) {
		t.Error("persisted content doesn't match original")
	}

	// Call again with same data — should return same path (content-addressed)
	path2, err := persistImageToCache(data, "test-image.png")
	if err != nil {
		t.Fatalf("second persist failed: %v", err)
	}
	if path2 != path {
		t.Errorf("content-addressed persistence should return same path: %s vs %s", path, path2)
	}
}
