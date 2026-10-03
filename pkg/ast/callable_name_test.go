package ast

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestCallableDeclaratorNames(t *testing.T) {
	body := "{\n /* BODY_SENTINEL retained evidence for exact recovery across name extraction changes */\n return 0;\n}\n"
	empty := "{\n /* BODY_SENTINEL constructor and destructor evidence */\n}\n"
	cases := []struct{ file, source, name string }{
		{"plain.c", "int Target(int value) " + body, "Target"},
		{"pointer.c", "const char *Target(int value) " + body, "Target"},
		{"callback.c", "int Target(int (*callback)(int parameter), int value) " + body, "Target"},
		{"factory.c", "int (*Factory(int value))(int callbackParameter) " + body, "Factory"},
		{"static.c", "static inline int Target(int value) " + body, "Target"},
		{"plain.cpp", "int Target(int value) " + body, "Target"},
		{"pointer.cpp", "const char *Target(int value) " + body, "Target"},
		{"callback.cpp", "int Target(int (*callback)(int parameter), int value) " + body, "Target"},
		{"factory.cpp", "int (*Factory(int value))(int callbackParameter) " + body, "Factory"},
		{"qualified.cpp", "int Widget::Target(int value) " + body, "Widget::Target"},
		{"method.cpp", "class Widget {\n int Target(int value) " + body + "};\n", "Target"},
		{"constructor.cpp", "class Widget {\n Widget() " + empty + "};\n", "Widget"},
		{"destructor.cpp", "class Widget {\n ~Widget() " + empty + "};\n", "~Widget"},
		{"operator.cpp", "class Widget {\n int operator[](int value) " + body + "};\n", "operator[]"},
		{"qualified_operator.cpp", "int Widget::operator+(int value) " + body, "Widget::operator+"},
		{"template.cpp", "template<typename Value>\nint Target(Value value) " + body, "Target"},
		{"sample.go", "package p\nfunc Target(value int) int {\n // BODY_SENTINEL\n return value\n}\n", "Target"},
		{"sample.py", "def Target(value):\n    # BODY_SENTINEL\n    return value\n", "Target"},
		{"sample.ts", "function Target(value:number) {\n // BODY_SENTINEL\n return value;\n}\n", "Target"},
		{"sample.tsx", "function Target(value:number) {\n // BODY_SENTINEL\n return value;\n}\n", "Target"},
		{"sample.js", "function Target(value) {\n // BODY_SENTINEL\n return value;\n}\n", "Target"},
		{"sample.rs", "fn Target(value:i32)->i32 {\n // BODY_SENTINEL\n return value;\n}\n", "Target"},
		{"Sample.java", "class Sample { int Target(int value) {\n // BODY_SENTINEL\n return value;\n}\n}\n", "Target"},
		{"Sample.cs", "class Sample { int Target(int value) {\n // BODY_SENTINEL\n return value;\n}\n}\n", "Target"},
	}
	rows := []map[string]any{}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			s, err := store.OpenStore(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			source := []byte(tc.source)
			result, err := Skeletonize(tc.file, source, s, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			line := strings.Count(tc.source[:strings.Index(tc.source, "BODY_SENTINEL")], "\n") + 1
			span, err := ExtractDeclarationSpan(tc.file, source, line, "", s)
			if err != nil || span == nil {
				t.Fatal("span", err)
			}
			symbols, err := s.SearchSymbols("fixture", tc.name, 100)
			if err != nil {
				t.Fatal(err)
			}
			matches := 0
			indexedHash := ""
			indexedLine := 0
			for _, symbol := range symbols {
				if symbol.Symbol == tc.name {
					matches++
					indexedHash = symbol.Hash
					indexedLine = symbol.Line
				}
			}
			blob, err := s.GetBlob(span.BodyHash)
			recovered := err == nil && blob.Body != "" && strings.Contains(tc.source, blob.Body)
			pass := matches == 1 && span.SymbolName == tc.name && recovered && indexedHash == span.BodyHash
			rows = append(rows, map[string]any{"case": tc.file, "expected_name": tc.name, "span_name": span.SymbolName, "indexed_matches": matches, "indexed_hash": indexedHash, "indexed_line": indexedLine, "body_hash": span.BodyHash, "span_start": span.StartLine, "span_end": span.EndLine, "skeleton": result.SkeletonCode, "exact_recovery": recovered, "pass": pass})
			if !pass {
				t.Errorf("expected=%s span=%s matches=%d recovered=%v hashes=%s/%s", tc.name, span.SymbolName, matches, recovered, indexedHash, span.BodyHash)
			}
			for _, wrong := range []string{"callback", "parameter", "callbackParameter", "value"} {
				syms, err := s.SearchSymbols("fixture", wrong, 100)
				if err != nil {
					t.Fatal(err)
				}
				for _, sym := range syms {
					if sym.Symbol == wrong {
						t.Errorf("parameter misindexed as callable: %s", wrong)
					}
				}
			}
		})
	}
	t.Run("anonymous_lambda_is_not_a_named_function", func(t *testing.T) {
		s, err := store.OpenStore(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		_, err = Skeletonize("lambda.cpp", []byte("auto worker = [](int callbackParameter) { return callbackParameter; };\n"), s, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		symbols, err := s.SearchSymbols("fixture", "", 100)
		if err != nil {
			t.Fatal(err)
		}
		pass := len(symbols) == 0
		rows = append(rows, map[string]any{"case": "lambda.cpp", "pass": pass, "indexed_matches": len(symbols)})
		if !pass {
			t.Fatalf("anonymous lambda indexed as named function: %+v", symbols)
		}
	})
	if path := os.Getenv("TZRO_CALLABLE_MATRIX"); path != "" {
		b, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
