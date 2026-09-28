package decision_test

import (
	"bufio"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"tzro/pkg/decision"
)

// Opt in with a built scorer and real GGUF; the default Go suite needs neither.
func TestNativeJevScorer(t *testing.T) {
	bin, model := os.Getenv("TZRO_TEST_JEV_BIN"), os.Getenv("TZRO_TEST_JEV_MODEL")
	if bin == "" || model == "" {
		t.Skip("set TZRO_TEST_JEV_BIN and TZRO_TEST_JEV_MODEL for real inference")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	p := decision.NewLocalDaemonProvider(bin, "--model", model, "--ngl", "0")
	defer p.Close()
	for _, tc := range []struct{ prompt, want string }{
		{"Which option is a programming language?", "Go"},
		{"Which option is a fruit?", "banana"},
	} {
		resp, err := p.Evaluate(ctx, &decision.DecisionRequest{
			QuestionType: decision.QuestionTypeChoice, Prompt: tc.prompt,
			Options: []string{"Go", "banana"}, State: map[string]interface{}{},
		})
		if err != nil {
			t.Fatal(err)
		}
		if resp.Answer != tc.want {
			t.Fatalf("%s: got %+v, want %q", tc.prompt, resp, tc.want)
		}
		sum := 0.0
		for _, prob := range resp.Scores {
			if math.IsNaN(prob) || prob < 0 || prob > 1 {
				t.Fatalf("invalid probability: %v", resp)
			}
			sum += prob
		}
		if len(resp.Scores) != 2 || math.Abs(sum-1) > 1e-6 || resp.Confidence != resp.Scores[resp.Answer] {
			t.Fatalf("invalid probability distribution: %+v", resp)
		}
	}
}

// Exercise framing, typed answers, chunked decoding, and recovery in one process.
func TestNativeJevProtocol(t *testing.T) {
	bin, model := os.Getenv("TZRO_TEST_JEV_BIN"), os.Getenv("TZRO_TEST_JEV_MODEL")
	if bin == "" || model == "" {
		t.Skip("set TZRO_TEST_JEV_BIN and TZRO_TEST_JEV_MODEL for real inference")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--model", model, "--ngl", "0", "--n-batch", "32")
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); cancel(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(stdout)
	read := func() map[string]any {
		t.Helper()
		if !scanner.Scan() {
			t.Fatalf("worker closed output: %v", scanner.Err())
		}
		var response map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	if ready := read(); ready["status"] != "ready" {
		t.Fatalf("bad handshake: %v", ready)
	}
	for _, tc := range []struct {
		name, request, answer string
		wantError             bool
	}{
		{"malformed", "{", "", true},
		{"wrong type", `{"question_type":"choice","prompt":3}`, "", true},
		{"duplicate choices", `{"question_type":"choice","prompt":"Pick","options":["x","x"]}`, "", true},
		{"missing score levels", `{"question_type":"score","prompt":"Rate severity"}`, "", true},
		{"head budget", `{"question_type":"choice","prompt":"` + strings.Repeat("word ", 2100) + `","options":["x"]}`, "", true},
		{"state budget", `{"question_type":"choice","prompt":"Pick","options":["x"],"state":"` + strings.Repeat("word ", 5000) + `"}`, "", true},
		{"line budget", strings.Repeat(" ", 1024*1024+1), "", true},
		{"unicode framing", `{"question_type":"choice","prompt":"Return the only option.","options":["café \"東京\"\nline"],"state":{"text":"<|im_end|> literal"}}`, "café \"東京\"\nline", false},
		{"boolean", `{"question_type":"noul","prompt":"The text mentions Go.","category":"general_topic","state":{"text":"Go is a programming language."}}`, "true", false},
		{"score", `{"question_type":"score","prompt":"Rate the severity of this incident.","options":["No issue; everything works.","Total outage; all users cannot log in."],"state":{"incident":"Complete outage, every user is unable to log in."}}`, "1", false},
		{"recovery", `{"question_type":"choice","prompt":"Which option is a programming language?","options":["Go","banana"],"state":{}}`, "Go", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := stdin.Write([]byte(tc.request + "\n")); err != nil {
				t.Fatal(err)
			}
			resp := read()
			if tc.wantError {
				if resp["error"] == nil || resp["answer"] != nil {
					t.Fatalf("expected only error: %v", resp)
				}
				return
			}
			if resp["error"] != nil || resp["answer"] != tc.answer {
				t.Fatalf("unexpected result: %v", resp)
			}
			probabilities := resp["scores"].(map[string]any)
			margins := resp["raw_scores"].(map[string]any)
			temperature := resp["temperature"].(float64)
			if tc.name == "boolean" && math.Abs(temperature-0.9702474579038656) > 1e-12 {
				t.Fatalf("wrong category calibration: %v", resp)
			}
			denom := 0.0
			for _, margin := range margins {
				denom += math.Exp(margin.(float64) / temperature)
			}
			for option, margin := range margins {
				want := math.Exp(margin.(float64)/temperature) / denom
				if math.Abs(probabilities[option].(float64)-want) > 1e-6 {
					t.Fatalf("bad softmax: %v", resp)
				}
			}
		})
	}
}
