package compactor

// segment.go — Content classification and segmentation for structured compaction.
//
// Splits mixed content (markdown with code blocks, pure code, tabular data)
// into typed segments for type-appropriate compaction strategies.

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// SegmentType classifies content for type-appropriate compaction.
type SegmentType int

const (
	SegmentCode    SegmentType = iota // Source code — deterministic skeleton
	SegmentText                       // Prose/logs/reasoning — deterministic middle-out
	SegmentTabular                    // Structured data — header + sample rows
)

func (s SegmentType) String() string {
	switch s {
	case SegmentCode:
		return "code"
	case SegmentText:
		return "text"
	case SegmentTabular:
		return "tabular"
	default:
		return "unknown"
	}
}

// Segment represents a classified chunk of content.
type Segment struct {
	Type     SegmentType
	Content  string
	Language string // For code segments: "go", "python", etc. Empty if unknown.
}

// tabularSampleRows is the number of sample rows to keep for tabular data.
const tabularSampleRows = 3

// middleOutKeepLines is the number of lines to keep from head and tail.
const middleOutKeepLines = 30

// SegmentContent splits mixed content into typed segments.
// It recognizes fenced code blocks (``` markers) and classifies each piece.
// For content without fenced blocks, it classifies the entire content as one segment.
func SegmentContent(content string) []Segment {
	if content == "" {
		return nil
	}

	lines := strings.Split(content, "\n")
	var segments []Segment
	var currentText []string
	inCodeBlock := false
	var codeLines []string
	var codeLang string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "```") {
			if !inCodeBlock {
				// Opening fence — flush any accumulated text
				if len(currentText) > 0 {
					text := strings.Join(currentText, "\n")
					if strings.TrimSpace(text) != "" {
						segments = append(segments, classifySingleSegment(text))
					}
					currentText = nil
				}
				// Extract language hint
				codeLang = strings.TrimPrefix(trimmed, "```")
				codeLang = strings.TrimSpace(codeLang)
				inCodeBlock = true
				codeLines = nil
			} else {
				// Closing fence — emit code segment
				code := strings.Join(codeLines, "\n")
				if strings.TrimSpace(code) != "" {
					segments = append(segments, Segment{
						Type:     SegmentCode,
						Content:  code,
						Language: codeLang,
					})
				}
				inCodeBlock = false
				codeLines = nil
				codeLang = ""
			}
			continue
		}

		if inCodeBlock {
			codeLines = append(codeLines, line)
		} else {
			currentText = append(currentText, line)
		}
	}

	// Flush remaining content
	if inCodeBlock && len(codeLines) > 0 {
		// Unclosed code block — treat as code anyway
		segments = append(segments, Segment{
			Type:     SegmentCode,
			Content:  strings.Join(codeLines, "\n"),
			Language: codeLang,
		})
	}
	if len(currentText) > 0 {
		text := strings.Join(currentText, "\n")
		if strings.TrimSpace(text) != "" {
			segments = append(segments, classifySingleSegment(text))
		}
	}

	// If no fenced blocks were found, classify the whole content
	if len(segments) == 0 {
		return []Segment{classifySingleSegment(content)}
	}

	return segments
}

// classifySingleSegment classifies a piece of content that has no fenced code blocks.
func classifySingleSegment(content string) Segment {
	ct := ClassifyContent(content)
	switch ct {
	case SegmentCode:
		return Segment{Type: SegmentCode, Content: content}
	case SegmentTabular:
		return Segment{Type: SegmentTabular, Content: content}
	default:
		return Segment{Type: SegmentText, Content: content}
	}
}

// ClassifyContent heuristically determines the content type of text.
func ClassifyContent(content string) SegmentType {
	if looksTabular(content) {
		return SegmentTabular
	}
	if looksLikeCode(content) {
		return SegmentCode
	}
	return SegmentText
}

