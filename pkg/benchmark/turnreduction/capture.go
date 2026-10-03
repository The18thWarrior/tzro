package turnreduction

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Evidence is written during execution, so an interrupted runner retains its trace.
func captureClient(ctx context.Context, cfg EvaluationConfig, workspace, home, artifactDir string, args []string) (NativeEvents, *int, float64, error) {
	var events NativeEvents
	stdout, err := newRedactedLog(filepath.Join(artifactDir, "events.ndjson"), cfg.APIKey)
	if err != nil {
		return events, nil, 0, err
	}
	defer stdout.Close()
	stderr, err := newRedactedLog(filepath.Join(artifactDir, "stderr.log"), cfg.APIKey)
	if err != nil {
		return events, nil, 0, err
	}
	defer stderr.Close()
	cmd := exec.CommandContext(ctx, cfg.ClientPath, args...)
	cleanup := ownProcessGroup(cmd)
	defer cleanup()
	cmd.Dir = workspace
	cmd.Env = append(nativeEnvironment(home, cfg.APIKey), "GOCACHE="+cfg.GoCache)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	runErr := cmd.Run()
	cleanup()
	seconds := time.Since(start).Seconds()
	var code *int
	if cmd.ProcessState != nil {
		value := cmd.ProcessState.ExitCode()
		code = &value
	}
	closeErr := errors.Join(stdout.Close(), stderr.Close(), retainClientLog(home, artifactDir, cfg.APIKey))
	trace, openErr := os.Open(filepath.Join(artifactDir, "events.ndjson"))
	if openErr != nil {
		return events, code, seconds, errors.Join(runErr, closeErr, openErr)
	}
	defer trace.Close()
	events, parseErr := ParseNativeEvents(trace)
	return events, code, seconds, errors.Join(runErr, closeErr, parseErr)
}

// redactedLog retains only enough trailing bytes to mask a split credential.
type redactedLog struct {
	file    *os.File
	key     []byte
	pending []byte
	closed  bool
}

func newRedactedLog(path, key string) (*redactedLog, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	return &redactedLog{file: file, key: []byte(key)}, nil
}

func (log *redactedLog) Write(data []byte) (int, error) {
	if len(log.key) == 0 {
		return log.file.Write(data)
	}
	log.pending = append(log.pending, data...)
	for {
		index := bytes.Index(log.pending, log.key)
		if index < 0 {
			break
		}
		if _, err := log.file.Write(log.pending[:index]); err != nil {
			return 0, err
		}
		if _, err := log.file.WriteString("[REDACTED]"); err != nil {
			return 0, err
		}
		log.pending = log.pending[index+len(log.key):]
	}
	ready := len(log.pending) - len(log.key) + 1
	if ready > 0 {
		if _, err := log.file.Write(log.pending[:ready]); err != nil {
			return 0, err
		}
		log.pending = append([]byte(nil), log.pending[ready:]...)
	}
	return len(data), nil
}

func (log *redactedLog) Close() error {
	if log.closed {
		return nil
	}
	log.closed = true
	_, err := log.file.Write(log.pending)
	return errors.Join(err, log.file.Sync(), log.file.Close())
}

func retainClientLog(home, artifactDir, key string) error {
	source := filepath.Join(home, ".gemini", "antigravity-cli", "cli.log")
	input, err := os.Open(source)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := newRedactedLog(filepath.Join(artifactDir, "client.log"), key)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	return errors.Join(copyErr, output.Close())
}
