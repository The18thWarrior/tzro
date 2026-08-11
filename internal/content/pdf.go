package content

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"tzro/internal/config"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// maxPDFImages is the budget cap for embedded images per PDF document.
const maxPDFImages = 5

// ExtractPDF parses a PDF file at the given path and returns an ExtractedContent
// with the native text and vision-processed embedded images.
//
// It extracts all native digital text using ledongthuc/pdf,
// then extracts embedded images using pdfcpu, and runs OCR on those images
// using the best available backend (vision model or tesseract).
func ExtractPDF(ctx context.Context, filePath string) (*ExtractedContent, error) {
	// 0. Verify file exists and is accessible
	if _, err := os.Stat(filePath); err != nil {
		return nil, fmt.Errorf("failed to open file '%s': %w", filePath, err)
	}

	// 1. Extract native digital text
	nativeText, err := extractNativeText(filePath)
	if err != nil {
		log.Printf("[PDF Parser Warning] Failed to extract native text: %v", err)
	}

	// 2. Setup temp directory for image extraction inside allowed paths (.tzro/cache)
	cacheDir := config.ResolvePath("cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return &ExtractedContent{
			Type: ContentText,
			Text: nativeText,
			Metadata: map[string]string{
				"path":  filePath,
				"error": fmt.Sprintf("cache dir creation failed: %v", err),
			},
		}, nil
	}

	tempDir, err := os.MkdirTemp(cacheDir, "pdf-images-*")
	if err != nil {
		return &ExtractedContent{
			Type:     ContentText,
			Text:     nativeText,
			Metadata: map[string]string{"path": filePath},
		}, nil
	}
	defer os.RemoveAll(tempDir)

	// 3. Extract embedded images
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	log.Printf("[PDF Parser] Extracting images from %s to %s...", filePath, tempDir)
	extractErr := api.ExtractImagesFile(filePath, tempDir, nil, conf)
	if extractErr != nil {
		log.Printf("[PDF Parser Info] Image extraction skipped or failed (possibly no images): %v", extractErr)
	}

	// Read extracted image files
	files, err := os.ReadDir(tempDir)
	if err != nil || len(files) == 0 {
		return &ExtractedContent{
			Type:     ContentText,
			Text:     nativeText,
			Metadata: map[string]string{"path": filePath},
		}, nil
	}

	// Sort image files to maintain page/extraction order
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// 4. Apply budget cap
	if len(files) > maxPDFImages {
		log.Printf("[PDF Parser] Capping image processing at %d (found %d)", maxPDFImages, len(files))
		files = files[:maxPDFImages]
	}

	// 5. Process images through vision or OCR
	var images []ImageContent
	var ocrTexts []string

	for _, file := range files {
		if file.IsDir() {
			continue
		}

		imgPath := filepath.Join(tempDir, file.Name())
		imgData, readErr := os.ReadFile(imgPath)
		if readErr != nil {
			log.Printf("[PDF Parser] Failed to read extracted image %s: %v", file.Name(), readErr)
			continue
		}

		// Skip tiny images (likely decorative)
		if len(imgData) < 5*1024 {
			continue
		}

		// Determine MIME type
		ext := strings.ToLower(filepath.Ext(file.Name()))
		mimeType := extensionToMIME(ext)
		dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imgData))

		// Persist to cache
		source := fmt.Sprintf("pdf-%s-%s", filepath.Base(filePath), file.Name())
		localPath, _ := persistImageToCache(imgData, source)

		// Run vision inference
		description, visionErr := runVisionInference(ctx, dataURI)
		if visionErr != nil {
			// Fallback to tesseract OCR
			description, _ = runTesseractOCR(ctx, imgPath)
			if description == "" {
				description = fmt.Sprintf("Embedded image from PDF: %s", file.Name())
			}
		}

		formattedDesc := FormatVisionDescription(description, fmt.Sprintf("page image from %s", filepath.Base(filePath)))
		ocrTexts = append(ocrTexts, formattedDesc)

		images = append(images, ImageContent{
			DataURI:     dataURI,
			Description: formattedDesc,
			Source:      source,
			LocalPath:   localPath,
		})
	}

	// Combine native text and image descriptions
	finalText := nativeText
	if len(ocrTexts) > 0 {
		if finalText != "" && !strings.HasSuffix(finalText, "\n") {
			finalText += "\n"
		}
		finalText += "\n" + strings.Join(ocrTexts, "\n\n")
	}

	contentType := ContentText
	if len(images) > 0 {
		contentType = ContentMixed
	}

	return &ExtractedContent{
		Type:   contentType,
		Text:   finalText,
		Images: images,
		Metadata: map[string]string{
			"path":       filePath,
			"imageCount": fmt.Sprintf("%d", len(images)),
		},
	}, nil
}

// runTesseractOCR runs the system tesseract CLI on an image file for text extraction.
func runTesseractOCR(ctx context.Context, imagePath string) (string, error) {
	tesseractPath, err := lookPath("tesseract")
	if err != nil {
		return "", fmt.Errorf("tesseract not found: %w", err)
	}

	cmd := execCommandContext(ctx, tesseractPath, imagePath, "stdout", "-l", "eng")
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract failed: %v (stderr: %s)", err, strings.TrimSpace(stderrBuf.String()))
	}

	return strings.TrimSpace(stdoutBuf.String()), nil
}

// extractNativeText extracts native digital text from a PDF using ledongthuc/pdf.
func extractNativeText(filePath string) (string, error) {
	f, r, err := pdf.Open(filePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	b, err := r.GetPlainText()
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	_, err = io.Copy(&buf, b)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// lookPath wraps exec.LookPath for testability.
var lookPath = exec.LookPath

// execCommandContext wraps exec.CommandContext for testability.
var execCommandContext = exec.CommandContext
