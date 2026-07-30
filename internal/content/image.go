package content

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"tzro/internal/config"
	"tzro/internal/inference"
)

// imageCacheDir is the subdirectory under .tzro/cache/ for persisted images.
const imageCacheDir = "cache/images"

// maxImageBytes is the upper bound for image file size (3MB).
// Images larger than this are skipped to avoid context bloat.
const maxImageBytes = 3 * 1024 * 1024

// minImageBytes is the lower bound for image file size (10KB).
// Images smaller than this are likely icons/pixels and are skipped.
const minImageBytesWeb = 10 * 1024

// ExtractImage reads an image file, sends it to the worker model for vision
// description, persists the image to .tzro/cache/images/, and returns an
// ExtractedContent with the vision description as text.
//
// The worker model (4B with mmproj) is used for vision processing.
// If vision is unavailable, returns a text-only description with file metadata.
func ExtractImage(ctx context.Context, path string) (*ExtractedContent, error) {
	imageData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read image file: %w", err)
	}

	if len(imageData) > maxImageBytes {
		return &ExtractedContent{
			Type: ContentText,
			Text: fmt.Sprintf("[Image skipped: file too large (%d bytes, max %d)]", len(imageData), maxImageBytes),
			Metadata: map[string]string{
				"path":   path,
				"reason": "too_large",
			},
		}, nil
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(path))
	mimeType := extensionToMIME(ext)

	// Base64 encode for vision API
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imageData))

	// Persist to cache
	localPath, persistErr := persistImageToCache(imageData, filepath.Base(path))
	if persistErr != nil {
		log.Printf("[Content] Failed to persist image to cache: %v", persistErr)
	}

	// Run vision inference via the worker model
	description, visionErr := runVisionInference(ctx, dataURI)
	if visionErr != nil {
		log.Printf("[Content] Vision inference failed for %s: %v", path, visionErr)
		description = fmt.Sprintf("Image file: %s (%d bytes, %s)", filepath.Base(path), len(imageData), mimeType)
	}

	formattedDesc := FormatVisionDescription(description, filepath.Base(path))

	img := ImageContent{
		DataURI:     dataURI,
		Description: formattedDesc,
		Source:      filepath.Base(path),
		LocalPath:   localPath,
	}

	return &ExtractedContent{
		Type:   ContentImage,
		Text:   formattedDesc,
		Images: []ImageContent{img},
		Metadata: map[string]string{
			"path":      path,
			"mimeType":  mimeType,
			"sizeBytes": fmt.Sprintf("%d", len(imageData)),
			"localPath": localPath,
		},
	}, nil
}

// ExtractImageFromBytes processes raw image bytes (e.g., from a web download)
// and returns an ExtractedContent with the vision description.
func ExtractImageFromBytes(ctx context.Context, imageData []byte, mimeType, source string) (*ExtractedContent, error) {
	if len(imageData) > maxImageBytes {
		return &ExtractedContent{
			Type: ContentText,
			Text: fmt.Sprintf("[Image skipped: too large (%d bytes, max %d)]", len(imageData), maxImageBytes),
			Metadata: map[string]string{
				"source": source,
				"reason": "too_large",
			},
		}, nil
	}

	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imageData))

	// Derive a filename from source
	filename := sanitizeFilename(source)
	localPath, persistErr := persistImageToCache(imageData, filename)
	if persistErr != nil {
		log.Printf("[Content] Failed to persist image to cache: %v", persistErr)
	}

	description, visionErr := runVisionInference(ctx, dataURI)
	if visionErr != nil {
		log.Printf("[Content] Vision inference failed for %s: %v", source, visionErr)
		description = fmt.Sprintf("Image from %s (%d bytes, %s)", source, len(imageData), mimeType)
	}

	formattedDesc := FormatVisionDescription(description, source)

	img := ImageContent{
		DataURI:     dataURI,
		Description: formattedDesc,
		Source:      source,
		LocalPath:   localPath,
	}

	return &ExtractedContent{
		Type:   ContentImage,
		Text:   formattedDesc,
		Images: []ImageContent{img},
		Metadata: map[string]string{
			"source":    source,
			"mimeType":  mimeType,
			"sizeBytes": fmt.Sprintf("%d", len(imageData)),
			"localPath": localPath,
		},
	}, nil
}

