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
	Options      []string               `json:"options,omitempty"`
	State        map[string]interface{} `json:"state"`
}

type DecisionResponse struct {
	Answer     string             `json:"answer"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores,omitempty"`
	LatencyMs  int64              `json:"latency_ms"`
}

func main() {
	// Emit ready line matching the real worker protocol
	fmt.Println(`{"status":"ready"}`)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Bytes()
		var req DecisionRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "Error unmarshalling: %v\n", err)
			continue
		}

		resp := DecisionResponse{
			Confidence: 0.99,
			LatencyMs:  10,
		}

		if req.QuestionType == "noul" {
			resp.Answer = "yes"
		} else if req.QuestionType == "choice" {
			resp.Answer = "option1"
		}

		b, _ := json.Marshal(resp)
		fmt.Println(string(b))
	}
}
