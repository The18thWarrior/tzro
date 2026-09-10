package search

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	ignore "github.com/sabhiram/go-gitignore"
	"tzro/pkg/dlp"
	"tzro/pkg/evidence"
	"tzro/pkg/store"
)

// SearchResult represents the unified evidence search envelope.
type SearchResult struct {
	Query       string                  `json:"query"`
	Budget      int                     `json:"budget"`
	UsedTokens  int                     `json:"used_tokens"`
	Items       []evidence.EvidenceItem `json:"items"`
	GeneratedAt time.Time               `json:"generated_at"`
}

// FormatMarkdown outputs agent-readable markdown for search evidence.
func (sr *SearchResult) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Unified Evidence Search: %q (Budget: %d tokens, Used: ~%d tokens)\n\n", sr.Query, sr.Budget, sr.UsedTokens))

	if len(sr.Items) == 0 {
		sb.WriteString("No matching evidence found across workspace files, documents, logs, or stored data.\n")
		return sb.String()
	}

	for i, item := range sr.Items {
		sb.WriteString(fmt.Sprintf("## [%d] `%s` (%s)\n", i+1, item.SourcePath, item.SourceKind))
		sb.WriteString(fmt.Sprintf("- **Hash:** `%s`\n", item.ContentHash[:min(16, len(item.ContentHash))]))
		if item.Revision != "" {
			sb.WriteString(fmt.Sprintf("- **Revision:** `%s`\n", item.Revision[:min(8, len(item.Revision))]))
		}
		if item.Anchor.LineRange != nil {
			sb.WriteString(fmt.Sprintf("- **Lines:** %d-%d\n", item.Anchor.LineRange.StartLine, item.Anchor.LineRange.EndLine))
		} else if len(item.Anchor.SectionPath) > 0 {
			sb.WriteString(fmt.Sprintf("- **Section:** %s\n", strings.Join(item.Anchor.SectionPath, " > ")))
		}

		if item.TabularData != nil {
			sb.WriteString(fmt.Sprintf("- **Status:** %s\n", item.TabularData.Status))
			if item.TabularData.Status == evidence.TabularStatusNotIngested {
				sb.WriteString(fmt.Sprintf("> 💡 Discovered tabular data. Ingest into SQLite using: `tzro ingest %s`\n", item.SourcePath))
			} else {
				sb.WriteString(fmt.Sprintf("- **Table:** `%s` (%d rows)\n", item.TabularData.TableName, item.TabularData.RowCount))
			}
		}

		if item.Content != "" {
			sb.WriteString("\n```\n")
			sb.WriteString(strings.TrimRight(item.Content, "\n"))
			sb.WriteString("\n```\n\n")
		} else {
			sb.WriteString("\n")
		}
	}

	return sb.String()
}

// Engine executes unified local evidence searches across the four source categories.
type Engine struct {
	store  *store.Store
	policy *dlp.PolicyEngine
}

// NewEngine creates a new search engine.
func NewEngine(s *store.Store, policy *dlp.PolicyEngine) *Engine {
	return &Engine{
		store:  s,
		policy: policy,
	}
}

