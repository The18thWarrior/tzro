package executor

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
)

var (
	referencesHeaderRe = regexp.MustCompile(`(?i)(?:^|\n)##+\s+(?:references|sources|bibliography|verified sources|citations)`)
	bracketCiteRe      = regexp.MustCompile(`\[\d+\]`)
)

// WebSource represents a visited or scraped web resource.
type WebSource struct {
	URL      string   `json:"url"`
	Title    string   `json:"title"`
	Snippet  string   `json:"snippet,omitempty"`
	KeyFacts []string `json:"keyFacts,omitempty"`
}

// FileSource represents a read or analyzed local codebase file.
type FileSource struct {
	Path      string   `json:"path"`
	Symbols   []string `json:"symbols,omitempty"`
	LineCount int      `json:"lineCount,omitempty"`
	Snippet   string   `json:"snippet,omitempty"`
}

// DataSource represents a queried cache or structured database table.
type DataSource struct {
	CacheID   string `json:"cacheId"`
	TableName string `json:"tableName,omitempty"`
	Query     string `json:"query,omitempty"`
	RowCount  int    `json:"rowCount,omitempty"`
	Summary   string `json:"summary,omitempty"`
}

// SourceTracker records verified provenance across Web, File, and Data operations.
type SourceTracker struct {
	mu          sync.RWMutex
	WebSources  []WebSource
	FileSources []FileSource
	DataSources []DataSource

	seenWeb  map[string]bool
	seenFile map[string]bool
	seenData map[string]bool
}

// NewSourceTracker creates an initialized SourceTracker.
func NewSourceTracker() *SourceTracker {
	return &SourceTracker{
		seenWeb:  make(map[string]bool),
		seenFile: make(map[string]bool),
		seenData: make(map[string]bool),
	}
}

// AddWebSource tracks a verified web page.
func (st *SourceTracker) AddWebSource(url, title, snippet string, keyFacts []string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.seenWeb == nil {
		st.seenWeb = make(map[string]bool)
	}
	if url == "" || st.seenWeb[url] {
		return
	}
	st.seenWeb[url] = true
	if title == "" {
		title = url
	}
	st.WebSources = append(st.WebSources, WebSource{
		URL:      url,
		Title:    title,
		Snippet:  snippet,
		KeyFacts: keyFacts,
	})
}

// AddFileSource tracks an accessed codebase file.
func (st *SourceTracker) AddFileSource(path string, symbols []string, lineCount int, snippet string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.seenFile == nil {
		st.seenFile = make(map[string]bool)
	}
	if path == "" || st.seenFile[path] {
		return
	}
	st.seenFile[path] = true
	st.FileSources = append(st.FileSources, FileSource{
		Path:      path,
		Symbols:   symbols,
		LineCount: lineCount,
		Snippet:   snippet,
	})
}

// AddDataSource tracks a queried structured data table or cache.
func (st *SourceTracker) AddDataSource(cacheID, tableName, query string, rowCount int, summary string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.seenData == nil {
		st.seenData = make(map[string]bool)
	}
	key := fmt.Sprintf("%s:%s:%s", cacheID, tableName, query)
	if st.seenData[key] {
		return
	}
	st.seenData[key] = true
	st.DataSources = append(st.DataSources, DataSource{
		CacheID:   cacheID,
		TableName: tableName,
		Query:     query,
		RowCount:  rowCount,
		Summary:   summary,
	})
}

// HasSources returns true if any web, file, or data sources have been recorded.
func (st *SourceTracker) HasSources() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return len(st.WebSources) > 0 || len(st.FileSources) > 0 || len(st.DataSources) > 0
}

// FormatReferencesMarkdown builds a deterministic markdown section of all verified sources.
func (st *SourceTracker) FormatReferencesMarkdown() string {
	st.mu.RLock()
	defer st.mu.RUnlock()

	if len(st.WebSources) == 0 && len(st.FileSources) == 0 && len(st.DataSources) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## References & Verified Sources\n\n")

	if len(st.WebSources) > 0 {
		for i, ws := range st.WebSources {
			title := ws.Title
			if title == "" || title == ws.URL {
				title = "Documentation / Source"
			}
			b.WriteString(fmt.Sprintf("- [%d] [%s](%s)\n", i+1, title, ws.URL))
		}
		b.WriteString("\n")
	}

	if len(st.FileSources) > 0 {
		b.WriteString("### Verified Codebase Files\n")
		for _, fs := range st.FileSources {
			symInfo := ""
			if len(fs.Symbols) > 0 {
				symInfo = fmt.Sprintf(" (Symbols: %s)", strings.Join(fs.Symbols, ", "))
			}
			b.WriteString(fmt.Sprintf("- `%s`%s\n", fs.Path, symInfo))
		}
		b.WriteString("\n")
	}

	if len(st.DataSources) > 0 {
		b.WriteString("### Data Caches & Queries\n")
		for _, ds := range st.DataSources {
			querySnippet := ""
			if ds.Query != "" {
				querySnippet = fmt.Sprintf(" — `%s`", ds.Query)
			}
			b.WriteString(fmt.Sprintf("- Cache `%s` (%s%s)\n", ds.CacheID, ds.TableName, querySnippet))
		}
		b.WriteString("\n")
	}

	return strings.TrimSpace(b.String())
}

// InjectOrNormalizeReferences ensures that the synthesis contains a canonical references section
// referencing verified URLs, files, or data caches.
func (st *SourceTracker) InjectOrNormalizeReferences(synthesis string) string {
	trimmed := strings.TrimSpace(synthesis)
	if trimmed == "" {
		return st.FormatReferencesMarkdown()
	}

	if !st.HasSources() {
		return trimmed
	}

	// If a references section already exists and contains markdown links or file names, keep as-is
	if referencesHeaderRe.MatchString(trimmed) {
		// If existing header has http links or file extensions, assume model fulfilled it
		if strings.Contains(trimmed, "http://") || strings.Contains(trimmed, "https://") || strings.Contains(trimmed, ".go") || strings.Contains(trimmed, ".ts") {
			return trimmed
		}
	}

	// Build the appendix
	appendix := st.FormatReferencesMarkdown()
	if appendix == "" {
		return trimmed
	}

	return fmt.Sprintf("%s\n\n%s", trimmed, appendix)
}

var urlInContextRe = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)

// ExtractSourcesFromRefinedContext extracts verified URLs from compacted context strings.
func ExtractSourcesFromRefinedContext(ctxText string) *SourceTracker {
	st := NewSourceTracker()
	if ctxText == "" {
		return st
	}

	urls := urlInContextRe.FindAllString(ctxText, -1)
	for _, u := range urls {
		uClean := strings.TrimRight(u, ".,;:!?'\"")
		if !strings.Contains(uClean, "example.com") && !strings.Contains(uClean, "schema.org") && len(uClean) > 10 {
			st.AddWebSource(uClean, "", "", nil)
		}
	}

	return st
}
