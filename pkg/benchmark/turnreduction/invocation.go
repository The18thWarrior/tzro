package turnreduction

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tzro/pkg/benchmark/turnreduction/invocation"
)

// Install the same passive observer in every condition without replacing product hooks.
func installInvocationObserver(recorder, home, artifactDir string) error {
	path := filepath.Join(home, ".gemini", "config", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	config := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(data, &config); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	const name = "tzro-benchmark-invocations"
	if _, exists := config[name]; exists {
		return fmt.Errorf("invocation observer already configured")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	observer := map[string]any{}
	for _, event := range []string{"PreInvocation", "PostInvocation", "Stop"} {
		command := quote(recorder) + " --event " + quote(event) + " --output " + quote(filepath.Join(artifactDir, "invocations.ndjson"))
		observer[event] = []map[string]any{{"type": "command", "command": command, "timeout": 10}}
	}
	config[name], err = json.Marshal(observer)
	if err != nil {
		return err
	}
	return writeJSON(path, config)
}

func observeInvocations(artifactDir string, events *NativeEvents, agentSeconds float64) error {
	events.CloudDecisionRounds = nil
	summary := invocation.Summary{}
	input, err := os.Open(filepath.Join(artifactDir, "invocations.ndjson"))
	if err == nil {
		summary, err = invocation.Parse(input, events.ConversationID)
		_ = input.Close()
	}
	if err == nil && (events.Status != "SUCCESS" || summary.ModelIntervalSeconds > agentSeconds) {
		err = fmt.Errorf("invocation trace contradicts native outcome or duration")
	}
	if err == nil && summary.Completed != events.AgentResponseSteps {
		err = fmt.Errorf("hook completions do not match native agent-response events")
	}
	if err != nil {
		summary.Complete = false
		summary.Problem = err.Error()
	}
	events.Invocations = &summary
	if err == nil {
		value := summary.Completed
		events.CloudDecisionRounds = &value
	}
	return errors.Join(err, writeJSON(filepath.Join(artifactDir, "invocation-summary.json"), summary))
}
