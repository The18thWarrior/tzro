package extractor

// ExtractionRequest is sent to the GLiNER worker.
type ExtractionRequest struct {
	Text   string   `json:"text"`
	Labels []string `json:"labels"` // e.g. ["file_path", "function_name", "line_number"]
}

// ExtractedSpan is a single extracted parameter span.
type ExtractedSpan struct {
	Label      string  `json:"label"`
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

// ExtractionResponse is the worker's answer.
type ExtractionResponse struct {
	Spans     []ExtractedSpan `json:"spans"`
	LatencyMs int64           `json:"latency_ms"`
}