// looksTabular checks for CSV/TSV/JSON-array/table patterns.
func looksTabular(content string) bool {
	lines := strings.SplitN(content, "\n", 5)
	if len(lines) < 2 {
		return false
	}

	// CSV/TSV: consistent delimiter counts across lines
	for _, delim := range []string{",", "\t", "|"} {
		firstCount := strings.Count(lines[0], delim)
		if firstCount >= 2 {
			matches := 0
			for _, line := range lines[1:] {
				if strings.Count(line, delim) == firstCount {
					matches++
				}
			}
			if matches >= len(lines[1:])-1 {
				return true
			}
		}
	}

	// JSON array
	trimmed := strings.TrimSpace(content)
	if strings.HasPrefix(trimmed, "[{") || strings.HasPrefix(trimmed, "[\n") {
		return true
	}

	return false
}

// looksLikeCode checks for source code patterns.
func looksLikeCode(content string) bool {
	codeIndicators := 0
	lines := strings.SplitN(content, "\n", 30)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Function declarations
		if strings.HasPrefix(trimmed, "func ") || strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "function ") || strings.HasPrefix(trimmed, "class ") ||
			strings.HasPrefix(trimmed, "type ") || strings.HasPrefix(trimmed, "interface ") {
			codeIndicators += 2
		}
		// Braces and brackets
		if strings.HasSuffix(trimmed, "{") || strings.HasSuffix(trimmed, "}") {
			codeIndicators++
		}
		// Import/package statements
		if strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "package ") ||
			strings.HasPrefix(trimmed, "from ") || strings.HasPrefix(trimmed, "#include") {
			codeIndicators += 2
		}
		// Comments
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "#") ||
			strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, `"""`) {
			codeIndicators++
		}
	}

	return codeIndicators >= 4
}

// TruncateTabular keeps the header row, first N sample rows, and a summary line.
func TruncateTabular(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= tabularSampleRows+2 {
		return content
	}

	var result []string
	headerEnd := 1
	if len(lines) > 1 && (strings.Contains(lines[1], "---") || strings.Contains(lines[1], "===")) {
		headerEnd = 2
	}

	result = append(result, lines[:headerEnd]...)
	endSample := headerEnd + tabularSampleRows
	if endSample > len(lines) {
		endSample = len(lines)
	}
	result = append(result, lines[headerEnd:endSample]...)

	totalRows := len(lines) - headerEnd
	omitted := totalRows - tabularSampleRows
	if omitted > 0 {
		result = append(result, fmt.Sprintf("[... %d more rows omitted (total: %d rows) ...]", omitted, totalRows))
	}

	if len(lines) > endSample+1 {
		result = append(result, lines[len(lines)-2])
	}

	joined := strings.Join(result, "\n")
	if utf8.RuneCountInString(joined) > targetChars {
		return TruncateTextMiddleOut(content, targetChars)
	}
	return joined
}

// TruncateTextMiddleOut keeps the first and last N lines of text content,
// replacing the middle with a summary marker.
func TruncateTextMiddleOut(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	keepLines := middleOutKeepLines

	if len(lines) <= keepLines*2 {
		runes := []rune(content)
		if len(runes) <= targetChars {
			return content
		}
		cutoff := targetChars - 50
		if cutoff < 1 {
			cutoff = targetChars
		}
		return string(runes[:cutoff]) + "\n[... truncated ...]"
	}

	head := strings.Join(lines[:keepLines], "\n")
	tail := strings.Join(lines[len(lines)-keepLines:], "\n")
	omitted := len(lines) - keepLines*2

	result := head + fmt.Sprintf("\n[... %d lines omitted ...]\n", omitted) + tail

	if utf8.RuneCountInString(result) > targetChars {
		runes := []rune(result)
		cutoff := targetChars - 50
		if cutoff < 1 {
			cutoff = targetChars
		}
		return string(runes[:cutoff]) + "\n[... truncated ...]"
	}
	return result
}
