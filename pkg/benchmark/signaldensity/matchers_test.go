package signaldensity

import (
	"testing"
)

func TestMatchers_Exact(t *testing.T) {
	task := TaskCase{MatcherType: MatcherExact, Expected: "42"}
	passed, err := EvaluateCompletion("42", task, "")
	if err != nil || !passed {
		t.Errorf("expected true for exact match")
	}

	passedWrong, _ := EvaluateCompletion("43", task, "")
	if passedWrong {
		t.Errorf("expected false for mismatch")
	}
}

func TestMatchers_Contains(t *testing.T) {
	taskAnd := TaskCase{MatcherType: MatcherContains, Expected: "foo && bar"}
	passed, _ := EvaluateCompletion("Here is foo and also bar in text", taskAnd, "")
	if !passed {
		t.Errorf("expected true for multiple AND terms")
	}

	taskOr := TaskCase{MatcherType: MatcherContains, Expected: "alpha || beta"}
	passedOr, _ := EvaluateCompletion("Only alpha is here", taskOr, "")
	if !passedOr {
		t.Errorf("expected true for OR terms")
	}
}

func TestMatchers_Regex(t *testing.T) {
	task := TaskCase{MatcherType: MatcherRegex, Expected: `\b[0-9]{3}-[0-9]{4}\b`}
	passed, err := EvaluateCompletion("Phone number: 555-1234", task, "")
	if err != nil || !passed {
		t.Errorf("expected regex match")
	}

	passedFail, _ := EvaluateCompletion("No number here", task, "")
	if passedFail {
		t.Errorf("expected regex false")
	}
}

func TestMatchers_JSON(t *testing.T) {
	task := TaskCase{MatcherType: MatcherJSON, Expected: `{"status": "ok", "count": 5}`}

	// With markdown code fences
	resp := "```json\n{\n  \"count\": 5,\n  \"status\": \"ok\"\n}\n```"
	passed, _ := EvaluateCompletion(resp, task, "")
	if !passed {
		t.Errorf("expected JSON match despite ordering and whitespace")
	}

	// Mismatched value
	respWrong := `{"status": "error", "count": 5}`
	passedWrong, _ := EvaluateCompletion(respWrong, task, "")
	if passedWrong {
		t.Errorf("expected false for JSON value mismatch")
	}
}

func TestMatchers_GoTest(t *testing.T) {
	task := TaskCase{
		MatcherType: MatcherGoTest,
		Expected:    "PASS",
		Scaffold: map[string]string{
			"go.mod": "module testmod\n\ngo 1.22.0\n",
			"calc_test.go": `package testmod

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("fail")
	}
}
`,
		},
	}

	validResponse := "```go\n// file: calc.go\npackage testmod\n\nfunc Add(a, b int) int { return a + b }\n```"
	passed, err := EvaluateCompletion(validResponse, task, "")
	if err != nil || !passed {
		t.Errorf("expected go test to pass, got passed=%v, err=%v", passed, err)
	}

	invalidResponse := "```go\n// file: calc.go\npackage testmod\n\nfunc Add(a, b int) int { return 0 }\n```"
	passedInvalid, _ := EvaluateCompletion(invalidResponse, task, "")
	if passedInvalid {
		t.Errorf("expected go test to fail for buggy code")
	}
}
