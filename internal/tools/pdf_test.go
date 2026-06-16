package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildPDFBytes generates a minimal valid PDF 1.4 file containing "Hello World"
// with correct cross-reference table byte offsets.
func buildPDFBytes() []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)

	// Object 1: Catalog
	offsets[1] = buf.Len()
	buf.WriteString("1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n")

	// Object 2: Pages
	offsets[2] = buf.Len()
	buf.WriteString("2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n")

	// Object 3: Page
	offsets[3] = buf.Len()
	buf.WriteString("3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>\nendobj\n")

	// Object 4: Font
	offsets[4] = buf.Len()
	buf.WriteString("4 0 obj\n<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>\nendobj\n")

	// Object 5: Content stream
	offsets[5] = buf.Len()
	content := "BT\n/F1 24 Tf\n100 700 Td\n(Hello World) Tj\nET\n"
	buf.WriteString(fmt.Sprintf("5 0 obj\n<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(content), content))

	// xref
	xrefOffset := buf.Len()
	buf.WriteString("xref\n0 6\n")
	buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= 5; i++ {
		buf.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[i]))
	}

	buf.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\n")
	buf.WriteString(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset))

	return buf.Bytes()
}

func TestParsePDF_DigitalTextExtraction(t *testing.T) {
	// 1. Create a temporary PDF file
	tempDir := t.TempDir()
	pdfPath := filepath.Join(tempDir, "test.pdf")
	pdfBytes := buildPDFBytes()
	if err := os.WriteFile(pdfPath, pdfBytes, 0644); err != nil {
		t.Fatalf("failed to write minimal PDF: %v", err)
	}

	// 2. Execute ParsePDF
	ctx := context.Background()
	text, err := ParsePDF(ctx, pdfPath)
	if err != nil {
		t.Fatalf("ParsePDF returned error: %v", err)
	}

	// 3. Verify extracted text
	expectedText := "Hello World"
	if !strings.Contains(text, expectedText) {
		t.Errorf("expected text %q not found in output: %q", expectedText, text)
	}
}

func TestParsePDF_NonExistentFile(t *testing.T) {
	ctx := context.Background()
	_, err := ParsePDF(ctx, "non_existent_file.pdf")
	if err == nil {
		t.Error("expected error for non-existent file, got nil")
	}
}
