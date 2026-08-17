package compactor

import (
	"math"
	"strings"
	"unicode"
)

// BM25Scorer computes BM25 relevance scores for a collection of text chunks against a query.
type BM25Scorer struct {
	chunks     []string
	docTokens  [][]string
	docLengths []int
	avgDocLen  float64
	docFreqs   map[string]int // term -> count of docs containing term
	k1         float64
	b          float64
}

// NewBM25Scorer initializes a BM25 scorer with k1=1.5 and b=0.75.
func NewBM25Scorer(chunks []string) *BM25Scorer {
	s := &BM25Scorer{
		chunks:     chunks,
		docTokens:  make([][]string, len(chunks)),
		docLengths: make([]int, len(chunks)),
		docFreqs:   make(map[string]int),
		k1:         1.5,
		b:          0.75,
	}

	totalLen := 0
	for i, chunk := range chunks {
		tokens := tokenizeText(chunk)
		s.docTokens[i] = tokens
		s.docLengths[i] = len(tokens)
		totalLen += len(tokens)

		seen := make(map[string]bool)
		for _, tok := range tokens {
			if !seen[tok] {
				seen[tok] = true
				s.docFreqs[tok]++
			}
		}
	}

	if len(chunks) > 0 {
		s.avgDocLen = float64(totalLen) / float64(len(chunks))
	}

	return s
}

// Score returns BM25 scores for all chunks against the query.
func (s *BM25Scorer) Score(query string) []float64 {
	scores := make([]float64, len(s.chunks))
	if len(s.chunks) == 0 {
		return scores
	}

	queryTokens := tokenizeText(query)
	if len(queryTokens) == 0 {
		return scores
	}

	nDocs := float64(len(s.chunks))

	for _, qTerm := range queryTokens {
		df := float64(s.docFreqs[qTerm])
		if df == 0 {
			continue
		}

		// Lucene/Standard BM25 IDF: ln(1 + (N - n + 0.5) / (n + 0.5))
		idf := math.Log(1.0 + (nDocs-df+0.5)/(df+0.5))
		if idf < 0 {
			idf = 0
		}

		for i, tokens := range s.docTokens {
			tf := 0.0
			for _, tok := range tokens {
				if tok == qTerm {
					tf++
				}
			}
			if tf == 0 {
				continue
			}

			docLen := float64(s.docLengths[i])
			denom := tf + s.k1*(1.0-s.b+s.b*(docLen/s.avgDocLen))
			if denom > 0 {
				scores[i] += idf * (tf * (s.k1 + 1.0) / denom)
			}
		}
	}

	return scores
}

func tokenizeText(s string) []string {
	var sb strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			sb.WriteRune(unicode.ToLower(r))
		} else {
			sb.WriteRune(' ')
		}
	}
	raw := strings.Fields(sb.String())
	var tokens []string
	for _, w := range raw {
		if !stopWordsMap[w] && len(w) > 1 {
			tokens = append(tokens, w)
		}
	}
	return tokens
}

var stopWordsMap = map[string]bool{
	"a": true, "about": true, "above": true, "after": true, "again": true, "against": true,
	"all": true, "am": true, "an": true, "and": true, "any": true, "are": true, "as": true,
	"at": true, "be": true, "because": true, "been": true, "before": true, "being": true,
	"below": true, "between": true, "both": true, "but": true, "by": true, "could": true,
	"did": true, "do": true, "does": true, "doing": true, "down": true, "during": true,
	"each": true, "few": true, "for": true, "from": true, "further": true, "had": true,
	"has": true, "have": true, "having": true, "he": true, "her": true, "here": true,
	"hers": true, "herself": true, "him": true, "himself": true, "his": true, "how": true,
	"i": true, "if": true, "in": true, "into": true, "is": true, "it": true, "its": true,
	"itself": true, "just": true, "me": true, "more": true, "most": true, "my": true,
	"myself": true, "no": true, "nor": true, "not": true, "now": true, "of": true,
	"off": true, "on": true, "once": true, "only": true, "or": true, "other": true,
	"ought": true, "our": true, "ours": true, "ourselves": true, "out": true, "over": true,
	"own": true, "same": true, "she": true, "should": true, "so": true, "some": true,
	"such": true, "than": true, "that": true, "the": true, "their": true, "theirs": true,
	"them": true, "themselves": true, "then": true, "there": true, "these": true,
	"they": true, "this": true, "those": true, "through": true, "to": true, "too": true,
	"under": true, "until": true, "up": true, "very": true, "was": true, "we": true,
	"were": true, "what": true, "when": true, "where": true, "which": true, "while": true,
	"who": true, "whom": true, "why": true, "with": true, "would": true, "you": true,
	"your": true, "yours": true, "yourself": true, "yourselves": true,
}
