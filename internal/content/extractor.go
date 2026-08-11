// Package content provides format-specific content extractors that convert files,
// web responses, and binary content into a universal ExtractedContent structure.
//
// Used by read_file and web_browse tools to handle PDFs, images, Office documents,
// and web pages. Images are pre-processed to text descriptions via the worker model's
// vision capability at extraction time — the Probe Node loop remains text-only.
package content

// ContentType classifies the kind of content that was extracted.
type ContentType string

const (
	// ContentText is plain text content (code, markdown, XML, etc.).
	ContentText ContentType = "text"
	// ContentImage is a single image that was vision-processed to text.
	ContentImage ContentType = "image"
	// ContentMixed is text content with embedded images (e.g., PDF with diagrams).
	ContentMixed ContentType = "mixed"
	// ContentBinary is unextractable binary content (error case).
	ContentBinary ContentType = "binary"
)

// ExtractedContent is the universal output of any extraction operation.
// Tools populate this and attach it to ToolResult.Extracted for the probe loop
// to consume. The Text field always contains a text representation suitable for
// the probe's context window. Images carry vision-processed descriptions.
type ExtractedContent struct {
	// Type classifies what kind of content this is.
	Type ContentType

	// Text is the extracted text content. Always present for ContentText and
	// ContentMixed types. For ContentImage, contains the vision description.
	Text string

	// Images holds extracted images with their vision-processed descriptions.
	// Present for ContentImage (1 image) and ContentMixed (multiple images).
	Images []ImageContent

	// Metadata holds source-specific information (url, path, page count, etc.).
	Metadata map[string]string
}

// ImageContent represents a single extracted image that has been vision-processed.
type ImageContent struct {
	// DataURI is the base64-encoded image data ("data:image/png;base64,...").
	// Ephemeral — used for vision processing, not persisted in context.
	DataURI string

	// Description is the vision model's text description of the image,
	// prefixed with a confidence caveat.
	Description string

	// Source identifies where this image came from ("page-3", "figure-2", URL).
	Source string

	// LocalPath is the persisted file path in .tzro/cache/images/.
	// Available for artifact assembly and later re-examination.
	LocalPath string
}

// VisionDescriptionPrefix is prepended to all vision-derived image descriptions
// to signal to the model that the content is approximate.
const VisionDescriptionPrefix = "[Image description (via vision model — may be approximate): "

// VisionDescriptionSuffix closes the caveat bracket.
const VisionDescriptionSuffix = "]"

// FormatVisionDescription wraps a raw vision output with the caveat prefix/suffix.
func FormatVisionDescription(rawDescription, source string) string {
	desc := VisionDescriptionPrefix + rawDescription + VisionDescriptionSuffix
	if source != "" {
		desc += "\n[Source: " + source + "]"
	}
	return desc
}
