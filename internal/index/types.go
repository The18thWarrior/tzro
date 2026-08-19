package index

// DocChunk represents a parsed chunk of a documentation, markdown, or unstructured text file.
type DocChunk struct {
	ID         string   `json:"id"`
	FilePath   string   `json:"filePath"`
	Kind       string   `json:"kind"` // "doc_section", "doc_page", "doc_text"
	Header     string   `json:"header"`
	Content    string   `json:"content"`
	SymbolRefs []string `json:"symbolRefs"`
	Embedding  []float32 `json:"embedding,omitempty"`
}

// SearchResult is a unified search hit across code symbols and document chunks.
type SearchResult struct {
	ID         string   `json:"id"`
	FilePath   string   `json:"filePath"`
	Kind       string   `json:"kind"`       // "func", "type", "doc_section", etc.
	Title      string   `json:"title"`      // Symbol name or Doc header
	Signature  string   `json:"signature"`  // Func signature / section breadcrumb
	Content    string   `json:"content"`    // Doc comment, body preview, or doc section text
	Score      float64  `json:"score"`      // Composite RRF score or similarity
	SourceType string   `json:"sourceType"` // "code" | "doc"
	SymbolRefs []string `json:"symbolRefs,omitempty"`
}
