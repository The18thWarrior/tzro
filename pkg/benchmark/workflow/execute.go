package workflow

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type piEvent struct {
	Type       string
	IsError    bool
	ToolName   string
	ToolCallID string
	Args       json.RawMessage
	Result     json.RawMessage
	Message    struct {
		Role, StopReason, ErrorMessage string
		Content                        []struct{ Type, Text string }
		Usage                          *struct{ Input, Output, CacheRead, CacheWrite *int }
	}
}

func runTask(ctx context.Context, cfg Config, task Task, p *prepared) {
	r := &p.result
	recorder, err := newEventRecorder(cfg, r)
	if err != nil {
		r.Status, r.Error = "evidence_incomplete", err.Error()
		return
	}
	defer recorder.close()
	trace := filepath.Join(r.Home, "task-activity.jsonl")
	p.env = append(p.env, "TZRO_RUNTIME_TRACE="+trace)
	defer func() {
		file, err := os.Open(trace)
		if err != nil {
			return
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				r.Activity = append(r.Activity, event)
				if event["runtime"] == "cli:tzro hook" {
					r.Hooks = "invocation observed"
				}
			}
		}
	}()
	start := time.Now()
	agentFinished := false
	defer func() {
		if !agentFinished {
			r.AgentMS = time.Since(start).Milliseconds()
		}
	}()
	taskCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	if r.Profile == Full {
		// Keep the local proxy alive until its final metrics are collected even
		// when the native task context is canceled on a limit or provider error.
		base, stop, err := startProxy(ctx, cfg, p)
		if err != nil {
			r.Status, r.Error = "failed", cleanText(cfg, err.Error())
			return
		}
		defer stop()
		defer func() {
			requests, err := proxyRequests(base)
			if err != nil {
				if r.Status == "completed" {
					r.Status = "usage_incomplete"
				}
				if r.Error != "" {
					r.Error += "; "
				}
				r.Error += "proxy request evidence unavailable"
				r.Usage.Complete = false
			} else {
				r.ProxyRequests = requests
			}
		}()
		if err := writeModelConfig(p, cfg, base); err != nil {
			r.Status, r.Error = "failed", err.Error()
			return
		}
	}
	cmd := command(taskCtx, append(append([]string{}, p.env...), "TZRO_BENCH_API_KEY="+cfg.APIKey), r.Workspace,
		cfg.PiBinary, "--mode", "json", "--print", "--no-session", "--offline", "--no-context-files", "--thinking", "off",
		"--provider", "tzro-workflow", "--model", cfg.Model, task.Prompt)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		r.Status, r.Error = "failed", err.Error()
		return
	}
	var stderr outputBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		r.Status, r.Error = "failed", err.Error()
		return
	}
	r.Usage.Complete = true
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 8*1024*1024)
	reason := ""
	for scanner.Scan() {
		var event piEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			reason = "invalid native client event"
			cancel()
			break
		}
		if err := recorder.record(event, scanner.Bytes()); err != nil {
			r.EvidenceComplete = false
			reason = cleanText(cfg, err.Error())
			cancel()
			break
		}
		switch event.Type {
		case "auto_compaction_start", "auto_retry_start":
			r.Usage.Complete = false
			reason = "native client retry or compaction has unaccounted usage"
			cancel()
		case "agent_end":
			r.AgentEnded = true
		case "tool_execution_start":
			r.ToolCalls++
		case "tool_execution_end":
			if event.IsError {
				r.ToolErrors++
			}
		case "message_end":
			m := event.Message
			if m.Role != "assistant" {
				continue
			}
			r.Usage.Requests++
			u := m.Usage
			turn := Turn{Number: r.Usage.Requests, StopReason: m.StopReason}
			if u == nil || u.Input == nil || u.Output == nil || u.CacheRead == nil || u.CacheWrite == nil || *u.Input < 0 || *u.Output < 0 || *u.CacheRead < 0 || *u.CacheWrite < 0 || (*u.Input == 0 && *u.Output == 0 && *u.CacheRead == 0 && *u.CacheWrite == 0) {
				r.Usage.Complete = false
				reason = "native client usage incomplete"
				cancel()
			} else {
				r.Usage.Input += *u.Input
				r.Usage.Output += *u.Output
				r.Usage.CacheRead += *u.CacheRead
				r.Usage.CacheWrite += *u.CacheWrite
				cost := (float64(r.Usage.Input)*cfg.Prices.Input + float64(r.Usage.Output)*cfg.Prices.Output + float64(r.Usage.CacheRead)*cfg.Prices.CacheRead + float64(r.Usage.CacheWrite)*cfg.Prices.CacheWrite) / 1e6
				r.Usage.EstimatedCostUSD = &cost
				turnCost := (float64(*u.Input)*cfg.Prices.Input + float64(*u.Output)*cfg.Prices.Output + float64(*u.CacheRead)*cfg.Prices.CacheRead + float64(*u.CacheWrite)*cfg.Prices.CacheWrite) / 1e6
				turn.Usage = Usage{Requests: 1, Input: *u.Input, Output: *u.Output, CacheRead: *u.CacheRead, CacheWrite: *u.CacheWrite, Complete: true, EstimatedCostUSD: &turnCost}
			}
			r.Turns = append(r.Turns, turn)
			for _, content := range m.Content {
				if content.Type == "text" {
					r.FinalResponse = cleanText(cfg, content.Text)
				}
			}
			if m.StopReason == "error" || m.StopReason == "aborted" {
				reason = "client " + m.StopReason + ": " + cleanText(cfg, m.ErrorMessage)
			}
			if r.Usage.EstimatedCostUSD != nil && *r.Usage.EstimatedCostUSD >= cfg.MaxCost {
				reason = "cost_limit"
				cancel()
			} else if r.Usage.Requests >= cfg.MaxTurns && m.StopReason == "toolUse" {
				reason = "turn_limit"
				cancel()
			}
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	r.AgentMS = time.Since(start).Milliseconds()
	agentFinished = true
	r.Status = "completed"
	if reason != "" {
		r.Status, r.Error = "failed", reason
	} else if taskCtx.Err() != nil {
		r.Status, r.Error = "timed_out", taskCtx.Err().Error()
	} else if waitErr != nil || scanErr != nil || !r.AgentEnded {
		r.Status, r.Error = "failed", cleanText(cfg, fmt.Sprintf("client did not complete: %v %v %s", waitErr, scanErr, stderr.String()))
	}
	if !r.AgentEnded || r.Usage.Requests == 0 {
		r.Usage.Complete = false
	}
	gradeStart := time.Now()
	r.TaskSuccess, err = grade(ctx, cfg, task, p)
	r.GradeMS = time.Since(gradeStart).Milliseconds()
	if err != nil && r.Status == "completed" {
		r.Status, r.Error = "failed", cleanText(cfg, err.Error())
	}
	if r.Status == "completed" && !r.TaskSuccess {
		r.Status = "failed"
	}
	if r.Status == "completed" && !r.Usage.Complete {
		r.Status = "usage_incomplete"
	}
}

