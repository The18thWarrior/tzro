package tokenizer_test

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"

	tzrocontext "tzro/pkg/context"
	"tzro/pkg/store"
	"tzro/pkg/tokenizer"
)

// Fixture defines a test case with pinned expected counts.
type Fixture struct {
	Name             string
	Text             string
	ExpectedCl100k   int
	ExpectedO200k    int
	VerifySpecialLit bool
}

var pinnedFixtures = []Fixture{
	{
		Name:           "Go function snippet",
		Text:           `package main; import "fmt"; func main() { fmt.Println("Hello, world!") }`,
		ExpectedCl100k: 19,
		ExpectedO200k:  19,
	},
	{
		Name:           "TypeScript snippet",
		Text:           "interface User { id: string; name: string; }\nconst greet = (u: User): string => `Hello, ${u.name}`;\n",
		ExpectedCl100k: 29,
		ExpectedO200k:  29,
	},
	{
		Name:           "Python function snippet",
		Text:           "def calculate_metrics(items: list[int]) -> dict[str, float]:\n    return {\"mean\": sum(items) / len(items)}\n",
		ExpectedCl100k: 26,
		ExpectedO200k:  26,
	},
	{
		Name:           "Rust function snippet",
		Text:           "pub fn compute_digest<T: AsRef<[u8]>>(data: T) -> Result<[u8; 32], CryptoError> {\n    let mut hasher = Sha256::new();\n    hasher.update(data.as_ref());\n    Ok(hasher.finalize().into())\n}\n",
		ExpectedCl100k: 57,
		ExpectedO200k:  59,
	},
	{
		Name:           "Multilingual Unicode (Japanese, Russian, Arabic, Chinese)",
		Text:           "こんにちは世界 Привет мир مرحبا بالعالم 你好世界",
		ExpectedCl100k: 25,
		ExpectedO200k:  12,
	},
	{
		Name:           "Emoji and zero-width joiner sequences",
		Text:           "👋🌍🚀✨🔥 👨‍👩‍👧‍👦",
		ExpectedCl100k: 31,
		ExpectedO200k:  19,
	},
	{
		Name:           "Whitespace and indentation varieties",
		Text:           "    \t\t\n\r\n        \n\t\n",
		ExpectedCl100k: 4,
		ExpectedO200k:  4,
	},
	{
		Name:           "Long single-token identifiers",
		Text:           "veryLongIdentifierNameWithCamelCaseAndMoreWords1234567890",
		ExpectedCl100k: 15,
		ExpectedO200k:  14,
	},
	{
		Name:             "Special token literal in source text",
		Text:             "let marker = \"<|endoftext|>\"; // should not trigger special token encoding error",
		ExpectedCl100k:   18,
		ExpectedO200k:    18,
		VerifySpecialLit: true,
	},
	{
		Name:             "FIM and ChatML special token literals",
		Text:             "<|im_start|>system\nYou are a helpful assistant.<|im_end|>\n<|fim_prefix|>def foo():<|fim_suffix|>",
		ExpectedCl100k:   36,
		ExpectedO200k:    34,
		VerifySpecialLit: true,
	},
}

func TestTokenizer_PinnedFixtures(t *testing.T) {
	for _, fix := range pinnedFixtures {
		t.Run(fix.Name+"/cl100k_base", func(t *testing.T) {
			count, err := tokenizer.Count(fix.Text, tokenizer.EncodingCl100kBase)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", fix.Name, err)
			}
			if count != fix.ExpectedCl100k {
				t.Errorf("cl100k_base count mismatch for %q: got %d, expected %d", fix.Name, count, fix.ExpectedCl100k)
			}

			// Verify CountDefault agrees with cl100k_base
			defCount := tokenizer.CountDefault(fix.Text)
			if defCount != count {
				t.Errorf("CountDefault mismatch for %q: got %d, expected %d", fix.Name, defCount, count)
			}
		})

		t.Run(fix.Name+"/o200k_base", func(t *testing.T) {
			count, err := tokenizer.Count(fix.Text, tokenizer.EncodingO200kBase)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", fix.Name, err)
			}
			if count != fix.ExpectedO200k {
				t.Errorf("o200k_base count mismatch for %q: got %d, expected %d", fix.Name, count, fix.ExpectedO200k)
			}
		})
	}
}

