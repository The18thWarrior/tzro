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

// ─── Slice 7: Compression-ratio catches semantic repetition ─────────────────

func TestRepetitionGuard_CompressionRatio_DegenerateCommentLoop_Aborts(t *testing.T) {
	guard := NewRepetitionGuard() // code mode

	// Simulate the self-referential comment loop from create_query_builder:
	// The model writes varied-but-semantically-identical comments debating
	// an implementation choice. Different words, same concept each time.
	var sb strings.Builder
	sb.WriteString("package db\n\nimport \"fmt\"\n\n")
	sb.WriteString("type QueryBuilder struct {\n\tsqlParts []string\n\targs []any\n}\n\n")

	// Generate varied-but-repetitive comment loop (different words, same meaning)
	commentVariants := []string{
		"// But the spec might expect the order to be the order of the keys in the map?\n",
		"// That's not deterministic.\n",
		"// So we must sort.\n",
		"// Let's sort the keys.\n",
		"// We'll sort the keys alphabetically.\n",
	}
	for i := 0; i < 80; i++ {
		sb.WriteString(commentVariants[i%len(commentVariants)])
	}

	action := guard.OnChunk(sb.String())
	if action != GuardAbort {
		t.Fatal("expected GuardAbort for degenerate comment loop (compression-ratio should catch this)")
	}
}

func TestRepetitionGuard_CompressionRatio_MetaCommentary_Aborts(t *testing.T) {
	guard := NewRepetitionGuardWithMode(ContentModeProse)

	// Simulate meta-commentary degeneration from multi_source_synthesis:
	var sb strings.Builder
	sb.WriteString("# Analysis of Local AI Trends\n\n")

	phrases := []string{
		"The synthesis is complete. The final answer is provided below.\n",
		"The response is in the required format. The analysis is complete.\n",
		"The output is ready. The user can proceed. The synthesis is done.\n",
		"The answer is complete. The final answer is ready.\n",
	}
	for i := 0; i < 60; i++ {
		sb.WriteString(phrases[i%len(phrases)])
	}

	action := guard.OnChunk(sb.String())
	if action != GuardAbort {
		t.Fatal("expected GuardAbort for meta-commentary degeneration")
	}
}

func TestRepetitionGuard_CompressionRatio_NormalGoCode_Continues(t *testing.T) {
	guard := NewRepetitionGuard() // code mode

	// Real Go source code — varied function bodies, imports, types.
	// Should NOT trigger compression-ratio detection.
	var sb strings.Builder
	sb.WriteString("package server\n\nimport (\n\t\"context\"\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"net/http\"\n\t\"time\"\n)\n\n")
	sb.WriteString("type Server struct {\n\taddr string\n\trouter *http.ServeMux\n\tlogger Logger\n}\n\n")
	sb.WriteString("func NewServer(addr string, logger Logger) *Server {\n\treturn &Server{\n\t\taddr: addr,\n\t\trouter: http.NewServeMux(),\n\t\tlogger: logger,\n\t}\n}\n\n")
	sb.WriteString("func (s *Server) HandleHealth(w http.ResponseWriter, r *http.Request) {\n\tw.Header().Set(\"Content-Type\", \"application/json\")\n\tw.WriteHeader(http.StatusOK)\n\tjson.NewEncoder(w).Encode(map[string]string{\"status\": \"ok\"})\n}\n\n")
	sb.WriteString("func (s *Server) HandleMetrics(w http.ResponseWriter, r *http.Request) {\n\tctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)\n\tdefer cancel()\n\tmetrics, err := s.collectMetrics(ctx)\n\tif err != nil {\n\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)\n\t\treturn\n\t}\n\tjson.NewEncoder(w).Encode(metrics)\n}\n\n")
	sb.WriteString("func (s *Server) collectMetrics(ctx context.Context) (map[string]interface{}, error) {\n\tresult := make(map[string]interface{})\n\tresult[\"uptime\"] = time.Since(s.startTime).String()\n\tresult[\"requests\"] = s.requestCount.Load()\n\tresult[\"errors\"] = s.errorCount.Load()\n\treturn result, nil\n}\n\n")
	sb.WriteString("func (s *Server) Start() error {\n\ts.router.HandleFunc(\"/health\", s.HandleHealth)\n\ts.router.HandleFunc(\"/metrics\", s.HandleMetrics)\n\ts.logger.Info(fmt.Sprintf(\"server starting on %s\", s.addr))\n\treturn http.ListenAndServe(s.addr, s.router)\n}\n")

	action := guard.OnChunk(sb.String())
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for normal Go source code")
	}
}