// runVisionInference sends an image to the worker model for text description.
func runVisionInference(ctx context.Context, dataURI string) (string, error) {
	if !inference.GlobalLocalModel.IsVisionAvailable() {
		return "", fmt.Errorf("vision not available (mmproj not loaded)")
	}

	msg := inference.NewMultimodalMessage("user", []inference.ContentPart{
		{Type: "text", Text: "Describe this image in detail. If it contains text, charts, tables, or data, extract all visible information. Preserve numbers, labels, and relationships accurately. Return only the description, nothing else."},
		{Type: "image_url", ImageURL: &inference.ImageURL{URL: dataURI}},
	})

	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: "You are a precise image analysis system. Describe images accurately, focusing on data, text, and structural information. Do not add commentary or speculation."},
			msg,
		},
	}

	result, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
	if err != nil {
		return "", fmt.Errorf("vision inference failed: %w", err)
	}
	return strings.TrimSpace(result), nil
}

// persistImageToCache saves image bytes to .tzro/cache/images/ with a
// content-hash filename. Returns the full path or empty string on error.
func persistImageToCache(data []byte, originalName string) (string, error) {
	cacheDir := config.ResolvePath(imageCacheDir)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create image cache directory: %w", err)
	}

	// Content-hash filename to deduplicate
	hash := sha256.Sum256(data)
	hashPrefix := fmt.Sprintf("%x", hash[:8])

	ext := filepath.Ext(originalName)
	if ext == "" {
		ext = ".png" // default
	}
	filename := hashPrefix + "-" + sanitizeFilename(originalName)
	if filepath.Ext(filename) == "" {
		filename += ext
	}

	fullPath := filepath.Join(cacheDir, filename)

	// Skip if already persisted (content-addressed)
	if _, err := os.Stat(fullPath); err == nil {
		return fullPath, nil
	}

	if err := os.WriteFile(fullPath, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write image to cache: %w", err)
	}

	log.Printf("[Content] Persisted image to %s (%d bytes)", fullPath, len(data))
	return fullPath, nil
}

// extensionToMIME maps common image extensions to MIME types.
func extensionToMIME(ext string) string {
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	case ".ico":
		return "image/x-icon"
	default:
		return "image/png"
	}
}

// sanitizeFilename removes or replaces characters that are invalid in filenames.
func sanitizeFilename(name string) string {
	// Strip directory components
	name = filepath.Base(name)

	// Replace URL-unfriendly characters
	replacer := strings.NewReplacer(
		"/", "-",
		"\\", "-",
		":", "-",
		"?", "",
		"&", "",
		"=", "",
		"#", "",
		"%", "",
		" ", "_",
	)
	name = replacer.Replace(name)

	// Limit length
	if len(name) > 64 {
		ext := filepath.Ext(name)
		name = name[:64-len(ext)] + ext
	}

	if name == "" || name == "." {
		name = "image.png"
	}
	return name
}

// ClearImageCache removes all cached images from .tzro/cache/images/.
// Returns the number of files removed and any error.
func ClearImageCache() (int, error) {
	cacheDir := config.ResolvePath(imageCacheDir)

	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // Nothing to clear
		}
		return 0, fmt.Errorf("failed to read cache directory: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(cacheDir, entry.Name())
		if err := os.Remove(path); err != nil {
			log.Printf("[Content] Failed to remove cached file %s: %v", path, err)
			continue
		}
		removed++
	}

	log.Printf("[Content] Cleared image cache: %d files removed from %s", removed, cacheDir)
	return removed, nil
}
