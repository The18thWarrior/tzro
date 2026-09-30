package ast

import (
	"strings"
	"testing"

	"tzro/pkg/store"
)

func TestSkeletonRetainsBodyWhenElisionCostsMore(t *testing.T) {
	for _, tc := range []struct{ path, source, symbol string }{
		{"small.go", "package sample\nfunc Ready() bool { return true }\n", "Ready"},
		{"small.py", "def ready():\n    return True\n", "ready"},
		{"small.ts", "function ready() { return true; }\n", "ready"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			s, err := store.OpenStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			result, err := Skeletonize(tc.path, []byte(tc.source), s, "workspace")
			if err != nil {
				t.Fatal(err)
			}
			if result.SkeletonCode != tc.source || result.ElidedBlocks != 0 || len(result.Hashes) != 0 {
				t.Fatalf("small source should stay exact instead of becoming a larger marker: %q", result.SkeletonCode)
			}
			syms, err := s.SearchSymbols("workspace", tc.symbol, 10)
			if err != nil || len(syms) != 1 {
				t.Fatalf("retained declaration must remain discoverable: %+v, %v", syms, err)
			}
			blob, err := s.GetBlob(syms[0].Hash)
			if err != nil || !strings.Contains(blob.Body, "return") {
				t.Fatalf("body recovery: %+v, %v", blob, err)
			}
		})
	}
}

func TestDeclarationSpanRetainsBodyWhenElisionCostsMore(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	source := []byte("package sample\nfunc Ready() bool { return true }\n")
	span, err := ExtractDeclarationSpan("small.go", source, 2, "Ready", s)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(span.Code, "return true") || strings.Contains(span.Code, "[body elided:") {
		t.Fatalf("span should expose the smaller original body, got %q", span.Code)
	}
	if span.TokenWeight > EstimateTokens(string(source)) {
		t.Fatal("small declaration grew during compaction")
	}
	body, err := s.GetBlob(span.BodyHash)
	if err != nil || body.Body != "{ return true }" {
		t.Fatalf("retained body lost recovery identity: %+v, %v", body, err)
	}
}