// Search searches across repository files, binary docs, explicit imports, and store artifacts.
func (e *Engine) Search(workspaceRoot, query string, budget int) (*SearchResult, error) {
	if budget <= 0 {
		budget = 4000
	}

	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return &SearchResult{Query: query, Budget: budget, GeneratedAt: time.Now().UTC()}, nil
	}

	var ign *ignore.GitIgnore
	gitIgnorePath := filepath.Join(workspaceRoot, ".gitignore")
	if gitIgnoreContent, err := os.ReadFile(gitIgnorePath); err == nil {
		lines := strings.Split(string(gitIgnoreContent), "\n")
		ign = ignore.CompileIgnoreLines(lines...)
	}

	defaultIgnores := map[string]bool{
		".git":         true,
		".tzro":        true,
		"node_modules": true,
		"vendor":       true,
		"dist":         true,
		"bin":          true,
	}

	var candidates []evidence.EvidenceItem
	seenHashes := make(map[string]bool)

	// 1 & 2. Scan workspace files (text files & binary documents)
	_ = filepath.WalkDir(workspaceRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relPath, _ := filepath.Rel(workspaceRoot, path)
		if relPath == "." {
			return nil
		}

		if d.IsDir() {
			if defaultIgnores[d.Name()] {
				return filepath.SkipDir
			}
			if ign != nil && ign.MatchesPath(relPath) {
				return filepath.SkipDir
			}
			// Privacy check on directories
			if e.policy != nil {
				eval := e.policy.EvaluatePath(relPath)
				if !eval.Allowed {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if ign != nil && ign.MatchesPath(relPath) {
			return nil
		}

		// Privacy check: silent omission for denied paths
		if e.policy != nil {
			eval := e.policy.EvaluatePath(relPath)
			if !eval.Allowed {
				return nil
			}
		}

		ext := strings.ToLower(filepath.Ext(path))

		// Tabular data discovery marker check
		if ext == ".csv" || ext == ".tsv" {
			info, statErr := d.Info()
			mtime := time.Now().UTC()
			if statErr == nil {
				mtime = info.ModTime().UTC()
			}
			dataBytes, _ := os.ReadFile(path)
			hash := sha256Hex(dataBytes)

			// Check if filename or content matches query terms
			lowerPath := strings.ToLower(relPath)
			lowerContent := strings.ToLower(string(dataBytes))
			matches := false
			for _, term := range terms {
				if strings.Contains(lowerPath, term) || strings.Contains(lowerContent, term) {
					matches = true
					break
				}
			}

			if matches {
				candidates = append(candidates, evidence.EvidenceItem{
					SourcePath:  relPath,
					ContentHash: hash,
					Timestamp:   mtime,
					Anchor: evidence.Anchor{
						LineRange: &evidence.LineRange{StartLine: 1, EndLine: 1},
					},
					SourceKind:  evidence.SourceKindData,
					WorkspaceID: workspaceRoot,
					TabularData: &evidence.TabularMetadata{
						Status: evidence.TabularStatusNotIngested,
					},
					Score: 50.0,
				})
			}
			return nil
		}

		// Document and text file extraction
		var textContent string
		var extractErr error

		switch ext {
		case ".docx":
			textContent, extractErr = extractDOCX(path)
		case ".pptx":
			textContent, extractErr = extractPPTX(path)
		case ".pdf":
			textContent, extractErr = extractPDF(path)
		default:
			// Text files (code, config, doc)
			isText := isTextExtension(ext)
			if !isText {
				return nil
			}
			bytes, readErr := os.ReadFile(path)
			if readErr != nil {
				return nil
			}
			textContent = string(bytes)
		}

		if extractErr != nil || textContent == "" {
			return nil
		}

		// Privacy check on extracted content: silent omission
		if e.policy != nil {
			eval := e.policy.EvaluateContent(textContent)
			if !eval.Allowed {
				return nil
			}
		}

		// Check relevance to query
		lowerContent := strings.ToLower(textContent)
		score := 0.0
		for _, term := range terms {
			if strings.Contains(lowerContent, term) {
				score += 30.0
			}
		}

		if score > 0 {
			hash := sha256Hex([]byte(textContent))
			if seenHashes[hash] {
				return nil // Deduplicate
			}
			seenHashes[hash] = true

			info, statErr := d.Info()
			mtime := time.Now().UTC()
			if statErr == nil {
				mtime = info.ModTime().UTC()
			}

			kind := evidence.ClassifySourceKind(relPath, false, "")
			lines := strings.Split(textContent, "\n")
			firstMatchLine := 1
			for i, line := range lines {
				lineLower := strings.ToLower(line)
				for _, term := range terms {
					if strings.Contains(lineLower, term) {
						firstMatchLine = i + 1
						break
					}
				}
				if firstMatchLine > 1 {
					break
				}
			}

			start := max(1, firstMatchLine-2)
			end := min(len(lines), firstMatchLine+15)
			excerpt := strings.Join(lines[start-1:end], "\n")

			candidates = append(candidates, evidence.EvidenceItem{
				SourcePath:  relPath,
				ContentHash: hash,
				Timestamp:   mtime,
				Anchor: evidence.Anchor{
					LineRange: &evidence.LineRange{StartLine: start, EndLine: end},
				},
				SourceKind:  kind,
				WorkspaceID: workspaceRoot,
				Content:     excerpt,
				Score:       score,
			})
		}

		return nil
	})

	// 4. Content-Hash Store artifacts (logs, sessions, etc.)
	if e.store != nil {
		artifacts, err := e.store.ListArtifacts(workspaceRoot)
		if err == nil {
			for _, art := range artifacts {
				fullArt, getErr := e.store.GetArtifact(art.ID, workspaceRoot)
				if getErr != nil || fullArt == nil {
					// Expired or missing tombstone
					candidates = append(candidates, evidence.EvidenceItem{
						SourcePath:  art.ID,
						ContentHash: art.Hash,
						Timestamp:   art.CreatedAt,
						Anchor: evidence.Anchor{
							LineRange: &evidence.LineRange{StartLine: 1, EndLine: 1},
						},
						SourceKind:  evidence.ClassifySourceKind(art.ID, true, art.Type),
						WorkspaceID: workspaceRoot,
						Expired:     true,
					})
					continue
				}

				lowerBody := strings.ToLower(fullArt.Body)
				score := 0.0
				for _, term := range terms {
					if strings.Contains(lowerBody, term) {
						score += 35.0
					}
				}

				if score > 0 {
					if seenHashes[fullArt.Hash] {
						continue
					}
					seenHashes[fullArt.Hash] = true

					kind := evidence.ClassifySourceKind(fullArt.ID, true, fullArt.Type)
					bodySnippet := fullArt.Body
					lines := strings.Split(bodySnippet, "\n")
					if len(lines) > 20 {
						bodySnippet = strings.Join(lines[:20], "\n")
					}

					candidates = append(candidates, evidence.EvidenceItem{
						SourcePath:  fullArt.ID,
						ContentHash: fullArt.Hash,
						Timestamp:   fullArt.CreatedAt,
						Anchor: evidence.Anchor{
							LineRange: &evidence.LineRange{StartLine: 1, EndLine: min(len(lines), 20)},
						},
						SourceKind:  kind,
						WorkspaceID: workspaceRoot,
						Content:     bodySnippet,
						Score:       score,
					})
				}
			}
		}
	}

	// Sort candidates: score DESC, timestamp DESC
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].Timestamp.After(candidates[j].Timestamp)
	})

	// Knapsack packing under budget
	res := &SearchResult{
		Query:       query,
		Budget:      budget,
		GeneratedAt: time.Now().UTC(),
	}

	used := 0
	for _, c := range candidates {
		weight := estimateTokens(c.Content)
		if used+weight <= budget {
			res.Items = append(res.Items, c)
			used += weight
		}
		if used >= budget {
			break
		}
	}
	res.UsedTokens = used

	return res, nil
}

