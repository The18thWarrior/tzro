package tools

import (
	"bytes"
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
	"tzro/internal/inference"

	"github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// ParsePDF parses a PDF file at the given path.
// It extracts all native digital text using ledongthuc/pdf,
// then extracts embedded images using pdfcpu,
// and runs OCR on those images using the best available backend:
//   - Local vision model (Gemma 4 with mmproj) — highest quality, no external deps
//   - System tesseract CLI — fast, requires install
//   - Skip — no OCR capability, returns text-only extraction
func ParsePDF(ctx context.Context, filePath string) (string, error) {
	// 0. Verify file exists and is accessible
	if _, err := os.Stat(filePath); err != nil {
		return "", fmt.Errorf("failed to open file '%s': %w", filePath, err)
	}

	// 1. Extract native digital text
	nativeText, err := extractNativeText(filePath)
	if err != nil {
		// Log error but try to proceed with image/OCR extraction anyway
		log.Printf("[PDF Parser Warning] Failed to extract native text: %v", err)
	}

	// 2. Setup temp directory for image extraction inside allowed paths (.tzro/cache)
	cacheDir := config.ResolvePath(".tzro/cache")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return nativeText, fmt.Errorf("failed to create cache directory: %w", err)
	}

	tempDir, err := os.MkdirTemp(cacheDir, "pdf-images-*")
	if err != nil {
		return nativeText, fmt.Errorf("failed to create temp directory for images: %w", err)
	}
	defer os.RemoveAll(tempDir) // Clean up temp images

	// 3. Extract embedded images
	conf := model.NewDefaultConfiguration()
	// Disable validation strictness to prevent failing on slightly malformed PDFs
	conf.ValidationMode = model.ValidationRelaxed

	log.Printf("[PDF Parser] Extracting images from %s to %s...", filePath, tempDir)
	extractErr := api.ExtractImagesFile(filePath, tempDir, nil, conf)
	if extractErr != nil {
		log.Printf("[PDF Parser Info] Image extraction skipped or failed (possibly no images): %v", extractErr)
	}

	// Read extracted image files
	files, err := os.ReadDir(tempDir)
	if err != nil {
		return nativeText, nil
	}

	if len(files) == 0 {
		return nativeText, nil
	}

	// Sort image files to maintain page/extraction order
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name() < files[j].Name()
	})

	// 4. Perform OCR via the best available backend
	ocrResults, ocrErr := performImageOCR(ctx, files, tempDir)
	if ocrErr != nil {
		log.Printf("[PDF Parser] OCR stage skipped: %v", ocrErr)
	}

	// Combine native text and OCR results
	finalText := nativeText
	if len(ocrResults) > 0 {
		if finalText != "" && !strings.HasSuffix(finalText, "\n") {
			finalText += "\n"
		}
		finalText += strings.Join(ocrResults, "\n\n")
	}

	return finalText, nil
}

// performImageOCR routes image OCR to the best available backend based on config:
//  1. Local vision model (Gemma 4 with mmproj) — highest quality, no external deps
//  2. System tesseract CLI — fast, requires install
//  3. Skip — returns error if no backend available
func performImageOCR(ctx context.Context, files []os.DirEntry, tempDir string) ([]string, error) {
	backend := config.GetPDFOcrBackend()

	useVision := false
	useTesseract := false

	switch backend {
	case "vision":
		useVision = inference.GlobalLocalModel.IsVisionAvailable()
		if !useVision {
			return nil, fmt.Errorf("vision backend requested but mmproj not loaded")
		}
	case "tesseract":
		if _, err := exec.LookPath("tesseract"); err != nil {
			return nil, fmt.Errorf("tesseract backend requested but not installed")
		}
		useTesseract = true
	default: // "auto"
		useVision = inference.GlobalLocalModel.IsVisionAvailable()
		if !useVision {
			if _, err := exec.LookPath("tesseract"); err == nil {
				useTesseract = true
			}
		}
	}

	if !useVision && !useTesseract {
		return nil, fmt.Errorf("no OCR backend available (install tesseract or load mmproj for vision)")
	}

	backendName := "tesseract"
	if useVision {
		backendName = "vision"
	}
	log.Printf("[PDF Parser] Using OCR backend: %s", backendName)

	var ocrResults []string
	for _, file := range files {
		if file.IsDir() {
			continue
		}

		imgPath := filepath.Join(tempDir, file.Name())
		log.Printf("[PDF Parser] Running %s OCR on extracted image: %s", backendName, file.Name())

		var ocrText string
		var err error

		if useVision {
			ocrText, err = visionOCR(ctx, imgPath)
		} else {
			ocrText, err = tesseractOCR(ctx, imgPath)
		}

		if err != nil {
			log.Printf("[PDF Parser OCR Warning] %s failed on %s: %v", backendName, file.Name(), err)
			continue
		}

		if ocrText != "" {
			ocrResults = append(ocrResults, fmt.Sprintf("--- OCR Text from Image (%s) ---\n%s", file.Name(), ocrText))
		}
	}

	return ocrResults, nil
}

// visionOCR sends an extracted PDF image to the local Gemma model for text extraction
// using the multimodal (vision) API. The image is encoded as a base64 data URI.
func visionOCR(ctx context.Context, imagePath string) (string, error) {
	imageData, err := os.ReadFile(imagePath)
	if err != nil {
		return "", fmt.Errorf("failed to read image file: %w", err)
	}

	// Determine MIME type from extension
	ext := strings.ToLower(filepath.Ext(imagePath))
	mimeType := "image/png"
	switch ext {
	case ".jpg", ".jpeg":
		mimeType = "image/jpeg"
	case ".gif":
		mimeType = "image/gif"
	case ".webp":
		mimeType = "image/webp"
	case ".bmp":
		mimeType = "image/bmp"
	}

	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, base64.StdEncoding.EncodeToString(imageData))

	msg := inference.NewMultimodalMessage("user", []inference.ContentPart{
		{Type: "text", Text: "Extract ALL text visible in this image. Preserve layout, tables, and formatting as closely as possible. Return only the extracted text, nothing else."},
		{Type: "image_url", ImageURL: &inference.ImageURL{URL: dataURI}},
	})

	req := inference.StructuredInferenceRequest{
		Messages: []inference.InferenceMessage{
			{Role: "system", Content: "You are a precise document OCR system. Extract text exactly as it appears in the image. Do not add commentary or descriptions."},
			msg,
		},
		JSONSchema: "", // No schema constraint — free-form text output
	}

	result, err := inference.GlobalLocalModel.ExecuteStructured(ctx, req)
	if err != nil {
		return "", fmt.Errorf("vision OCR inference failed: %w", err)
	}
	return strings.TrimSpace(result), nil
}

// tesseractOCR runs the system tesseract CLI on an image file for text extraction.
func tesseractOCR(ctx context.Context, imagePath string) (string, error) {
	tesseractPath, err := exec.LookPath("tesseract")
	if err != nil {
		return "", fmt.Errorf("tesseract not found: %w", err)
	}

	// Execute tesseract: tesseract <image-path> stdout -l eng
	cmd := exec.CommandContext(ctx, tesseractPath, imagePath, "stdout", "-l", "eng")
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("tesseract failed: %v (stderr: %s)", err, strings.TrimSpace(stderrBuf.String()))
	}

	return strings.TrimSpace(stdoutBuf.String()), nil
}

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

	var buf bytes.Buffer
	_, err = io.Copy(&buf, b)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}
