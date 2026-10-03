package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tzro/pkg/dlp"
)

// Native event evidence is separate from hook activity. Hook entry alone cannot
// prove transformation, and resource discovery cannot prove a successful read.
type eventRecorder struct {
	file     *os.File
	result   *Result
	config   Config
	redactor *dlp.Redactor
	started  map[string]time.Time
	indices  map[string]int
}

func newEventRecorder(cfg Config, r *Result) (*eventRecorder, error) {
	r.TracePath = filepath.Join(r.Home, "task-events.jsonl")
	f, err := os.OpenFile(r.TracePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	r.EvidenceComplete = true
	return &eventRecorder{file: f, result: r, config: cfg, redactor: dlp.NewRedactor(), started: map[string]time.Time{}, indices: map[string]int{}}, nil
}

func (e *eventRecorder) close() {
	err := e.file.Close()
	if err == nil {
		e.result.TraceSHA256, err = fileDigest(e.result.TracePath)
	}
	if err != nil {
		e.result.EvidenceComplete = false
		e.result.Error += "; native trace persistence failed: " + err.Error()
		if e.result.Status == "completed" {
			e.result.Status = "evidence_incomplete"
		}
	}
}

func (e *eventRecorder) redact(value any) any {
	switch v := value.(type) {
	case string:
		if e.config.APIKey != "" {
			v = strings.ReplaceAll(v, e.config.APIKey, "[redacted]")
		}
		v, _ = e.redactor.Redact(v)
		return v
	case map[string]any:
		for k, item := range v {
			v[k] = e.redact(item)
		}
	case []any:
		for i, item := range v {
			v[i] = e.redact(item)
		}
	}
	return value
}

func (e *eventRecorder) record(event piEvent, raw []byte) error {
	// Stream deltas duplicate the final messages. Preserve semantic boundaries,
	// complete tool payloads, terminal states, and all retry/compaction signals.
	switch event.Type {
	case "message_end", "tool_execution_start", "tool_execution_end", "agent_end", "auto_retry_start", "auto_retry_end", "auto_compaction_start", "auto_compaction_end":
	default:
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	data, err := json.Marshal(e.redact(value))
	if err != nil {
		return err
	}
	if _, err := e.file.Write(append(data, '\n')); err != nil {
		e.result.EvidenceComplete = false
		return fmt.Errorf("write native trace: %w", err)
	}
	var safe piEvent
	if err := json.Unmarshal(data, &safe); err != nil {
		return err
	}
	switch event.Type {
	case "tool_execution_start":
		e.indices[event.ToolCallID] = len(e.result.Tools)
		e.started[event.ToolCallID] = time.Now()
		e.result.Tools = append(e.result.Tools, ToolCall{ID: safe.ToolCallID, Name: safe.ToolName, Arguments: safe.Args})
	case "tool_execution_end":
		index, ok := e.indices[event.ToolCallID]
		if !ok {
			e.result.EvidenceComplete = false
			return fmt.Errorf("tool completion without start: %s", event.ToolCallID)
		}
		tool := &e.result.Tools[index]
		tool.Completed, tool.IsError, tool.Result = true, safe.IsError, safe.Result
		tool.DurationMS = time.Since(e.started[event.ToolCallID]).Milliseconds()
		var result struct{ Content []struct{ Type, Text string } }
		_ = json.Unmarshal(event.Result, &result)
		for _, item := range result.Content {
			if item.Type == "text" {
				tool.OutputBytes += len(item.Text)
			}
		}
		var args struct{ Path string }
		_ = json.Unmarshal(tool.Arguments, &args)
		if tool.Name == "read" && !tool.IsError && strings.HasSuffix(filepath.ToSlash(args.Path), "/skills/tzro/SKILL.md") {
			e.result.SkillRead = true
		}
	}
	return nil
}
