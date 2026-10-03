package hooks

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/store"
)

func TestPiWholeSourceReadIsRecoverable(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var code strings.Builder
	code.WriteString("package sample\n")
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&code, "func Function%d() int {\n%sreturn %d\n}\n", i, strings.Repeat("// An implementation detail that can be recovered when needed.\n", 8), i)
	}
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte(code.String()), 0600); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(t.TempDir(), "activity.jsonl")
	t.Setenv("TZRO_RUNTIME_TRACE", trace)
	call := func(args map[string]any) string {
		t.Helper()
		input, _ := json.Marshal(PiCoderPostToolInput{ToolName: "read", ToolInput: args, ToolOutput: code.String()})
		var output bytes.Buffer
		if err := HandlePiCoderPostTool(bytes.NewReader(input), &output, s); err != nil {
			t.Fatal(err)
		}
		var result PiCoderPostToolOutput
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.ToolOutput.(string)
	}
	compacted := call(map[string]any{"path": path})
	if len(compacted) >= len(code.String())/2 || !strings.Contains(compacted, "tzro expand") || !strings.Contains(compacted, "func Function49() int") {
		t.Fatalf("whole-source read was not usefully compacted: %d -> %d bytes", code.Len(), len(compacted))
	}
	cwd, _ := os.Getwd()
	symbols, err := s.SearchSymbols(cwd, "Function49", 10)
	if err != nil || len(symbols) == 0 {
		t.Fatal("elided implementation was not stored", err)
	}
	blob, err := s.GetBlob(symbols[0].Hash)
	if err != nil || !strings.Contains(blob.Body, "return 49") {
		t.Fatal("implementation cannot be recovered", err)
	}
	if got := call(map[string]any{"path": path, "offset": 1, "limit": 500}); got != code.String() {
		t.Fatal("explicit source slice was altered")
	}
	evidence, err := os.ReadFile(trace)
	if err != nil || !bytes.Contains(evidence, []byte(`"status":"transformed"`)) || !bytes.Contains(evidence, []byte(`"input_bytes"`)) {
		t.Fatalf("hook outcome missing: %s %v", evidence, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := call(map[string]any{"path": path}); got != code.String() {
		t.Fatal("source was elided despite an unavailable recovery store")
	}
}
