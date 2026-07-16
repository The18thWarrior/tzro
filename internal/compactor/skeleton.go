package compactor

// skeleton.go — Deterministic code skeleton extraction.
//
// Extracts function signatures, type declarations, doc comments, and
// package/import statements from source code. Function bodies are replaced
// with fingerprints containing line count and extracted function calls.
//
// This is NEVER LLM-compressed — purely structural transformation.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// codeFloorChars is the minimum chars to retain per code file.
// Function signatures may exceed this floor and are always retained.
const codeFloorChars = 500

// maxFingerprintCalls is the maximum number of function calls to list in a body fingerprint.
const maxFingerprintCalls = 5

// ExtractSkeleton reduces source code to its structural skeleton:
// function signatures, type declarations, doc comments, const/var blocks.
// Function bodies are replaced with fingerprints.
// If the skeleton fits within targetChars, returns it directly.
// If targetChars <= 0, no budget constraint is applied (skeleton only).
func ExtractSkeleton(content string, targetChars int) string {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return content
	}

	// Parse lines into structural elements
	elements := parseCodeStructure(lines)

	// Build skeleton from elements
	skeleton := buildSkeleton(elements)

	// If no budget or within budget, return skeleton
	if targetChars <= 0 || utf8.RuneCountInString(skeleton) <= targetChars {
		return skeleton
	}

	// Over budget — try signatures-only (no doc comments or body fingerprints)
	sigsOnly := buildSignaturesOnly(elements)
	if utf8.RuneCountInString(sigsOnly) <= targetChars {
		return sigsOnly
	}

	// Even signatures exceed budget — hard truncate
	runes := []rune(sigsOnly)
	if len(runes) > targetChars-50 {
		return string(runes[:targetChars-50]) + "\n// ... [truncated] ..."
	}
	return sigsOnly
}

// codeElement represents a parsed structural element of source code.
type codeElement struct {
	kind        elementKind
	lines       []string // The actual source lines
	bodyLines   []string // For functions: the body lines (for fingerprinting)
	docComment  []string // Leading doc comment lines
	fingerprint string   // For functions: extracted body fingerprint
}

type elementKind int

const (
	elemPackage elementKind = iota // package declaration
	elemImport                     // import block
	elemConst                      // const block
	elemVar                        // var block
	elemType                       // type declaration (struct, interface, etc.)
	elemFunc                       // function/method declaration
	elemComment                    // standalone doc comment
	elemOther                      // other top-level code
)

// parseCodeStructure analyzes source lines and groups them into structural elements.
func parseCodeStructure(lines []string) []codeElement {
	var elements []codeElement
	var docBuf []string
	i := 0

	for i < len(lines) {
		trimmed := strings.TrimSpace(lines[i])

		// Collect doc comments
		if strings.HasPrefix(trimmed, "//") {
			docBuf = append(docBuf, lines[i])
			i++
			continue
		}

		// Package declaration
		if strings.HasPrefix(trimmed, "package ") {
			elements = append(elements, codeElement{
				kind:       elemPackage,
				lines:      []string{lines[i]},
				docComment: docBuf,
			})
			docBuf = nil
			i++
			continue
		}

		// Import block
		if strings.HasPrefix(trimmed, "import ") || trimmed == "import (" {
			start := i
			if strings.Contains(trimmed, "(") {
				// Multi-line import
				for i < len(lines) && !strings.Contains(strings.TrimSpace(lines[i]), ")") {
					i++
				}
				if i < len(lines) {
					i++ // include closing )
				}
			} else {
				i++
			}
			elements = append(elements, codeElement{
				kind:  elemImport,
				lines: lines[start:i],
			})
			docBuf = nil
			continue
		}

		// Const block
		if strings.HasPrefix(trimmed, "const ") || trimmed == "const (" {
			start := i
			if strings.Contains(trimmed, "(") {
				for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), ")") {
					i++
				}
				if i < len(lines) {
					i++
				}
			} else {
				i++
			}
			elements = append(elements, codeElement{
				kind:       elemConst,
				lines:      lines[start:i],
				docComment: docBuf,
			})
			docBuf = nil
			continue
		}

		// Var block
		if strings.HasPrefix(trimmed, "var ") || trimmed == "var (" {
			start := i
			if strings.Contains(trimmed, "(") {
				for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), ")") {
					i++
				}
				if i < len(lines) {
					i++
				}
			} else {
				i++
			}
			elements = append(elements, codeElement{
				kind:       elemVar,
				lines:      lines[start:i],
				docComment: docBuf,
			})
			docBuf = nil
			continue
		}

		// Type declaration
		if strings.HasPrefix(trimmed, "type ") {
			start := i
			if strings.HasSuffix(trimmed, "{") {
				// Multi-line type (struct, interface)
				depth := 1
				i++
				for i < len(lines) && depth > 0 {
					t := strings.TrimSpace(lines[i])
					depth += strings.Count(t, "{") - strings.Count(t, "}")
					i++
				}
			} else {
				i++
			}
			elements = append(elements, codeElement{
				kind:       elemType,
				lines:      lines[start:i],
				docComment: docBuf,
			})
			docBuf = nil
			continue
		}

		// Function/method declaration
		if strings.HasPrefix(trimmed, "func ") ||
			strings.HasPrefix(trimmed, "def ") ||
			strings.HasPrefix(trimmed, "function ") {
			sigLine := lines[i]
			start := i
			i++

			if strings.HasSuffix(trimmed, "{") {
				// Collect body
				depth := 1
				bodyStart := i
				for i < len(lines) && depth > 0 {
					t := strings.TrimSpace(lines[i])
					depth += strings.Count(t, "{") - strings.Count(t, "}")
					i++
				}
				bodyLines := lines[bodyStart:i]
				// Exclude closing brace from body for fingerprinting
				if len(bodyLines) > 0 {
					last := strings.TrimSpace(bodyLines[len(bodyLines)-1])
					if last == "}" {
						bodyLines = bodyLines[:len(bodyLines)-1]
					}
				}

				fp := buildBodyFingerprint(bodyLines)
				elements = append(elements, codeElement{
					kind:        elemFunc,
					lines:       []string{sigLine},
					bodyLines:   bodyLines,
					docComment:  docBuf,
					fingerprint: fp,
				})
			} else {
				// Single-line function or declaration without body
				elements = append(elements, codeElement{
					kind:       elemFunc,
					lines:      lines[start:i],
					docComment: docBuf,
				})
			}
			docBuf = nil
			continue
		}

		// Empty line — flush doc buffer if it was standalone
		if trimmed == "" {
			if len(docBuf) > 0 {
				elements = append(elements, codeElement{
					kind:  elemComment,
					lines: docBuf,
				})
				docBuf = nil
			}
			i++
			continue
		}

		// Other content
		docBuf = nil
		i++
	}

	// Flush remaining doc comments
	if len(docBuf) > 0 {
		elements = append(elements, codeElement{
			kind:  elemComment,
			lines: docBuf,
		})
	}

	return elements
}

