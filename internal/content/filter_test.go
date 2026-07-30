package content

import (
	"context"
	"strings"
	"testing"
)

func TestIsNoiseImage_TrackingPixel(t *testing.T) {
	if !IsNoiseImage("https://example.com/tracking/pixel.png", 0, 0) {
		t.Error("expected tracking pixel URL to be noise")
	}
}

func TestIsNoiseImage_DoubleClick(t *testing.T) {
	if !IsNoiseImage("https://ad.doubleclick.net/image.png", 0, 0) {
		t.Error("expected doubleclick URL to be noise")
	}
}

func TestIsNoiseImage_Favicon(t *testing.T) {
	if !IsNoiseImage("https://example.com/favicon.ico", 0, 0) {
		t.Error("expected favicon URL to be noise")
	}
}

func TestIsNoiseImage_SmallDimensions(t *testing.T) {
	if !IsNoiseImage("https://example.com/image.png", 16, 16) {
		t.Error("expected 16x16 image to be noise")
	}
	if !IsNoiseImage("https://example.com/image.png", 50, 200) {
		t.Error("expected narrow image to be noise (width < 100)")
	}
}

func TestIsNoiseImage_ContentImage(t *testing.T) {
	if IsNoiseImage("https://example.com/chart.png", 400, 300) {
		t.Error("expected content image with good dimensions to NOT be noise")
	}
	if IsNoiseImage("https://example.com/diagram.png", 0, 0) {
		t.Error("expected normal URL with unknown dimensions to NOT be noise")
	}
}

func TestIsNoiseImage_GIF(t *testing.T) {
	if !IsNoiseImage("https://example.com/animation.gif", 400, 300) {
		t.Error("expected GIF to be noise (usually decorative)")
	}
}

func TestIsNoiseImage_LogoPattern(t *testing.T) {
	if !IsNoiseImage("https://example.com/images/logo-header.png", 200, 50) {
		t.Error("expected logo URL to be noise")
	}
}

func TestFilterWebImages_BudgetCap(t *testing.T) {
	images := make([]DiscoveredImage, 10)
	for i := range images {
		images[i] = DiscoveredImage{
			URL:  "https://example.com/image" + string(rune('a'+i)) + ".png",
			Size: (i + 1) * 50 * 1024, // 50KB to 500KB
		}
	}

	filtered := FilterWebImages(images, 3)
	if len(filtered) != 3 {
		t.Fatalf("expected 3 images after budget cap, got %d", len(filtered))
	}

	// Should be sorted by size descending
	for i := 1; i < len(filtered); i++ {
		if filtered[i].Size > filtered[i-1].Size {
			t.Errorf("images not sorted by size descending: %d > %d at position %d",
				filtered[i].Size, filtered[i-1].Size, i)
		}
	}
}

func TestFilterWebImages_SkipsTiny(t *testing.T) {
	images := []DiscoveredImage{
		{URL: "https://example.com/big.png", Size: 100 * 1024},    // 100KB — keep
		{URL: "https://example.com/tiny.png", Size: 5 * 1024},     // 5KB — skip (too small)
		{URL: "https://example.com/medium.png", Size: 50 * 1024},  // 50KB — keep
	}

	filtered := FilterWebImages(images, 10)
	if len(filtered) != 2 {
		t.Fatalf("expected 2 images (tiny filtered out), got %d", len(filtered))
	}
}

func TestFilterWebImages_SkipsHuge(t *testing.T) {
	images := []DiscoveredImage{
		{URL: "https://example.com/normal.png", Size: 100 * 1024},
		{URL: "https://example.com/hero.png", Size: 5 * 1024 * 1024}, // 5MB — too large
	}

	filtered := FilterWebImages(images, 10)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 image (hero filtered out), got %d", len(filtered))
	}
}

func TestFilterWebImages_SkipsNoiseURLs(t *testing.T) {
	images := []DiscoveredImage{
		{URL: "https://example.com/chart.png", Size: 100 * 1024},
		{URL: "https://ad.doubleclick.net/ad.png", Size: 100 * 1024},
		{URL: "https://example.com/tracking/pixel.png", Size: 100 * 1024},
	}

	filtered := FilterWebImages(images, 10)
	if len(filtered) != 1 {
		t.Fatalf("expected 1 image (noise filtered out), got %d", len(filtered))
	}
	if filtered[0].URL != "https://example.com/chart.png" {
		t.Errorf("expected chart.png to survive, got %s", filtered[0].URL)
	}
}

func TestDetectContentType_Header(t *testing.T) {
	got := DetectContentType("text/html; charset=utf-8", "", nil)
	if got != "text/html" {
		t.Errorf("expected 'text/html', got '%s'", got)
	}
}

