package compactor

import (
	"strings"
	"testing"
)

func TestSmartJSONCrusher(t *testing.T) {
	input := `[
		{"id": 1, "name": "Alice", "role": "admin"},
		{"id": 2, "name": "Bob", "role": "user"},
		{"id": 3, "name": "Charlie", "role": "developer"}
	]`

	crushed := SmartJSONCrusher(input)
	if !strings.Contains(crushed, "# Compressed JSON Table (3 rows)") {
		t.Errorf("expected table header, got %s", crushed)
	}
	if !strings.Contains(crushed, "| id | name | role |") {
		t.Errorf("expected sorted column headers, got %s", crushed)
	}
	if !strings.Contains(crushed, "| 1 | Alice | admin |") {
		t.Errorf("expected row content, got %s", crushed)
	}
}

func TestStackTraceElider(t *testing.T) {
	input := `panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x104b2c8]
goroutine 1 [running]:
main.HandleRequest(0x0)
	/Users/dev/project/handler.go:42 +0x28
runtime/panic.go:838 +0x207
runtime/proc.go:271 +0x45
testing.go:1234 +0x56
main.main()
	/Users/dev/project/main.go:15 +0x1f
`

	pruned := StackTraceElider(input)
	if !strings.Contains(pruned, "main.HandleRequest(0x0)") {
		t.Errorf("expected user code frame preserved")
	}
	if !strings.Contains(pruned, "[3 framework/runtime frames elided]") {
		t.Errorf("expected elision marker, got:\n%s", pruned)
	}
	if strings.Contains(pruned, "runtime/panic.go:838") {
		t.Errorf("expected runtime frame to be elided")
	}
}