// buildSkeleton assembles the full skeleton with doc comments and fingerprints.
func buildSkeleton(elements []codeElement) string {
	var sb strings.Builder

	for _, elem := range elements {
		// Write doc comments
		for _, dc := range elem.docComment {
			sb.WriteString(dc)
			sb.WriteString("\n")
		}

		switch elem.kind {
		case elemFunc:
			// Write signature
			for _, line := range elem.lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			// Write fingerprint if body was present
			if elem.fingerprint != "" {
				sb.WriteString(elem.fingerprint)
				sb.WriteString("\n}\n")
			}
		case elemType, elemConst, elemVar, elemPackage, elemImport:
			// Write full element
			for _, line := range elem.lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		case elemComment:
			// Write standalone comments
			for _, line := range elem.lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	return strings.TrimRight(sb.String(), "\n")
}

// buildSignaturesOnly emits only signatures without doc comments or fingerprints.
// Used when budget is very tight.
func buildSignaturesOnly(elements []codeElement) string {
	var sb strings.Builder

	for _, elem := range elements {
		switch elem.kind {
		case elemPackage:
			for _, line := range elem.lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
		case elemFunc:
			for _, line := range elem.lines {
				sb.WriteString(line)
				sb.WriteString("\n")
			}
			if elem.fingerprint != "" {
				sb.WriteString("}\n")
			}
		case elemType:
			// Just the first line (type declaration)
			if len(elem.lines) > 0 {
				sb.WriteString(elem.lines[0])
				sb.WriteString("\n")
				if strings.HasSuffix(strings.TrimSpace(elem.lines[0]), "{") {
					sb.WriteString("}\n")
				}
			}
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// buildBodyFingerprint creates a compact fingerprint of a function body.
// Format: "	// [body: N lines, calls: foo(), bar(), baz()]"
func buildBodyFingerprint(bodyLines []string) string {
	lineCount := len(bodyLines)
	if lineCount == 0 {
		return "\t// [empty body]"
	}

	calls := extractFunctionCalls(bodyLines)
	if len(calls) == 0 {
		return fmt.Sprintf("\t// [body: %d lines]", lineCount)
	}

	// Limit to maxFingerprintCalls
	if len(calls) > maxFingerprintCalls {
		calls = calls[:maxFingerprintCalls]
	}

	return fmt.Sprintf("\t// [body: %d lines, calls: %s]", lineCount, strings.Join(calls, ", "))
}

// funcCallRe matches function/method calls: identifier( or identifier.method(
var funcCallRe = regexp.MustCompile(`\b([a-zA-Z_]\w*(?:\.[a-zA-Z_]\w*)?)\s*\(`)

// extractFunctionCalls extracts unique function call names from body lines,
// sorted by frequency (most common first).
func extractFunctionCalls(bodyLines []string) []string {
	counts := make(map[string]int)
	body := strings.Join(bodyLines, "\n")

	matches := funcCallRe.FindAllStringSubmatch(body, -1)
	for _, m := range matches {
		name := m[1]
		// Skip common keywords that look like function calls
		if isKeyword(name) {
			continue
		}
		counts[name+"()"]++
	}

	// Sort by frequency descending
	type callCount struct {
		name  string
		count int
	}
	var sorted []callCount
	for name, count := range counts {
		sorted = append(sorted, callCount{name, count})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	var result []string
	for _, cc := range sorted {
		result = append(result, cc.name)
	}
	return result
}

// isKeyword returns true if the name is a Go/Python/JS keyword that looks like a call.
func isKeyword(name string) bool {
	keywords := map[string]bool{
		"if": true, "for": true, "switch": true, "select": true,
		"case": true, "return": true, "range": true, "go": true,
		"defer": true, "else": true, "break": true, "continue": true,
		"fallthrough": true, "default": true, "while": true,
		"try": true, "catch": true, "finally": true, "throw": true,
		"new": true, "delete": true, "typeof": true, "instanceof": true,
		"var": true, "let": true, "const": true,
	}
	return keywords[name]
}
