package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

type DecisionRequest struct {
	QuestionType string                 `json:"question_type"`
	Prompt       string                 `json:"prompt"`
	Options      []string               `json:"options"`
	State        map[string]interface{} `json:"state"`
	Category     string                 `json:"category"`
}

type DecisionResponse struct {
	Answer     string             `json:"answer"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
	LatencyMs  int64              `json:"latency_ms"`
	Error      string             `json:"error,omitempty"`
}

func main() {
	// Signal ready on stdout (matching jev-score protocol)
	fmt.Println(`{"status":"ready","model":"Jev-Style-0.8B-Decision-v3-Q4_K_M"}`)

	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req DecisionRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Printf(`{"error":"%s","answer":"","confidence":0,"latency_ms":0}`+"\n", err.Error())
			continue
		}

		resp := DecisionResponse{
			LatencyMs: 15,
		}

		switch req.QuestionType {
		case "choice":
			if len(req.Options) > 0 {
				resp.Answer = req.Options[0]
				resp.Confidence = 0.92
				scores := make(map[string]float64)
				remaining := 0.08 / float64(len(req.Options))
				for i, opt := range req.Options {
					if i == 0 {
						scores[opt] = 0.92
					} else {
						scores[opt] = remaining
					}
				}
				resp.Scores = scores
			}
		case "score":
			resp.Answer = "0.85"
			resp.Confidence = 0.85
		case "noul":
			resp.Answer = "yes"
			resp.Confidence = 0.95
			resp.Scores = map[string]float64{
				"yes": 0.95,
				"no":  0.05,
			}
		default:
			resp.Answer = "yes"
			resp.Confidence = 0.5
		}

		out, _ := json.Marshal(resp)
		fmt.Println(string(out))
	}
}
