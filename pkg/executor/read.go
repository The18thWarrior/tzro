package executor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"unicode/utf8"

	"tzro/pkg/dlp"
)

// Bound retained text independently of the CLI's smaller inline result limit.
const maxReadBytes = 1 << 20

func (b *BuiltinDispatcher) dispatchRead(ctx context.Context, args map[string]any) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, _ := args["file"].(string)
	if file == "" {
		return nil, fmt.Errorf("read requires a nonempty 'file' string")
	}
	offset, err := readLineArgument(args, "offset", 1)
	if err != nil {
		return nil, err
	}
	limit, err := readLineArgument(args, "limit", 0)
	if err != nil {
		return nil, err
	}

	policy, err := dlp.LoadWorkspacePolicy(b.WorkspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("read privacy policy: %w", err)
	}
	engine := dlp.NewPolicyEngine(policy)
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(b.WorkspaceRoot, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", file, err)
	}
	for _, candidate := range []string{file, path, canonical} {
		if err := allowExactRead(engine.EvaluatePath(candidate)); err != nil {
			return nil, err
		}
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("read requires a regular text file: %q", file)
	}
	f, err := os.Open(canonical)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), maxReadBytes+1)
	scanner.Split(exactLines)
	var body bytes.Buffer
	line, count := 0, 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line++
		if line < offset {
			continue
		}
		part := scanner.Bytes()
		if bytes.IndexByte(part, 0) >= 0 || !utf8.Valid(part) {
			return nil, fmt.Errorf("read requires UTF-8 text without NUL bytes: %q", file)
		}
		if body.Len()+len(part) > maxReadBytes {
			return nil, fmt.Errorf("read exceeds 1 MiB; use offset and limit to request fewer lines")
		}
		body.Write(part)
		count++
		if limit != 0 && count == limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read failed (maximum line size 1 MiB): %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if offset > line && !(line == 0 && offset == 1) {
		return nil, fmt.Errorf("read offset %d exceeds file length of %d lines", offset, line)
	}
	text := body.String()
	if err := allowExactRead(engine.EvaluateContent(text)); err != nil {
		return nil, err
	}
	return map[string]any{"file": path, "body": text, "start_line": offset, "end_line": offset - 1 + count}, nil
}

func allowExactRead(evaluation dlp.PolicyEvaluation) error {
	if !evaluation.Allowed {
		return fmt.Errorf("read privacy policy: %s", evaluation.Reason)
	}
	if evaluation.Action == dlp.ActionRedact {
		return fmt.Errorf("read cannot return exact text: privacy policy requires redaction")
	}
	return nil
}

func readLineArgument(args map[string]any, name string, fallback int) (int, error) {
	value, exists := args[name]
	if !exists {
		return fallback, nil
	}
	var n int
	switch v := value.(type) {
	case int:
		n = v
	case float64: // JSON-decoded graph arguments.
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Trunc(v) != v || v < 1 || v >= float64(int(^uint(0)>>1)) {
			return 0, fmt.Errorf("read %s must be a positive integer", name)
		}
		n = int(v)
	default:
		return 0, fmt.Errorf("read %s must be a positive integer", name)
	}
	if n < 1 {
		return 0, fmt.Errorf("read %s must be a positive integer", name)
	}
	return n, nil
}

// Preserve CRLF, LF, and the absence of a final newline.
func exactLines(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