func TestRepetitionGuard_CompressionRatio_StructuredCodeWithRepeatedFields_Continues(t *testing.T) {
	guard := NewRepetitionGuard() // code mode

	// This pattern caused false positive aborts in benchmark Run 14:
	// TypeScript interfaces and Go structs with repeated field types.
	// The structural repetition (string/int types, field patterns) causes
	// higher compression, but this is legitimate code — NOT degeneration.
	var sb strings.Builder
	sb.WriteString("package models\n\n")
	sb.WriteString("type UserProfile struct {\n")
	sb.WriteString("\tID          string `json:\"id\"`\n")
	sb.WriteString("\tFirstName   string `json:\"firstName\"`\n")
	sb.WriteString("\tLastName    string `json:\"lastName\"`\n")
	sb.WriteString("\tEmail       string `json:\"email\"`\n")
	sb.WriteString("\tPhone       string `json:\"phone\"`\n")
	sb.WriteString("\tAddress     string `json:\"address\"`\n")
	sb.WriteString("\tCity        string `json:\"city\"`\n")
	sb.WriteString("\tState       string `json:\"state\"`\n")
	sb.WriteString("\tZip         string `json:\"zip\"`\n")
	sb.WriteString("\tCountry     string `json:\"country\"`\n")
	sb.WriteString("\tCompany     string `json:\"company\"`\n")
	sb.WriteString("\tTitle       string `json:\"title\"`\n")
	sb.WriteString("\tDepartment  string `json:\"department\"`\n")
	sb.WriteString("\tBio         string `json:\"bio\"`\n")
	sb.WriteString("\tAvatarURL   string `json:\"avatarUrl\"`\n")
	sb.WriteString("\tCreatedAt   string `json:\"createdAt\"`\n")
	sb.WriteString("\tUpdatedAt   string `json:\"updatedAt\"`\n")
	sb.WriteString("}\n\n")
	sb.WriteString("type ConfigParser struct {\n")
	sb.WriteString("\tFilePath    string `json:\"filePath\"`\n")
	sb.WriteString("\tFormat      string `json:\"format\"`\n")
	sb.WriteString("\tEncoding    string `json:\"encoding\"`\n")
	sb.WriteString("\tDelimiter   string `json:\"delimiter\"`\n")
	sb.WriteString("\tCommentChar string `json:\"commentChar\"`\n")
	sb.WriteString("\tMaxSize     int    `json:\"maxSize\"`\n")
	sb.WriteString("\tTimeout     int    `json:\"timeout\"`\n")
	sb.WriteString("\tRetries     int    `json:\"retries\"`\n")
	sb.WriteString("\tStrict      bool   `json:\"strict\"`\n")
	sb.WriteString("\tVerbose     bool   `json:\"verbose\"`\n")
	sb.WriteString("}\n\n")
	sb.WriteString("type QueryBuilder struct {\n")
	sb.WriteString("\tTable      string   `json:\"table\"`\n")
	sb.WriteString("\tColumns    []string `json:\"columns\"`\n")
	sb.WriteString("\tWhere      string   `json:\"where\"`\n")
	sb.WriteString("\tOrderBy    string   `json:\"orderBy\"`\n")
	sb.WriteString("\tGroupBy    string   `json:\"groupBy\"`\n")
	sb.WriteString("\tHaving     string   `json:\"having\"`\n")
	sb.WriteString("\tLimit      int      `json:\"limit\"`\n")
	sb.WriteString("\tOffset     int      `json:\"offset\"`\n")
	sb.WriteString("\tDistinct   bool     `json:\"distinct\"`\n")
	sb.WriteString("}\n")

	action := guard.OnChunk(sb.String())
	if action != GuardContinue {
		ratio := compressionRatio(sb.String())
		t.Fatalf("expected GuardContinue for structured code with repeated field patterns (ratio=%.4f), got GuardAbort", ratio)
	}
}

