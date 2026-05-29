package embeddings

import (
	"context"
	"math"
	"testing"
)

func TestPureGoVectorizer_CosineSimilarity(t *testing.T) {
	engine := NewPureGoEmbeddingEngine()

	// Proximity check on identical texts
	v1, err := engine.Embed(context.Background(), "HubSpot CRM contact data and sync pipeline")
	if err != nil {
		t.Fatalf("failed to embed: %v", err)
	}
	v2, err := engine.Embed(context.Background(), "HubSpot CRM contact data and sync pipeline")
	if err != nil {
		t.Fatalf("failed to embed: %v", err)
	}

	sim1 := engine.CosineSimilarity(v1, v2)
	if sim1 < 0.99 {
		t.Errorf("expected near-identical similarity, got %.4f", sim1)
	}

	// Proximity check on unrelated texts
	v3, err := engine.Embed(context.Background(), "Deploy Docker containerized MCP daemon on AWS cluster")
	if err != nil {
		t.Fatalf("failed to embed: %v", err)
	}

	sim2 := engine.CosineSimilarity(v1, v3)
	if sim2 > 0.5 {
		t.Errorf("expected low similarity between unrelated texts, got %.4f", sim2)
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		s1       string
		s2       string
		expected float64
		tol      float64
	}{
		{
			name:     "Exact match",
			s1:       "create hubspot account",
			s2:       "create hubspot account",
			expected: 1.0,
			tol:      1e-9,
		},
		{
			name:     "Case insensitivity",
			s1:       "Create HubSpot Account",
			s2:       "create hubspot account",
			expected: 1.0,
			tol:      1e-9,
		},
		{
			name:     "Punctuation stripping",
			s1:       "create hubspot account!",
			s2:       "create, hubspot; account.",
			expected: 1.0,
			tol:      1e-9,
		},
		{
			name:     "Stop words filtering",
			s1:       "create a hubspot account",
			s2:       "create the hubspot account",
			expected: 1.0,
			tol:      1e-9,
		},
		{
			name:     "Boilerplate filtering",
			s1:       "Submitting requests related to: create hubspot account",
			s2:       "create hubspot account",
			expected: 1.0,
			tol:      1e-9,
		},
		{
			name:     "Partially similar matching",
			s1:       "create hubspot contact",
			s2:       "create new hubspot contact",
			expected: 0.8660254037844386, // 3 / sqrt(4 * 3) = 3 / sqrt(12) = 3 / 3.4641 = 0.866
			tol:      1e-4,
		},
		{
			name:     "Dissimilar match",
			s1:       "close ticket",
			s2:       "create account",
			expected: 0.0,
			tol:      1e-9,
		},
		{
			name:     "Zero norm empty strings",
			s1:       "",
			s2:       "",
			expected: 0.0,
			tol:      1e-9,
		},
		{
			name:     "Zero norm stop words only",
			s1:       "a and the to for",
			s2:       "a and the",
			expected: 0.0,
			tol:      1e-9,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			score := CosineSimilarity(tc.s1, tc.s2)
			if math.Abs(score-tc.expected) > tc.tol {
				t.Errorf("Expected CosineSimilarity(%q, %q) = %f (tolerance %f), got %f", tc.s1, tc.s2, tc.expected, tc.tol, score)
			}
		})
	}
}
