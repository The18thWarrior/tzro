package inference

import (
	"fmt"
	"strings"
	"testing"
)

// ─── Slice 1: Normal content passes without abort ───────────────────────────

func TestRepetitionGuard_NormalContent_Continues(t *testing.T) {
	guard := NewRepetitionGuard()

	// Simulate streaming chunks of normal Go code
	chunks := []string{
		"package main\n\nimport \"fmt\"\n\n",
		"func main() {\n",
		"\tfmt.Println(\"Hello, World!\")\n",
		"}\n",
	}

	accumulated := ""
	for _, chunk := range chunks {
		accumulated += chunk
		action := guard.OnChunk(accumulated)
		if action != GuardContinue {
			t.Fatalf("expected GuardContinue for normal content, got %v after chunk %q", action, chunk)
		}
	}
}

// ─── Slice 2: Character-level degeneration triggers abort ───────────────────

func TestRepetitionGuard_CharacterDegeneration_Aborts(t *testing.T) {
	guard := NewRepetitionGuard()

	// Start with valid content, then append degenerate backtick-space pairs
	valid := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	degenerate := strings.Repeat("` ", 300) // 600 chars of backtick-space
	full := valid + degenerate + "\n"

	action := guard.OnChunk(full)
	if action != GuardAbort {
		t.Fatal("expected GuardAbort for character-level degeneration")
	}
}

func TestRepetitionGuard_DotFillDegeneration_Aborts(t *testing.T) {
	guard := NewRepetitionGuard()

	valid := "package main\n\nfunc main() {\n"
	degenerate := strings.Repeat(".", 500) + "\n"
	full := valid + degenerate

	action := guard.OnChunk(full)
	if action != GuardAbort {
		t.Fatal("expected GuardAbort for dot-fill degeneration")
	}
}

// ─── Slice 3: Block-level repetition triggers abort ─────────────────────────

func TestRepetitionGuard_BlockRepetition_Aborts(t *testing.T) {
	guard := NewRepetitionGuard()

	// Create a 10-line block and repeat it 3 times (30 contiguous identical lines)
	var block strings.Builder
	for i := 0; i < blockWindowSize; i++ {
		block.WriteString(fmt.Sprintf("func (u *User) Validate%d() error {\n", i))
	}
	repeatedBlock := block.String()

	// Valid prefix + 3x identical block
	content := "package main\n\nimport \"fmt\"\n\n"
	content += strings.Repeat(repeatedBlock, 3)

	action := guard.OnChunk(content)
	if action != GuardAbort {
		t.Fatal("expected GuardAbort for 3x identical 10-line block repetition")
	}
}

// ─── Slice 4: Similar but non-identical blocks do NOT abort ─────────────────

func TestRepetitionGuard_SimilarButDifferentBlocks_Continues(t *testing.T) {
	guard := NewRepetitionGuard()

	// 3 blocks that are structurally similar but have different handler names
	var content strings.Builder
	content.WriteString("package main\n\nimport \"net/http\"\n\n")

	for blockIdx := 0; blockIdx < 3; blockIdx++ {
		for lineIdx := 0; lineIdx < blockWindowSize; lineIdx++ {
			content.WriteString(fmt.Sprintf("func handler%d_line%d(w http.ResponseWriter, r *http.Request) {\n", blockIdx, lineIdx))
		}
	}

	action := guard.OnChunk(content.String())
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for similar-but-different blocks")
	}
}

// ─── Slice 5: Mixed content — valid prefix + degenerate tail ────────────────

func TestRepetitionGuard_MixedContent_AbortsOnDegenerateTail(t *testing.T) {
	guard := NewRepetitionGuard()

	// Simulate streaming: valid content first, then degenerate
	valid := "package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n"
	accumulated := valid

	// First chunk should be fine
	action := guard.OnChunk(accumulated)
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for valid prefix")
	}

	// Now add degenerate content
	accumulated += strings.Repeat("` ", 300) + "\n"
	action = guard.OnChunk(accumulated)
	if action != GuardAbort {
		t.Fatal("expected GuardAbort after degenerate tail was added")
	}
}

// ─── Slice 6: Short strings never flagged ───────────────────────────────────

func TestRepetitionGuard_ShortContent_NeverAborts(t *testing.T) {
	guard := NewRepetitionGuard()

	// Even degenerate-looking content under the minimum threshold should pass
	short := "```\n"
	action := guard.OnChunk(short)
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for short content")
	}
}
