package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGraphReadAndWholeOutput(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"left.txt": "first\r\nsecond\nlast", "right.txt": "independent"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	g := &Graph{Version: "3.0", TaskID: "read-files", Nodes: []Node{
		{ID: "left", Type: NodeTypeTool, Tool: "read", Args: map[string]any{"file": "left.txt"}},
		{ID: "right", Type: NodeTypeTool, Tool: "read", Args: map[string]any{"file": "right.txt"}},
		{ID: "slice", Type: NodeTypeTool, Tool: "read", DependsOn: []string{"left"}, Args: map[string]any{"file": map[string]any{"$ref": "/nodes/left/output/file"}, "offset": float64(2), "limit": float64(1)}},
	}, Returns: []string{"/nodes/left/output", "/nodes/right/output/body", "/nodes/slice/output/body"}}
	result, err := NewEngine(WithToolDispatcher(NewBuiltinDispatcher(root, nil))).Execute(context.Background(), g)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "completed" || result.Returns["nodes/right/output/body"] != "independent" || result.Returns["nodes/slice/output/body"] != "second\n" {
		t.Fatalf("read graph failed: %+v", result)
	}
	whole, ok := result.Returns["nodes/left/output"].(NodeOutput)
	if !ok || whole.Data["body"] != "first\r\nsecond\nlast" || whole.Status != "completed" {
		t.Fatalf("whole output lost: %#v", result.Returns)
	}
}

func TestWholeOutputPointer(t *testing.T) {
	out := NodeOutput{NodeID: "a", Status: "completed", Data: map[string]any{"body": "exact\n"}}
	value, err := resolvePointer("/nodes/a/output", map[string]NodeOutput{"a": out})
	if err != nil {
		t.Fatal(err)
	}
	if value.(NodeOutput).Data["body"] != "exact\n" {
		t.Fatalf("lost output: %#v", value)
	}
}

func TestReadBoundsAndFailures(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"lines.txt": "one\r\ntwo\nthree", "empty.txt": "", "binary.txt": "a\x00b", "large.txt": strings.Repeat("line\n", 220000), ".env": "PRIVATE=fixture"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	d := NewBuiltinDispatcher(root, nil)
	for _, tc := range []struct {
		name       string
		args       map[string]any
		want       string
		start, end int
	}{
		{"full", map[string]any{"file": "lines.txt"}, "one\r\ntwo\nthree", 1, 3},
		{"slice", map[string]any{"file": "lines.txt", "offset": 1, "limit": 1}, "one\r\n", 1, 1},
		{"tail", map[string]any{"file": "lines.txt", "offset": 3, "limit": 9}, "three", 3, 3},
		{"empty", map[string]any{"file": "empty.txt"}, "", 1, 0},
		{"large_slice", map[string]any{"file": "large.txt", "offset": 219999, "limit": 1}, "line\n", 219999, 219999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := d.Dispatch(context.Background(), "read", tc.args)
			if err != nil || got["body"] != tc.want || got["start_line"] != tc.start || got["end_line"] != tc.end {
				t.Fatalf("read = %#v, %v", got, err)
			}
		})
	}
	for _, args := range []map[string]any{
		{}, {"file": "missing.txt"}, {"file": "."}, {"file": "binary.txt"}, {"file": "large.txt"}, {"file": ".env"},
		{"file": "lines.txt", "offset": 0}, {"file": "lines.txt", "offset": 4},
		{"file": "lines.txt", "limit": -1}, {"file": "lines.txt", "limit": 1.5}, {"file": "lines.txt", "limit": "2"},
	} {
		if got, err := d.Dispatch(context.Background(), "read", args); err == nil || got != nil {
			t.Errorf("expected explicit failure for %#v: %#v, %v", args, got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Dispatch(ctx, "read", map[string]any{"file": "lines.txt"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestReadPrivacy(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("PRIVATE=fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, ".env"), filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}
	d := NewBuiltinDispatcher(root, nil)
	if _, err := d.Dispatch(context.Background(), "read", map[string]any{"file": "alias.txt"}); err == nil {
		t.Fatal("symlink bypassed path policy")
	}
	if err := os.Mkdir(filepath.Join(root, ".tzro"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, policy := range []string{`{`, `{"default_action":"block"}`, `{"default_action":"redact"}`, `{"default_action":"allow","rules":[{"data_class":"api_key","action":"block"}]}`} {
		if err := os.WriteFile(filepath.Join(root, ".tzro", "privacy.json"), []byte(policy), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "source.txt"), []byte("sk-proj-1234567890abcdef1234567890abcdef"), 0600); err != nil {
			t.Fatal(err)
		}
		if got, err := d.Dispatch(context.Background(), "read", map[string]any{"file": "source.txt"}); err == nil || got != nil {
			t.Fatalf("policy %s not enforced: %#v, %v", policy, got, err)
		}
	}
}
