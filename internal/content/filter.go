package content

import (
	"net/http"
	"path/filepath"
	"sort"
	"strings"
)

// DiscoveredImage represents an image found on a web page before filtering.
type DiscoveredImage struct {
	URL    string // Image URL
	Width  int    // From HTML attributes (0 if unknown)
	Height int    // From HTML attributes (0 if unknown)
	Size   int    // Content-Length or estimated size in bytes
}

// FilterWebImages applies heuristic + size + budget filtering to web page images.
// Returns at most maxImages images, sorted by size descending (larger = more likely content).
func FilterWebImages(images []DiscoveredImage, maxImages int) []DiscoveredImage {
	var filtered []DiscoveredImage

	for _, img := range images {
		if IsNoiseImage(img.URL, img.Width, img.Height) {
			continue
		}
		// Size filter: skip tiny (<10KB) and huge (>3MB)
		if img.Size > 0 {
			if img.Size < minImageBytesWeb {
				continue
			}
			if img.Size > maxImageBytes {
				continue
			}
		}
		filtered = append(filtered, img)
	}

	// Sort by size descending — larger images are more likely to be content-bearing
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].Size > filtered[j].Size
	})

	// Apply budget cap
	if len(filtered) > maxImages {
		filtered = filtered[:maxImages]
	}

	return filtered
}

// IsNoiseImage returns true for images that are likely ads, icons, tracking pixels,
// or other non-content images that should be skipped.
func IsNoiseImage(url string, width, height int) bool {
	// Dimension check: skip images smaller than 100x100 when dimensions are known
	if width > 0 && height > 0 && (width < 100 || height < 100) {
		return true
	}

	lowerURL := strings.ToLower(url)

	// URL pattern matching for known noise sources
	noisePatterns := []string{
		// Tracking and analytics
		"tracking", "pixel", "spacer", "beacon",
		"analytics", "telemetry",
		// Ad networks
		"doubleclick", "googlesyndication", "adsense",
		"advertisement", "adservice", "adserver",
		"facebook.com/tr", "fbcdn.net/rsrc",
		// Social/UI elements
		"gravatar", "avatar",
		"favicon", "/icon", "/logo",
		"/badge", "/button", "/spinner",
		// Common noise file patterns
		"1x1.", "blank.", "transparent.",
	}

	for _, pattern := range noisePatterns {
		if strings.Contains(lowerURL, pattern) {
			return true
		}
	}

	// GIF files on web pages are usually decorative/animated noise
	if strings.HasSuffix(lowerURL, ".gif") {
		return true
	}

	// Data URIs that are very small (inline icons/pixels)
	if strings.HasPrefix(lowerURL, "data:") && len(lowerURL) < 500 {
		return true
	}

	return false
}

// DetectContentType determines the content type of an HTTP response using a
// three-tier detection cascade: Content-Type header → URL extension → byte sniffing.
func DetectContentType(header string, url string, body []byte) string {
	// Tier 1: Trust the Content-Type header if present and specific
	if header != "" {
		// Parse out just the media type, stripping parameters like charset
		mediaType := strings.TrimSpace(strings.SplitN(header, ";", 2)[0])
		if mediaType != "" && mediaType != "application/octet-stream" {
			return mediaType
		}
	}

	// Tier 2: URL extension sniffing
	ext := strings.ToLower(filepath.Ext(urlPath(url)))
	if ext != "" {
		switch ext {
		case ".pdf":
			return "application/pdf"
		case ".png":
			return "image/png"
		case ".jpg", ".jpeg":
			return "image/jpeg"
		case ".webp":
			return "image/webp"
		case ".gif":
			return "image/gif"
		case ".svg":
			return "image/svg+xml"
		case ".bmp":
			return "image/bmp"
		case ".xml":
			return "text/xml"
		case ".json":
			return "application/json"
		case ".txt":
			return "text/plain"
		case ".html", ".htm":
			return "text/html"
		case ".css":
			return "text/css"
		case ".js":
			return "application/javascript"
		case ".docx":
			return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
		case ".pptx":
			return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
		}
	}

	// Tier 3: Byte sniffing (Go stdlib — inspects first 512 bytes)
	if len(body) > 0 {
		return http.DetectContentType(body)
	}

	// Default to HTML (existing behavior)
	return "text/html"
}

// urlPath extracts the path component from a URL, stripping query params and fragments.
func urlPath(rawURL string) string {
	// Strip query string
	if idx := strings.Index(rawURL, "?"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	// Strip fragment
	if idx := strings.Index(rawURL, "#"); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	return rawURL
}