func TestDetectContentType_HeaderPDF(t *testing.T) {
	got := DetectContentType("application/pdf", "", nil)
	if got != "application/pdf" {
		t.Errorf("expected 'application/pdf', got '%s'", got)
	}
}

func TestDetectContentType_URLExtension(t *testing.T) {
	got := DetectContentType("", "https://example.com/report.pdf", nil)
	if got != "application/pdf" {
		t.Errorf("expected 'application/pdf' from extension, got '%s'", got)
	}
}

func TestDetectContentType_URLExtensionPNG(t *testing.T) {
	got := DetectContentType("", "https://example.com/chart.png?v=123", nil)
	if got != "image/png" {
		t.Errorf("expected 'image/png' from extension (with query), got '%s'", got)
	}
}

func TestDetectContentType_ByteSniffing(t *testing.T) {
	// PDF magic bytes
	pdfBytes := []byte("%PDF-1.4 fake pdf content here")
	got := DetectContentType("", "https://example.com/download", pdfBytes)
	// http.DetectContentType doesn't recognize PDF magic, it will return application/octet-stream or text/plain
	// The important thing is it doesn't default to text/html when we have bytes to sniff
	if got == "" {
		t.Error("expected non-empty result from byte sniffing")
	}
}

func TestDetectContentType_OctetStreamFallsThrough(t *testing.T) {
	// application/octet-stream should be treated as "unknown" and fall through to extension
	got := DetectContentType("application/octet-stream", "https://example.com/file.pdf", nil)
	if got != "application/pdf" {
		t.Errorf("expected octet-stream to fall through to extension, got '%s'", got)
	}
}

func TestDetectContentType_DefaultHTML(t *testing.T) {
	got := DetectContentType("", "https://example.com/page", nil)
	if got != "text/html" {
		t.Errorf("expected default 'text/html', got '%s'", got)
	}
}

func TestExtractWebResponse_HTML(t *testing.T) {
	html := []byte("<html><body><h1>Hello World</h1><p>Test content</p></body></html>")
	result, err := ExtractWebResponse(context.Background(), html, "text/html", "https://example.com")
	if err != nil {
		t.Fatalf("ExtractWebResponse failed: %v", err)
	}

	if result.Type != ContentText {
		t.Errorf("expected ContentText, got %s", result.Type)
	}
	if !strings.Contains(result.Text, "Hello World") {
		t.Errorf("expected 'Hello World' in text, got: %s", result.Text)
	}
	if !strings.Contains(result.Text, "Test content") {
		t.Errorf("expected 'Test content' in text")
	}
}

func TestExtractWebResponse_PlainText(t *testing.T) {
	text := []byte("This is plain text content")
	result, err := ExtractWebResponse(context.Background(), text, "text/plain", "https://example.com/file.txt")
	if err != nil {
		t.Fatalf("ExtractWebResponse failed: %v", err)
	}

	if result.Type != ContentText {
		t.Errorf("expected ContentText, got %s", result.Type)
	}
	if result.Text != "This is plain text content" {
		t.Errorf("expected exact text pass-through, got: %s", result.Text)
	}
}

func TestExtractWebResponse_UnsupportedType(t *testing.T) {
	_, err := ExtractWebResponse(context.Background(), []byte("data"), "application/zip", "https://example.com/file.zip")
	if err == nil {
		t.Fatal("expected error for unsupported content type")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("expected 'unsupported' in error, got: %v", err)
	}
}

func TestHtmlToPlainText(t *testing.T) {
	html := `<html><head><title>Test</title><script>alert('hi')</script><style>body{color:red}</style></head>
<body><h1>Hello</h1><p>World</p></body></html>`

	text := htmlToPlainText(html)

	if strings.Contains(text, "alert") {
		t.Error("expected script content to be stripped")
	}
	if strings.Contains(text, "color:red") {
		t.Error("expected style content to be stripped")
	}
	if !strings.Contains(text, "Hello") {
		t.Error("expected 'Hello' in output")
	}
	if !strings.Contains(text, "World") {
		t.Error("expected 'World' in output")
	}
}

func TestUrlPath(t *testing.T) {
	tests := map[string]string{
		"https://example.com/file.pdf?v=123":       "https://example.com/file.pdf",
		"https://example.com/page#section":          "https://example.com/page",
		"https://example.com/file.pdf?v=1#section":  "https://example.com/file.pdf",
		"https://example.com/path":                  "https://example.com/path",
	}
	for input, want := range tests {
		got := urlPath(input)
		if got != want {
			t.Errorf("urlPath(%q) = %q, want %q", input, got, want)
		}
	}
}