func estimateTokens(text string) int {
	t := len(text) / 4
	if t == 0 && len(text) > 0 {
		return 1
	}
	return t
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func isTextExtension(ext string) bool {
	switch ext {
	case ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".rs", ".java",
		".c", ".cpp", ".cc", ".h", ".hpp", ".rb", ".php", ".cs",
		".md", ".txt", ".rst", ".adoc", ".yaml", ".yml", ".json",
		".toml", ".ini", ".cfg", ".conf", ".sql", ".sh", ".zsh", ".bash":
		return true
	default:
		return false
	}
}

// Pure-Go DOCX parser (word/document.xml extraction)
func extractDOCX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	for _, f := range r.File {
		if f.Name == "word/document.xml" {
			rc, err := f.Open()
			if err != nil {
				return "", err
			}
			defer rc.Close()

			return parseOfficeXMLText(rc, "t", "p")
		}
	}
	return "", nil
}

// Pure-Go PPTX parser (ppt/slides/slide*.xml extraction)
func extractPPTX(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var parts []string
	for _, f := range r.File {
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			rc, err := f.Open()
			if err == nil {
				text, _ := parseOfficeXMLText(rc, "t", "p")
				rc.Close()
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.Join(parts, "\n\n"), nil
}

func parseOfficeXMLText(r io.Reader, textTag, paraTag string) (string, error) {
	decoder := xml.NewDecoder(r)
	var paras []string
	var curr strings.Builder
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
			if t.Name.Local == textTag {
				inText = true
			}
		case xml.EndElement:
			if t.Name.Local == textTag {
				inText = false
			}
			if t.Name.Local == paraTag {
				str := strings.TrimSpace(curr.String())
				if str != "" {
					paras = append(paras, str)
				}
				curr.Reset()
			}
		case xml.CharData:
			if inText {
				curr.Write(t)
			}
		}
	}

	if curr.Len() > 0 {
		paras = append(paras, strings.TrimSpace(curr.String()))
	}

	return strings.Join(paras, "\n"), nil
}

// Pure-Go basic text extraction from PDF
func extractPDF(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	content := string(data)
	// Extract plain text blocks between BT and ET operators or parentheses
	textRe := regexp.MustCompile(`\(([^)]+)\)\s*Tj`)
	matches := textRe.FindAllStringSubmatch(content, -1)
	if len(matches) > 0 {
		var parts []string
		for _, m := range matches {
			parts = append(parts, m[1])
		}
		return strings.Join(parts, " "), nil
	}

	// Fallback: check for ASCII strings in text streams
	asciiRe := regexp.MustCompile(`[a-zA-Z0-9\s,\.]{4,}`)
	asciiMatches := asciiRe.FindAllString(content, -1)
	if len(asciiMatches) > 0 {
		return strings.Join(asciiMatches, " "), nil
	}

	return "", nil
}