func grade(ctx context.Context, cfg Config, task Task, p *prepared) (bool, error) {
	for name, original := range task.Files {
		if !strings.HasSuffix(name, "_test.go") && name != "go.mod" && !slices.Contains(task.ReadOnlyFiles, name) {
			continue
		}
		path := filepath.Join(p.result.Workspace, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return false, fmt.Errorf("grading file removed or replaced: %s", name)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != original {
			return false, fmt.Errorf("grading file changed: %s", name)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	gradeDir := p.result.Workspace
	if len(task.GradeFiles) > 0 {
		var err error
		gradeDir, err = os.MkdirTemp(filepath.Dir(p.result.Home), "grade-")
		if err != nil {
			return false, err
		}
		defer os.RemoveAll(gradeDir)
		if err := os.CopyFS(gradeDir, os.DirFS(p.result.Workspace)); err != nil {
			return false, err
		}
		for name, body := range task.GradeFiles {
			name = filepath.Clean(name)
			if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(os.PathSeparator)) {
				return false, fmt.Errorf("grading path escapes workspace: %s", name)
			}
			path := filepath.Join(gradeDir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return false, err
			}
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				return false, err
			}
		}
	}
	cmd := command(ctx, p.env, gradeDir, filepath.Join(filepath.Dir(p.result.Home), "tools", "go"), "test", "-count=1", "./...")
	var out outputBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if err != nil {
		return false, fmt.Errorf("task tests failed: %w: %s", err, cleanText(cfg, out.String()))
	}
	return true, nil
}

func cleanText(cfg Config, text string) string {
	if cfg.APIKey != "" {
		text = strings.ReplaceAll(text, cfg.APIKey, "[redacted]")
	}
	if len(text) > 16000 {
		text = text[:16000] + "\n[truncated]"
	}
	return text
}
