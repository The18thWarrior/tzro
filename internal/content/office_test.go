package content

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// createTestDOCX creates a minimal DOCX file for testing.
// A DOCX is a ZIP archive with word/document.xml containing <w:t> text elements.
func createTestDOCX(t *testing.T, dir string, paragraphs []string) string {
	t.Helper()
	docPath := filepath.Join(dir, "test.docx")
	f, err := os.Create(docPath)
	if err != nil {
		t.Fatalf("failed to create test docx: %v", err)
	}

	w := zip.NewWriter(f)

	// Build document.xml content
	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	xml.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	xml.WriteString(`<w:body>`)
	for _, p := range paragraphs {
		xml.WriteString(`<w:p><w:r><w:t>`)
		xml.WriteString(p)
		xml.WriteString(`</w:t></w:r></w:p>`)
	}
	xml.WriteString(`</w:body></w:document>`)

	fw, err := w.Create("word/document.xml")
	if err != nil {
		t.Fatalf("failed to create document.xml entry: %v", err)
	}
	fw.Write([]byte(xml.String()))

	w.Close()
	f.Close()

	return docPath
}

// createTestPPTX creates a minimal PPTX file for testing.
func createTestPPTX(t *testing.T, dir string, slides [][]string) string {
	t.Helper()
	pptxPath := filepath.Join(dir, "test.pptx")
	f, err := os.Create(pptxPath)
	if err != nil {
		t.Fatalf("failed to create test pptx: %v", err)
	}

	w := zip.NewWriter(f)

	for i, slide := range slides {
		var xml strings.Builder
		xml.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
		xml.WriteString(`<p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main">`)
		xml.WriteString(`<p:cSld><p:spTree>`)
		for _, text := range slide {
			xml.WriteString(`<p:sp><p:txBody>`)
			xml.WriteString(`<a:p><a:r><a:t>`)
			xml.WriteString(text)
			xml.WriteString(`</a:t></a:r></a:p>`)
			xml.WriteString(`</p:txBody></p:sp>`)
		}
		xml.WriteString(`</p:spTree></p:cSld></p:sld>`)

		name := filepath.Join("ppt", "slides", "slide"+string(rune('1'+i))+".xml")
		fw, err := w.Create(name)
		if err != nil {
			t.Fatalf("failed to create slide entry: %v", err)
		}
		fw.Write([]byte(xml.String()))
	}

	w.Close()
	f.Close()

	return pptxPath
}

func TestExtractOffice_DOCX(t *testing.T) {
	tmpDir := t.TempDir()
	docPath := createTestDOCX(t, tmpDir, []string{
		"Hello, World!",
		"This is a test document.",
		"Third paragraph here.",
	})

	result, err := ExtractOffice(docPath)
	if err != nil {
		t.Fatalf("ExtractOffice failed: %v", err)
	}

	if result.Type != ContentText {
		t.Errorf("expected ContentText (no embedded images), got %s", result.Type)
	}

	if !strings.Contains(result.Text, "Hello, World!") {
		t.Errorf("expected text to contain 'Hello, World!', got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "This is a test document.") {
		t.Errorf("expected text to contain second paragraph")
	}
	if !strings.Contains(result.Text, "Third paragraph here.") {
		t.Errorf("expected text to contain third paragraph")
	}

	if result.Metadata["format"] != "docx" {
		t.Errorf("expected format 'docx', got '%s'", result.Metadata["format"])
	}
}

func TestExtractOffice_PPTX(t *testing.T) {
	tmpDir := t.TempDir()
	pptxPath := createTestPPTX(t, tmpDir, [][]string{
		{"Title Slide", "Welcome to the presentation"},
		{"Slide 2", "Key findings"},
	})

	result, err := ExtractOffice(pptxPath)
	if err != nil {
		t.Fatalf("ExtractOffice failed: %v", err)
	}

	if !strings.Contains(result.Text, "Title Slide") {
		t.Errorf("expected text to contain 'Title Slide', got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Welcome to the presentation") {
		t.Errorf("expected text to contain 'Welcome to the presentation'")
	}
	if !strings.Contains(result.Text, "Slide 2") {
		t.Errorf("expected text to contain 'Slide 2'")
	}
	if !strings.Contains(result.Text, "Key findings") {
		t.Errorf("expected text to contain 'Key findings'")
	}

	// Should have slide markers
	if !strings.Contains(result.Text, "--- Slide 1 ---") {
		t.Errorf("expected slide markers in output")
	}

	if result.Metadata["format"] != "pptx" {
		t.Errorf("expected format 'pptx', got '%s'", result.Metadata["format"])
	}
	if result.Metadata["slideCount"] != "2" {
		t.Errorf("expected slideCount '2', got '%s'", result.Metadata["slideCount"])
	}
}

func TestExtractOffice_UnsupportedFormat(t *testing.T) {
	_, err := ExtractOffice("/fake/path.xlsx")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported' in error, got: %v", err)
	}
}

func TestExtractOffice_NonexistentFile(t *testing.T) {
	_, err := ExtractOffice("/nonexistent/file.docx")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
}

func TestExtractOffice_EmptyDOCX(t *testing.T) {
	tmpDir := t.TempDir()
	docPath := createTestDOCX(t, tmpDir, []string{})

	result, err := ExtractOffice(docPath)
	if err != nil {
		t.Fatalf("ExtractOffice failed on empty doc: %v", err)
	}

	// Empty document should not error but may have empty text
	if result.Type != ContentText {
		t.Errorf("expected ContentText for empty doc, got %s", result.Type)
	}
}

func TestStripPrefix(t *testing.T) {
	tests := map[string]string{
		"w:t":   "t",
		"a:p":   "p",
		"plain": "plain",
		"x:y:z": "y:z",
	}
	for input, want := range tests {
		got := stripPrefix(input)
		if got != want {
			t.Errorf("stripPrefix(%q) = %q, want %q", input, got, want)
		}
	}
}
