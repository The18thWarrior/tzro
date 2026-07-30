package content

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
)

// ExtractOffice extracts text and embedded images from DOCX and PPTX files.
// Uses pure Go: archive/zip + encoding/xml to parse the Office Open XML format.
// No external dependencies required.
func ExtractOffice(path string) (*ExtractedContent, error) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".docx":
		return extractDOCX(path)
	case ".pptx":
		return extractPPTX(path)
	default:
		return nil, fmt.Errorf("unsupported Office format: %s", ext)
	}
}

// extractDOCX extracts text from a DOCX file by parsing word/document.xml.
// DOCX files are ZIP archives containing XML documents.
func extractDOCX(path string) (*ExtractedContent, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open DOCX file: %w", err)
	}
	defer r.Close()

	var textParts []string
	var images []ImageContent

	// Extract text from document.xml
	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			text, err := parseDocxXML(f)
			if err != nil {
				return nil, fmt.Errorf("failed to parse document.xml: %w", err)
			}
			textParts = append(textParts, text)
		}
	}

	// Extract embedded images from word/media/
	images, _ = extractOfficeMedia(r, "word/media/")

	contentType := ContentText
	if len(images) > 0 {
		contentType = ContentMixed
	}

	return &ExtractedContent{
		Type:   contentType,
		Text:   strings.Join(textParts, "\n"),
		Images: images,
		Metadata: map[string]string{
			"path":       path,
			"format":     "docx",
			"imageCount": fmt.Sprintf("%d", len(images)),
		},
	}, nil
}

// extractPPTX extracts text from a PPTX file by parsing ppt/slide*.xml files.
func extractPPTX(path string) (*ExtractedContent, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open PPTX file: %w", err)
	}
	defer r.Close()

	// Collect slide files and sort them by name for correct ordering
	var slideFiles []*zip.File
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			slideFiles = append(slideFiles, f)
		}
	}

	sort.Slice(slideFiles, func(i, j int) bool {
		return slideFiles[i].Name < slideFiles[j].Name
	})

	var textParts []string
	for i, f := range slideFiles {
		text, err := parsePptxSlide(f)
		if err != nil {
			continue
		}
		if text != "" {
			textParts = append(textParts, fmt.Sprintf("--- Slide %d ---\n%s", i+1, text))
		}
	}

	// Extract embedded images from ppt/media/
	var images []ImageContent
	images, _ = extractOfficeMedia(r, "ppt/media/")

	contentType := ContentText
	if len(images) > 0 {
		contentType = ContentMixed
	}

	return &ExtractedContent{
		Type:   contentType,
		Text:   strings.Join(textParts, "\n\n"),
		Images: images,
		Metadata: map[string]string{
			"path":       path,
			"format":     "pptx",
			"slideCount": fmt.Sprintf("%d", len(slideFiles)),
			"imageCount": fmt.Sprintf("%d", len(images)),
		},
	}, nil
}

// parseDocxXML extracts text from a DOCX document.xml file.
// It walks the XML tree and extracts text from <w:t> elements,
// treating <w:p> (paragraph) elements as newline boundaries.
func parseDocxXML(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	return parseXMLTextElements(rc, "w:t", "w:p")
}

// parsePptxSlide extracts text from a PPTX slide XML file.
// It extracts text from <a:t> elements within the slide.
func parsePptxSlide(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	return parseXMLTextElements(rc, "a:t", "a:p")
}

// parseXMLTextElements is a generic XML text extractor that collects text from
// elements with the given tag name and treats paragraph elements as newline boundaries.
func parseXMLTextElements(r io.Reader, textTag, paraTag string) (string, error) {
	decoder := xml.NewDecoder(r)
	var paragraphs []string
	var currentPara strings.Builder
	inText := false

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		switch t := token.(type) {
		case xml.StartElement:
			localName := t.Name.Local
			if localName == stripPrefix(textTag) {
				inText = true
			}
		case xml.EndElement:
			localName := t.Name.Local
			if localName == stripPrefix(textTag) {
				inText = false
			}
			if localName == stripPrefix(paraTag) {
				text := strings.TrimSpace(currentPara.String())
				if text != "" {
					paragraphs = append(paragraphs, text)
				}
				currentPara.Reset()
			}
		case xml.CharData:
			if inText {
				currentPara.Write(t)
			}
		}
	}

	// Flush any remaining content
	remaining := strings.TrimSpace(currentPara.String())
	if remaining != "" {
		paragraphs = append(paragraphs, remaining)
	}

	return strings.Join(paragraphs, "\n"), nil
}

// stripPrefix removes the namespace prefix from an XML tag name.
// "w:t" → "t", "a:p" → "p"
func stripPrefix(tag string) string {
	if idx := strings.Index(tag, ":"); idx >= 0 {
		return tag[idx+1:]
	}
	return tag
}

// extractOfficeMedia extracts images from a ZIP archive's media directory.
// Returns at most maxPDFImages (5) images, applying the same budget cap as PDFs.
func extractOfficeMedia(r *zip.ReadCloser, mediaPrefix string) ([]ImageContent, error) {
	var images []ImageContent
	count := 0

	for _, f := range r.File {
		if count >= maxPDFImages {
			break
		}
		if !strings.HasPrefix(f.Name, mediaPrefix) {
			continue
		}

		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".bmp" && ext != ".webp" {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil || len(data) < 5*1024 {
			continue // Skip tiny images (likely decorative)
		}

		source := filepath.Base(f.Name)

		localPath, _ := persistImageToCache(data, source)

		images = append(images, ImageContent{
			Description: fmt.Sprintf("[Embedded image from Office document: %s]", source),
			Source:      source,
			LocalPath:   localPath,
		})
		count++
	}

	return images, nil
}
