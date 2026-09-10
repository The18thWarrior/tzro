package signaldensity

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
)

// EvaluateCompletion checks if the model's response satisfies the task criteria.
func EvaluateCompletion(response string, task TaskCase, workspaceDir string) (bool, error) {
	switch task.MatcherType {
	case MatcherExact:
		return evaluateExact(response, task.Expected), nil

	case MatcherContains:
		return evaluateContains(response, task.Expected), nil

	case MatcherRegex:
		return evaluateRegex(response, task.Expected)

	case MatcherJSON:
		return evaluateJSON(response, task.Expected), nil

	case MatcherGoTest:
		return evaluateGoTest(response, task, workspaceDir)

	default:
		return evaluateContains(response, task.Expected), nil
	}
}

func evaluateExact(response, expected string) bool {
	return strings.TrimSpace(strings.ToLower(response)) == strings.TrimSpace(strings.ToLower(expected))
}

func evaluateContains(response, expected string) bool {
	respLower := strings.ToLower(response)
	// Support multi-term logic: term1 && term2
	if strings.Contains(expected, "&&") {
		parts := strings.Split(expected, "&&")
		for _, p := range parts {
			trimmed := strings.ToLower(strings.TrimSpace(p))
			if trimmed != "" && !strings.Contains(respLower, trimmed) {
				return false
			}
		}
		return true
	}

	// Support alternative logic: term1 || term2
	if strings.Contains(expected, "||") {
		parts := strings.Split(expected, "||")
		for _, p := range parts {
			trimmed := strings.ToLower(strings.TrimSpace(p))
			if trimmed != "" && strings.Contains(respLower, trimmed) {
				return true
			}
		}
		return false
	}

	return strings.Contains(respLower, strings.ToLower(strings.TrimSpace(expected)))
}

func evaluateRegex(response, pattern string) (bool, error) {
	re, err := regexp.Compile("(?i)" + pattern)
	if err != nil {
		return false, fmt.Errorf("invalid regex pattern %q: %w", pattern, err)
	}
	return re.MatchString(response), nil
}

func extractJSONFromResponse(response string) string {
	trimmed := strings.TrimSpace(response)
	// Strip markdown code fences if present
	if strings.Contains(trimmed, "```") {
		re := regexp.MustCompile("(?s)```(?:json)?\\s*(.+?)\\s*```")
		if matches := re.FindStringSubmatch(trimmed); len(matches) > 1 {
			trimmed = strings.TrimSpace(matches[1])
		}
	}
	return trimmed
}

func evaluateJSON(response, expected string) bool {
	extracted := extractJSONFromResponse(response)

	var expData any
	if err := json.Unmarshal([]byte(expected), &expData); err != nil {
		// Fallback to substring matching if expected isn't valid JSON
		return strings.Contains(response, expected)
	}

	var respData any
	if err := json.Unmarshal([]byte(extracted), &respData); err != nil {
		// Try searching for JSON array or object inside response
		startObj := strings.Index(response, "{")
		startArr := strings.Index(response, "[")
		start := -1
		if startObj >= 0 && (startArr < 0 || startObj < startArr) {
			start = startObj
		} else if startArr >= 0 {
			start = startArr
		}

		if start >= 0 {
			for end := len(response); end > start; end-- {
				candidate := response[start:end]
				if err := json.Unmarshal([]byte(candidate), &respData); err == nil {
					return reflect.DeepEqual(respData, expData)
				}
			}
		}
		return false
	}

	return reflect.DeepEqual(respData, expData)
}

// evaluateGoTest handles mini-macro coding evaluation.
func evaluateGoTest(response string, task TaskCase, workspaceDir string) (bool, error) {
	if workspaceDir == "" {
		tmp, err := os.MkdirTemp("", "tzro-sdm-macro-*")
		if err != nil {
			return false, err
		}
		defer os.RemoveAll(tmp)
		workspaceDir = tmp
	}

	// 1. Scaffold files in workspace
	for relPath, content := range task.Scaffold {
		fullPath := filepath.Join(workspaceDir, relPath)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return false, err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0644); err != nil {
			return false, err
		}
	}

	// 2. Extract code files from model response (e.g. ```go ... ``` blocks or diff)
	applyPatchesFromResponse(workspaceDir, response)

	// 3. Run `go test ./...`
	cmd := exec.Command("go", "test", "./...")
	cmd.Dir = workspaceDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		_ = out
		return false, nil
	}

	return true, nil
}

// applyPatchesFromResponse looks for filename headers and code fences in model output.
func applyPatchesFromResponse(workspaceDir, response string) {
	// Look for pattern: File: `path` or `path`\n```go\n...```
	fenceRe := regexp.MustCompile("(?s)(?:(?:File|path):?\\s*`?([a-zA-Z0-9_./\\-]+)`?\\s*\\n)?```(?:go|golang)?\\s*\\n(.+?)\\s*```")
	matches := fenceRe.FindAllStringSubmatch(response, -1)

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}
		filePath := strings.TrimSpace(match[1])
		code := match[2]

		if filePath == "" {
			// Check if first line inside code has `// file: path`
			lines := strings.Split(code, "\n")
			if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "// file:") {
				filePath = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[0]), "// file:"))
			}
		}

		if filePath != "" {
			target := filepath.Join(workspaceDir, filePath)
			_ = os.MkdirAll(filepath.Dir(target), 0755)
			_ = os.WriteFile(target, []byte(code), 0644)
		}
	}
}
