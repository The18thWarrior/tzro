package embeddings

import (
	"context"
	"math"
	"strings"
	"unicode"
)

type EmbeddingEngine interface {
	Embed(ctx context.Context, text string) ([]float32, error)
	CosineSimilarity(v1, v2 []float32) float32
}

type PureGoEmbeddingEngine struct {
	vocabulary map[string]int
	vocabList  []string
}

func NewPureGoEmbeddingEngine() *PureGoEmbeddingEngine {
	// A simple predefined vocabulary of common domain terms to build standard-dimensional vectors.
	// This helps represent semantic coordinates consistently.
	vocab := []string{
		"hubspot", "crm", "contact", "sync", "pipeline",
		"docker", "container", "mcp", "daemon", "aws", "cluster",
		"sqlite", "database", "query", "memory", "graph", "rag",
		"workflow", "task", "kahn", "compiler", "telemetry",
		"salesforce", "jira", "slack", "spreadsheet", "lead",
		"deduplication", "compaction", "cache", "notification",
	}
	vMap := make(map[string]int)
	for i, w := range vocab {
		vMap[w] = i
	}
	return &PureGoEmbeddingEngine{
		vocabulary: vMap,
		vocabList:  vocab,
	}
}

func (e *PureGoEmbeddingEngine) Embed(ctx context.Context, text string) ([]float32, error) {
	// Simple bag-of-words / TF vectorizer matching our standard dimensions
	words := tokenize(text)
	vec := make([]float32, len(e.vocabList))
	for w, count := range words {
		if idx, exists := e.vocabulary[w]; exists {
			vec[idx] = float32(count)
		}
	}
	return vec, nil
}

func (e *PureGoEmbeddingEngine) CosineSimilarity(v1, v2 []float32) float32 {
	if len(v1) != len(v2) || len(v1) == 0 {
		return 0.0
	}
	var dot, norm1, norm2 float32
	for i := range v1 {
		dot += v1[i] * v2[i]
		norm1 += v1[i] * v1[i]
		norm2 += v2[i] * v2[i]
	}
	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}
	return dot / (float32(math.Sqrt(float64(norm1))) * float32(math.Sqrt(float64(norm2))))
}

func tokenize(s string) map[string]int {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(' ')
		}
	}
	words := strings.Fields(sb.String())
	vec := make(map[string]int)
	for _, w := range words {
		vec[w]++
	}
	return vec
}

// stopWords lists common English stop words and tzro boilerplate keywords to prevent similarity inflation.
var stopWords = map[string]bool{
	"a":          true,
	"about":      true,
	"above":      true,
	"after":      true,
	"again":      true,
	"against":    true,
	"all":        true,
	"am":         true,
	"an":         true,
	"and":        true,
	"any":        true,
	"are":        true,
	"as":         true,
	"at":         true,
	"be":         true,
	"because":    true,
	"been":       true,
	"before":     true,
	"being":      true,
	"below":      true,
	"between":    true,
	"both":       true,
	"but":        true,
	"by":         true,
	"can":        true,
	"did":        true,
	"do":         true,
	"does":       true,
	"doing":      true,
	"don":        true,
	"down":       true,
	"during":     true,
	"each":       true,
	"few":        true,
	"for":        true,
	"from":       true,
	"further":    true,
	"had":        true,
	"has":        true,
	"have":       true,
	"having":     true,
	"he":         true,
	"her":        true,
	"here":       true,
	"hers":       true,
	"herself":    true,
	"him":        true,
	"himself":    true,
	"his":        true,
	"how":        true,
	"i":          true,
	"if":         true,
	"in":         true,
	"into":       true,
	"is":         true,
	"it":         true,
	"its":        true,
	"itself":     true,
	"just":       true,
	"me":         true,
	"more":       true,
	"most":       true,
	"my":         true,
	"myself":     true,
	"no":         true,
	"nor":        true,
	"not":        true,
	"of":         true,
	"off":        true,
	"on":         true,
	"once":       true,
	"only":       true,
	"or":         true,
	"other":      true,
	"our":        true,
	"ours":       true,
	"ourselves":  true,
	"out":        true,
	"over":       true,
	"own":        true,
	"s":          true,
	"same":       true,
	"she":        true,
	"should":     true,
	"so":         true,
	"some":       true,
	"such":       true,
	"t":          true,
	"than":       true,
	"that":       true,
	"the":        true,
	"their":      true,
	"theirs":     true,
	"them":       true,
	"themselves": true,
	"then":       true,
	"there":      true,
	"these":      true,
	"they":       true,
	"this":       true,
	"those":      true,
	"through":    true,
	"to":         true,
	"too":        true,
	"under":      true,
	"until":      true,
	"up":         true,
	"very":       true,
	"was":        true,
	"we":         true,
	"were":       true,
	"what":       true,
	"when":       true,
	"where":      true,
	"which":      true,
	"while":      true,
	"who":        true,
	"whom":       true,
	"why":        true,
	"will":       true,
	"with":       true,
	"you":        true,
	"your":       true,
	"yours":      true,
	"yourself":   true,
	"yourselves": true,

	// tzro-specific boilerplate keywords to prevent artificial similarity inflation
	"submitting": true,
	"requests":   true,
	"related":    true,
}

// tokenizeDynamic extracts lowercase terms from a string, skipping stop words and stripping punctuation.
func tokenizeDynamic(s string) map[string]float64 {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(' ')
		}
	}
	words := strings.Fields(sb.String())
	vec := make(map[string]float64)
	for _, w := range words {
		if stopWords[w] {
			continue
		}
		vec[w]++
	}
	return vec
}

// CosineSimilarity computes the dynamic cosine similarity coefficient [0.0, 1.0] between two strings.
func CosineSimilarity(s1, s2 string) float64 {
	v1 := tokenizeDynamic(s1)
	v2 := tokenizeDynamic(s2)

	if len(v1) == 0 || len(v2) == 0 {
		return 0.0
	}

	var dot float64
	for k, val1 := range v1 {
		if val2, exists := v2[k]; exists {
			dot += val1 * val2
		}
	}

	var norm1 float64
	for _, val1 := range v1 {
		norm1 += val1 * val1
	}

	var norm2 float64
	for _, val2 := range v2 {
		norm2 += val2 * val2
	}

	if norm1 == 0 || norm2 == 0 {
		return 0.0
	}

	return dot / (math.Sqrt(norm1) * math.Sqrt(norm2))
}

