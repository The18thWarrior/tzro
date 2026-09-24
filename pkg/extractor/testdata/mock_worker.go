package main

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
)

type ExtractionRequest struct {
	Text   string   `json:"text"`
	Labels []string `json:"labels"`
}

type ExtractedSpan struct {
	Label      string  `json:"label"`
	Text       string  `json:"text"`
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Confidence float64 `json:"confidence"`
}

type ExtractionResponse struct {
	Spans     []ExtractedSpan `json:"spans"`
	LatencyMs int64           `json:"latency_ms"`
}

func main() {
	// Emit ready line (matches real gliner_worker.py protocol)
	os.Stdout.Write([]byte("{\"status\":\"ready\",\"load_ms\":0}\n"))

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		var req ExtractionRequest
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}

		resp := ExtractionResponse{
			Spans: []ExtractedSpan{},
		}

		if strings.Contains(req.Text, "FAIL: TestSessionSave") {
			resp.Spans = []ExtractedSpan{
				{
					Label: "file_path",
					Text:  "pkg/session/manifest_test.go",
					Start: 0,
					End:   28,
				},
				{
					Label: "function_name",
					Text:  "TestSessionSave",
					Start: 30,
					End:   45,
				},
				{
					Label: "line_number",
					Text:  "42",
					Start: 46,
					End:   48,
				},
			}
		}

		out, _ := json.Marshal(resp)
		os.Stdout.Write(append(out, '\n'))
	}
}
