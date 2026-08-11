package content

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// maxWebImages is the budget cap for images extracted from a single web page.
const maxWebImages = 3

// ExtractWebResponse dispatches HTTP response content to the appropriate extractor
// based on content type. Returns an ExtractedContent with text and optionally images.
func ExtractWebResponse(ctx context.Context, body []byte, contentType string, url string) (*ExtractedContent, error) {
	// Detect the actual content type using the cascade
	detectedType := DetectContentType(contentType, url, body)

	switch {
	case strings.HasPrefix(detectedType, "text/html"),
		strings.HasPrefix(detectedType, "application/xhtml"):
		return extractHTML(ctx, body, url)

	case detectedType == "application/pdf":
		return extractPDFFromBytes(ctx, body, url)

	case strings.HasPrefix(detectedType, "image/"):
		return ExtractImageFromBytes(ctx, body, detectedType, url)

	case strings.HasPrefix(detectedType, "text/"):
		// Plain text content (CSS, JS, XML, etc.)
		return &ExtractedContent{
			Type: ContentText,
			Text: string(body),
			Metadata: map[string]string{
				"url":         url,
				"contentType": detectedType,
			},
		}, nil

	default:
		return nil, fmt.Errorf("unsupported content type: %s (url: %s)", detectedType, url)
	}
}

// extractHTML processes an HTML page, extracting text content.
// Image extraction from HTML pages is handled separately by the caller
// (web_browse tool) which has access to the parsed DOM for <img> tag discovery.
func extractHTML(ctx context.Context, body []byte, url string) (*ExtractedContent, error) {
	// Convert HTML to text using the same approach as htmlToText
	text := htmlToPlainText(string(body))

	return &ExtractedContent{
		Type: ContentText,
		Text: text,
		Metadata: map[string]string{
			"url":         url,
			"contentType": "text/html",
		},
	}, nil
}

// extractPDFFromBytes writes PDF bytes to a temp file and runs ExtractPDF.
func extractPDFFromBytes(ctx context.Context, body []byte, url string) (*ExtractedContent, error) {
	// Write to temp file for PDF parser
	tmpFile, err := createTempFile("web-pdf-*.pdf", body)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for PDF: %w", err)
	}
	defer removeTempFile(tmpFile)

	result, err := ExtractPDF(ctx, tmpFile)
	if err != nil {
		return nil, fmt.Errorf("failed to extract PDF from %s: %w", url, err)
	}

	// Add URL to metadata
	if result.Metadata == nil {
		result.Metadata = make(map[string]string)
	}
	result.Metadata["url"] = url

	return result, nil
}

// htmlToPlainText strips HTML tags and extracts visible text content.
// This is a simplified version — the full htmlToText in web_browse.go
// handles more edge cases. This is used as a fallback.
func htmlToPlainText(html string) string {
	// Remove script and style blocks
	for _, tag := range []string{"script", "style", "noscript"} {
		for {
			start := strings.Index(strings.ToLower(html), "<"+tag)
			if start < 0 {
				break
			}
			end := strings.Index(strings.ToLower(html[start:]), "</"+tag+">")
			if end < 0 {
				end = len(html) - start
			} else {
				end += len("</" + tag + ">")
			}
			html = html[:start] + html[start+end:]
		}
	}

	// Strip remaining tags
	var result strings.Builder
	inTag := false
	for _, ch := range html {
		switch {
		case ch == '<':
			inTag = true
		case ch == '>':
			inTag = false
			result.WriteRune(' ')
		case !inTag:
			result.WriteRune(ch)
		}
	}

	// Clean up whitespace
	text := result.String()
	lines := strings.Split(text, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleaned = append(cleaned, line)
		}
	}

	return strings.Join(cleaned, "\n")
}

// createTempFile creates a temporary file with the given pattern and content.
func createTempFile(pattern string, data []byte) (string, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	return path, nil
}

// removeTempFile removes a temporary file, logging errors.
func removeTempFile(path string) {
	if err := os.Remove(path); err != nil {
		log.Printf("[Content] Failed to remove temp file %s: %v", path, err)
	}
}