func TestTokenizer_UnknownEncoding(t *testing.T) {
	unknowns := []string{"gpt2", "llama", "claude_v3", "p50k_base", "custom_enc"}
	for _, enc := range unknowns {
		_, err := tokenizer.Count("hello", enc)
		if err == nil {
			t.Errorf("expected error for unknown encoding %q, got nil", enc)
		}
		if !strings.Contains(err.Error(), "unknown tokenizer encoding") {
			t.Errorf("expected ErrUnknownEncoding message for %q, got %v", enc, err)
		}
	}
}

func TestTokenizer_TruncateToBudget(t *testing.T) {
	longText := strings.Repeat("The quick brown fox jumps over the lazy dog. 🦊🚀\n", 50)
	totalTokens, err := tokenizer.Count(longText, tokenizer.EncodingCl100kBase)
	if err != nil {
		t.Fatalf("Count failed: %v", err)
	}

	targets := []int{0, 1, 5, 10, 25, 50, 100, totalTokens, totalTokens + 10}
	for _, target := range targets {
		t.Run(fmt.Sprintf("target_%d", target), func(t *testing.T) {
			truncated, count, err := tokenizer.TruncateToBudget(longText, tokenizer.EncodingCl100kBase, target)
			if err != nil {
				t.Fatalf("TruncateToBudget failed: %v", err)
			}

			// 1. Must be valid UTF-8
			if !utf8.ValidString(truncated) {
				t.Errorf("truncated text is not valid UTF-8")
			}

			// 2. Count must not exceed requested target
			if count > target && target > 0 {
				t.Errorf("token count %d exceeds target %d", count, target)
			}
			if target == 0 && count != 0 {
				t.Errorf("expected 0 tokens for target 0, got %d", count)
			}

			// 3. If target >= totalTokens, nothing truncated
			if target >= totalTokens && truncated != longText {
				t.Errorf("expected full text retained when target >= totalTokens")
			}
		})
	}
}

func TestTokenizer_TruncateToBudget_MultibyteSplits(t *testing.T) {
	// Japanese characters and emoji (each rune is 3-4 bytes)
	text := "あいうえおかきくけこさしすせそたちつてとなにぬねのはひふへほまみむめも🤖👾🧙‍♀️"
	for budget := 1; budget <= 20; budget++ {
		truncated, count, err := tokenizer.TruncateToBudget(text, tokenizer.EncodingCl100kBase, budget)
		if err != nil {
			t.Fatalf("budget %d failed: %v", budget, err)
		}
		if !utf8.ValidString(truncated) {
			t.Errorf("budget %d produced invalid UTF-8 string: %q", budget, truncated)
		}
		if count > budget {
			t.Errorf("budget %d exceeded: got %d", budget, count)
		}
	}
}

