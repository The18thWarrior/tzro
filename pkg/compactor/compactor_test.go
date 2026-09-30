package compactor

import (
	"strings"
	"testing"
	"tzro/pkg/store"
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

func TestCompactWithArtifact_RetentionAndSSE(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// 1. Stack trace input compaction with artifact retention (> threshold)
	largeLogInput := `Error in worker:
runtime/panic.go:838 +0x207
runtime/proc.go:271 +0x45
runtime/asm_amd64.s:1650 +0x1
main.Process()
	/app/process.go:12
testing.go:1234 +0x56
testing.go:1235 +0x57
testing.go:1236 +0x58
testing.go:1237 +0x59
runtime/panic.go:839 +0x208`

	compacted := CompactWithArtifact(largeLogInput, "/workspace", s)
	if !strings.Contains(compacted, "// [Tzro Artifact: art_") {
		t.Errorf("expected artifact ID header in compacted output, got %s", compacted)
	}
	if !strings.Contains(compacted, "main.Process()") {
		t.Errorf("expected main.Process preserved in compacted output, got %s", compacted)
	}

	// 2. Small input (< threshold): compacted without artifact header to avoid token inflation
	smallLogInput := `Error:
runtime/panic.go:838 +0x207
testing.go:1234 +0x56
main.Run()`
	smallCompacted := CompactWithArtifact(smallLogInput, "/workspace", s)
	if strings.Contains(smallCompacted, "// [Tzro Artifact:") {
		t.Errorf("expected small input to omit artifact header, got %s", smallCompacted)
	}
	if !strings.Contains(smallCompacted, "main.Run()") {
		t.Errorf("expected user code preserved in small compacted output, got %s", smallCompacted)
	}
	if !strings.Contains(smallCompacted, "elided") {
		t.Errorf("expected elision marker in small compacted output, got %s", smallCompacted)
	}

	// 3. Uncompacted input: returns original raw input untouched without artifact or header
	rawInput := "module acme/cache\n\ngo 1.22.0\n"
	uncompacted := CompactWithArtifact(rawInput, "/workspace", s)
	if uncompacted != rawInput {
		t.Errorf("expected uncompacted input returned untouched, got %q", uncompacted)
	}

	// 4. SSE payload must be passed through untouched
	sse := "event: message_delta\ndata: {\"text\":\"hi\"}\n\n"
	passthrough := CompactWithArtifact(sse, "/workspace", s)
	if passthrough != sse {
		t.Errorf("expected SSE payload untouched, got %q", passthrough)
	}
}

func TestGetArtifactThreshold(t *testing.T) {
	if GetArtifactThreshold() != MinArtifactThreshold {
		t.Errorf("expected default threshold %d, got %d", MinArtifactThreshold, GetArtifactThreshold())
	}

	t.Setenv("TZRO_COMPACT_THRESHOLD", "512")
	if GetArtifactThreshold() != 512 {
		t.Errorf("expected threshold 512, got %d", GetArtifactThreshold())
	}
}

func TestStackTraceEliderGoFramePairs(t *testing.T) {
	input := `panic: runtime error: invalid memory address or nil pointer dereference

goroutine 42 [running]:
shop/internal/pricing.(*Calculator).ApplyCoupon(...)
	/app/internal/pricing/coupon.go:87 +0x34
runtime.gopanic(...)
	/usr/local/go/src/runtime/panic.go:860 +0x12c

goroutine 100 [IO wait]:
internal/poll.runtime_pollWait(...)
	/usr/local/go/src/runtime/netpoll.go:351 +0x84
net/http.(*persistConn).readLoop(...)
	/opt/go/src/net/http/transport.go:2205 +0x15c
shop/runtime.Handle(...)
	/work/src/runtime/handle.go:18 +0x44
`
	output := StackTraceElider(input)
	for _, retained := range []string{"nil pointer dereference", "coupon.go:87", "goroutine 100 [IO wait]", "shop/runtime.Handle", "/work/src/runtime/handle.go:18"} {
		if !strings.Contains(output, retained) {
			t.Errorf("lost application or diagnostic evidence: %s", retained)
		}
	}
	for _, removed := range []string{"runtime.gopanic", "/usr/local/go/src/runtime/", "net/http.(*persistConn)"} {
		if strings.Contains(output, removed) {
			t.Errorf("standard Go frame pair retained: %s", removed)
		}
	}
}
