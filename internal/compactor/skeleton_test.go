package compactor

import (
	"strings"
	"testing"
)

func TestExtractSkeleton_GoFile(t *testing.T) {
	code := `package cache

import (
	"context"
	"strings"
)

// PruneColumns removes irrelevant columns from TSV data.
func PruneColumns(ctx context.Context, tsvContent string, stepInstruction string) (string, error) {
	headers := strings.Split(tsvContent, "\t")
	for _, h := range headers {
		if h == "irrelevant" {
			continue
		}
	}
	return tsvContent, nil
}

// Process handles the full compaction pipeline.
func Process(ctx context.Context, payload string, stepInstruction string) (processedPayload string, cacheID string, err error) {
	result := strings.TrimSpace(payload)
	if len(result) > 12000 {
		return result, "cache-123", nil
	}
	return result, "", nil
}

type CacheStore struct {
	db     interface{}
	prefix string
}
`
	skeleton := ExtractSkeleton(code, 0)

	// Must preserve function signatures
	if !strings.Contains(skeleton, "func PruneColumns(ctx context.Context, tsvContent string, stepInstruction string) (string, error)") {
		t.Error("expected PruneColumns signature preserved")
	}
	if !strings.Contains(skeleton, "func Process(ctx context.Context, payload string, stepInstruction string) (processedPayload string, cacheID string, err error)") {
		t.Error("expected Process signature preserved")
	}

	// Must preserve doc comments
	if !strings.Contains(skeleton, "// PruneColumns removes irrelevant columns") {
		t.Error("expected PruneColumns doc comment preserved")
	}
	if !strings.Contains(skeleton, "// Process handles the full compaction pipeline") {
		t.Error("expected Process doc comment preserved")
	}

	// Must preserve type declarations
	if !strings.Contains(skeleton, "type CacheStore struct") {
		t.Error("expected CacheStore type preserved")
	}

	// Must preserve package and imports
	if !strings.Contains(skeleton, "package cache") {
		t.Error("expected package declaration preserved")
	}

	// Must contain body fingerprints
	if !strings.Contains(skeleton, "[body:") {
		t.Error("expected body fingerprint")
	}

	// Must NOT contain implementation details
	if strings.Contains(skeleton, `h == "irrelevant"`) {
		t.Error("expected implementation details stripped")
	}

	// Must be shorter than original
	if len(skeleton) >= len(code) {
		t.Errorf("expected skeleton shorter than original: %d >= %d", len(skeleton), len(code))
	}
}

func TestExtractSkeleton_WithBudget(t *testing.T) {
	code := `package main

// Run executes the main logic.
func Run() {
	doSomething()
	doSomethingElse()
	finalStep()
}

// Helper does stuff.
func Helper(x int) int {
	return x * 2
}
`
	// Very tight budget — should still have signatures
	skeleton := ExtractSkeleton(code, 200)
	if !strings.Contains(skeleton, "func Run()") {
		t.Error("expected Run signature even with tight budget")
	}
	if !strings.Contains(skeleton, "func Helper(x int) int") {
		t.Error("expected Helper signature even with tight budget")
	}
}

func TestExtractSkeleton_EmptyFunction(t *testing.T) {
	code := `package main

func NoBody() {
}
`
	skeleton := ExtractSkeleton(code, 0)
	if !strings.Contains(skeleton, "func NoBody()") {
		t.Error("expected NoBody signature")
	}
}

func TestBodyFingerprint_ExtractsCalls(t *testing.T) {
	body := []string{
		"\theaders := strings.Split(line, \"\\t\")",
		"\tfor _, h := range headers {",
		"\t\tfmt.Println(h)",
		"\t}",
		"\treturn strings.Join(headers, \"\\t\")",
	}

	fp := buildBodyFingerprint(body)
	if !strings.Contains(fp, "body: 5 lines") {
		t.Errorf("expected 5 lines in fingerprint, got: %s", fp)
	}
	if !strings.Contains(fp, "strings.Split()") {
		t.Errorf("expected strings.Split in calls, got: %s", fp)
	}
	if !strings.Contains(fp, "fmt.Println()") {
		t.Errorf("expected fmt.Println in calls, got: %s", fp)
	}
}

func TestBodyFingerprint_EmptyBody(t *testing.T) {
	fp := buildBodyFingerprint(nil)
	if fp != "\t// [empty body]" {
		t.Errorf("expected empty body fingerprint, got: %s", fp)
	}
}

func TestExtractFunctionCalls_SkipsKeywords(t *testing.T) {
	body := []string{
		"\tif err != nil {",
		"\t\treturn err",
		"\t}",
		"\tfor _, item := range items {",
		"\t\tfmt.Println(item)",
		"\t}",
	}

	calls := extractFunctionCalls(body)
	for _, c := range calls {
		if c == "if()" || c == "for()" || c == "range()" || c == "return()" {
			t.Errorf("keyword %s should not be in calls", c)
		}
	}
}