func TestTokenizer_ContextEnvelopeBudgetEnforcement(t *testing.T) {
	tempDir := t.TempDir()

	// Write 5 files with distinct functions
	for i := 1; i <= 5; i++ {
		content := fmt.Sprintf("package pkg%d\n\nfunc Worker%d() {\n\t// compute\n\t_ = %d * 42\n}\n", i, i, i)
		_ = os.WriteFile(filepath.Join(tempDir, fmt.Sprintf("worker_%d.go", i)), []byte(content), 0644)
	}

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := tzrocontext.NewAssembler(s, nil)
	pack, err := assembler.Assemble(tempDir, "Worker", 2000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if pack.Tokenizer == nil {
		t.Fatal("expected Tokenizer metadata on ContextPack")
	}
	if pack.Tokenizer.Encoding != tokenizer.EncodingDefault {
		t.Errorf("expected encoding %s, got %s", tokenizer.EncodingDefault, pack.Tokenizer.Encoding)
	}
	if pack.Tokenizer.Mode != tokenizer.ModeExact {
		t.Errorf("expected mode exact, got %s", pack.Tokenizer.Mode)
	}
	if pack.Tokenizer.ContentTokens != pack.UsedTokens {
		t.Errorf("mismatch: ContentTokens %d != UsedTokens %d", pack.Tokenizer.ContentTokens, pack.UsedTokens)
	}

	// Enforce strict envelope budget
	envelopeBudget := 250
	err = pack.EnforceEnvelopeBudget(envelopeBudget)
	if err != nil {
		t.Fatalf("EnforceEnvelopeBudget failed: %v", err)
	}

	markdown := pack.FormatMarkdown()
	actualSerializedTokens := tokenizer.CountDefault(markdown)
	if actualSerializedTokens > envelopeBudget {
		t.Errorf("serialized markdown exceeds envelope budget: %d > %d", actualSerializedTokens, envelopeBudget)
	}

	// Tiny budget that cannot fit minimum envelope should error
	err = pack.EnforceEnvelopeBudget(5)
	if err == nil {
		t.Error("expected error when budget cannot fit minimum envelope, got nil")
	}
}

func TestTokenizer_LargeOmissionListBounding(t *testing.T) {
	pack := &tzrocontext.ContextPack{
		Query:      "test",
		Budget:     1000,
		UsedTokens: 100,
		Coverage: &tzrocontext.CoverageReport{
			TotalCandidates:    500,
			IncludedCandidates: 5,
			TruncatedCount:     495,
			TruncatedManifest:  make([]string, 495),
		},
	}
	for i := 0; i < 495; i++ {
		pack.Coverage.TruncatedManifest[i] = fmt.Sprintf("path/to/very/long/nested/directory/structure/file_%04d.go", i)
	}

	markdown := pack.FormatMarkdown()
	// Bounding ensures we don't dump 495 file paths into the markdown envelope
	if !strings.Contains(markdown, "(and 485 more)") {
		t.Errorf("expected omission list to be bounded with summary, got:\n%s", markdown)
	}
}

// BenchmarkTokenizer_WarmCounting10KB measures warm counting for a ~10 KB source file.
// Target: less than 1ms warm p95.
func BenchmarkTokenizer_WarmCounting10KB(b *testing.B) {
	// Construct 10 KB of realistic Go code
	var sb strings.Builder
	sb.WriteString("package server\n\nimport (\n\t\"context\"\n\t\"fmt\"\n\t\"sync\"\n)\n\n")
	for i := 0; i < 120; i++ {
		sb.WriteString(fmt.Sprintf("func ProcessMessage%d(ctx context.Context, id string, payload []byte) (bool, error) {\n", i))
		sb.WriteString("\tif len(payload) == 0 { return false, fmt.Errorf(\"empty payload: %s\", id) }\n")
		sb.WriteString("\treturn true, nil\n}\n\n")
	}
	code := sb.String()
	if len(code) < 10000 {
		b.Fatalf("fixture size %d is less than 10KB", len(code))
	}

	// Warm up
	_, _ = tokenizer.Count(code, tokenizer.EncodingCl100kBase)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		count, err := tokenizer.Count(code, tokenizer.EncodingCl100kBase)
		if err != nil || count == 0 {
			b.Fatalf("Count error: %v", err)
		}
	}
}

func BenchmarkTokenizer_O200kCounting10KB(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("package server\n\n")
	for i := 0; i < 120; i++ {
		sb.WriteString(fmt.Sprintf("func ProcessMessage%d(id string) error { return nil }\n", i))
	}
	code := sb.String()

	_, _ = tokenizer.Count(code, tokenizer.EncodingO200kBase)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = tokenizer.Count(code, tokenizer.EncodingO200kBase)
	}
}

func BenchmarkTokenizer_ProcessMemoryTarget(b *testing.B) {
	var m1, m2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m1)

	// Perform multiple counts across codecs
	for i := 0; i < 100; i++ {
		_, _ = tokenizer.Count("hello world", tokenizer.EncodingCl100kBase)
		_, _ = tokenizer.Count("hello world", tokenizer.EncodingO200kBase)
	}

	runtime.ReadMemStats(&m2)
	heapAllocMB := float64(m2.HeapAlloc) / (1024 * 1024)
	if heapAllocMB > 50.0 {
		b.Errorf("HeapAlloc %.2f MB exceeds 50 MB target", heapAllocMB)
	}
}