func TestRepetitionGuard_CompressionRatio_NormalProse_Continues(t *testing.T) {
	guard := NewRepetitionGuardWithMode(ContentModeProse)

	// Valid research synthesis — varied content, real information.
	var sb strings.Builder
	sb.WriteString("# Market Analysis: Local AI Infrastructure\n\n")
	sb.WriteString("## Executive Summary\n\n")
	sb.WriteString("The local AI infrastructure market is experiencing rapid growth driven by privacy requirements and cost optimization. Organizations are increasingly deploying on-device inference to reduce cloud API costs while maintaining data sovereignty.\n\n")
	sb.WriteString("## Key Findings\n\n")
	sb.WriteString("1. **Cost Arbitrage**: Running inference locally on consumer hardware (Apple Silicon, NVIDIA RTX) can reduce per-token costs by 95% compared to cloud APIs for suitable workloads.\n\n")
	sb.WriteString("2. **Model Quantization**: GGUF format with Q4_K_M quantization provides the best balance of model quality and memory efficiency. The quality degradation from float16 to Q4 is measurable but acceptable for most agentic tasks.\n\n")
	sb.WriteString("3. **Speculative Decoding**: Using a smaller draft model to accelerate generation has become standard practice. The technique provides 2-3x throughput improvement when draft acceptance rates exceed 60%.\n\n")
	sb.WriteString("4. **Privacy Compliance**: GDPR and CCPA requirements are driving enterprise adoption of local inference for PII-containing workloads. The regulatory landscape favors on-premise processing.\n\n")
	sb.WriteString("## Competitive Landscape\n\n")
	sb.WriteString("Major players include Ollama (simplicity-focused), llama.cpp (performance-focused), and vLLM (production-focused). Each addresses different deployment scenarios with varying trade-offs between ease of use and configurability.\n\n")

	action := guard.OnChunk(sb.String())
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for normal research prose")
	}
}

func TestRepetitionGuard_CompressionRatio_ShortContent_NeverTriggers(t *testing.T) {
	guard := NewRepetitionGuard()

	// Even highly repetitive content below compressionWindowSize should not trigger
	short := strings.Repeat("hello\n", 20)
	action := guard.OnChunk(short)
	if action != GuardContinue {
		t.Fatal("expected GuardContinue for content below compression window size")
	}
}

// ─── Slice 8: compressionRatio function unit tests ──────────────────────────

func TestCompressionRatio_HighlyRepetitive(t *testing.T) {
	repetitive := strings.Repeat("The answer is complete. The synthesis is done. ", 50)
	ratio := compressionRatio(repetitive)
	if ratio >= 0.20 {
		t.Errorf("expected highly repetitive text to have ratio < 0.20, got %.4f", ratio)
	}
}

func TestCompressionRatio_NormalText(t *testing.T) {
	normal := "The quick brown fox jumps over the lazy dog. Pack my box with five dozen liquor jugs. How vexingly quick daft zebras jump. The five boxing wizards jump quickly."
	ratio := compressionRatio(normal)
	if ratio < 0.40 {
		t.Errorf("expected normal text to have ratio >= 0.40, got %.4f", ratio)
	}
}

func TestCompressionRatio_EmptyString(t *testing.T) {
	ratio := compressionRatio("")
	if ratio != 1.0 {
		t.Errorf("expected empty string ratio = 1.0, got %.4f", ratio)
	}
}

// --- Slice 3a RED (Run 32 fix): ContentModeCode calibration ---

// TestRepetitionGuard_CodeMode_IdiomaticGoErrors_NeverAborts verifies that
// a realistic Go file with 6 HTTP handlers each containing the canonical
// "if err != nil { http.Error(...) }" pattern does NOT trigger GuardAbort.
// Regression: ContentModeCode flate threshold 0.35 caught valid Go (ratio ~0.30).
func TestRepetitionGuard_CodeMode_IdiomaticGoErrors_NeverAborts(t *testing.T) {
	guard := NewRepetitionGuardWithMode(ContentModeCode)

	var b strings.Builder
	b.WriteString("package main\n\nimport \"net/http\"\n\n")
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&b, `func handler%d(w http.ResponseWriter, r *http.Request) {
	data, err := fetchData%d()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

`, i, i)
	}

	action := guard.OnChunk(b.String())
	if action == GuardAbort {
		ratio := compressionRatio(b.String())
		t.Errorf("GuardAbort must not fire for idiomatic Go error handling (compression ratio=%.4f); "+
			"valid Go compresses to 0.28-0.40, degenerate to <0.20", ratio)
	}
}

// TestRepetitionGuard_CodeMode_DegenerateLoop_Aborts verifies that a genuine
// degenerate pattern (deeply nested empty loops) still triggers GuardAbort
// after the threshold recalibration.
func TestRepetitionGuard_CodeMode_DegenerateLoop_Aborts(t *testing.T) {
	guard := NewRepetitionGuardWithMode(ContentModeCode)
	// Deeply nested empty for loops compress extremely well (ratio ~0.05-0.10)
	degenerate := "package main\n\nfunc main() {\n" + strings.Repeat("\tfor {\n", 200) + strings.Repeat("\t}\n", 200) + "}\n"
	action := guard.OnChunk(degenerate)
	if action != GuardAbort {
		ratio := compressionRatio(degenerate)
		t.Errorf("expected GuardAbort for degenerate nested loop (compression ratio=%.4f); "+
			"should be well below 0.20 threshold", ratio)
	}
}
