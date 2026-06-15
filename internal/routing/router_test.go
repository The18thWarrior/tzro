package routing

import (
	"testing"
)

func TestRoute_ModelModeLocal(t *testing.T) {
	d := Route(RoutingContext{ModelMode: "local"})
	if d.Backend != "local" {
		t.Errorf("expected backend local, got %s", d.Backend)
	}
	if d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback false for ModelMode=local")
	}
}

func TestRoute_ModelModeCloud(t *testing.T) {
	d := Route(RoutingContext{ModelMode: "cloud"})
	if d.Backend != "cloud" {
		t.Errorf("expected backend cloud, got %s", d.Backend)
	}
	if d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback false for ModelMode=cloud")
	}
}

func TestRoute_RestrictedDir(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:      "cooperative",
		ActivePaths:    []string{"/secrets/db.go"},
		RestrictedDirs: []string{"/secrets"},
	})
	if d.Backend != "local" {
		t.Errorf("expected backend local, got %s", d.Backend)
	}
	if !d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined true for restricted dir match")
	}
	if d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback false for privacy quarantine")
	}
}

func TestRoute_RestrictedDir_ExactMatch(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:      "cooperative",
		ActivePaths:    []string{"/secrets"},
		RestrictedDirs: []string{"/secrets"},
	})
	if !d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined true for exact dir match")
	}
}

func TestRoute_RestrictedDir_NoMatch(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ActivePaths:         []string{"/public/app.go"},
		RestrictedDirs:      []string{"/secrets"},
		CloudKeyAvailable:   true,
		ComplexityTier:      "T2",
		ComplexityThreshold: "T1",
	})
	if d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined false for non-matching paths")
	}
}

func TestRoute_SensitiveKeyword(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:         "cooperative",
		Prompt:            "rotate the api_key for production",
		SensitiveKeywords: []string{"api_key", "password"},
	})
	if !d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined true for keyword match")
	}
	if d.Backend != "local" {
		t.Errorf("expected backend local, got %s", d.Backend)
	}
}

func TestRoute_SensitiveKeyword_CaseInsensitive(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:         "cooperative",
		Prompt:            "Rotate the API_KEY for production",
		SensitiveKeywords: []string{"api_key"},
	})
	if !d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined true for case-insensitive keyword match")
	}
}

func TestRoute_T0BelowT1(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ComplexityTier:      "T0",
		ComplexityThreshold: "T1",
		CloudKeyAvailable:   true,
	})
	if d.Backend != "local" {
		t.Errorf("expected backend local, got %s", d.Backend)
	}
	if !d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback true when below threshold")
	}
}

func TestRoute_T1AtT1(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ComplexityTier:      "T1",
		ComplexityThreshold: "T1",
		CloudKeyAvailable:   true,
	})
	if d.Backend != "local" {
		t.Errorf("expected backend local for T1 at T1 threshold, got %s", d.Backend)
	}
	if !d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback true")
	}
}

func TestRoute_T2AboveT1(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ComplexityTier:      "T2",
		ComplexityThreshold: "T1",
		CloudKeyAvailable:   true,
		PrivacyLevel:        "hybrid",
	})
	if d.Backend != "cloud" {
		t.Errorf("expected backend cloud for T2 above T1, got %s", d.Backend)
	}
}

func TestRoute_NoCloudKey(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ComplexityTier:      "T2",
		ComplexityThreshold: "T1",
		CloudKeyAvailable:   false,
	})
	if d.Backend != "local" {
		t.Errorf("expected backend local when no cloud key, got %s", d.Backend)
	}
	if d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback false when no cloud key")
	}
}

func TestRoute_StrictLocal(t *testing.T) {
	d := Route(RoutingContext{
		ModelMode:           "cooperative",
		ComplexityTier:      "T2",
		ComplexityThreshold: "T1",
		CloudKeyAvailable:   true,
		PrivacyLevel:        "strict-local",
	})
	if d.Backend != "local" {
		t.Errorf("expected backend local for strict-local, got %s", d.Backend)
	}
	if !d.PrivacyQuarantined {
		t.Error("expected PrivacyQuarantined true for strict-local")
	}
	if d.AllowCloudFallback {
		t.Error("expected AllowCloudFallback false for strict-local")
	}
}

func TestTierAtOrBelow(t *testing.T) {
	tests := []struct {
		actual    string
		threshold string
		expected  bool
	}{
		{"T0", "T0", true},
		{"T0", "T1", true},
		{"T0", "T2", true},
		{"T1", "T0", false},
		{"T1", "T1", true},
		{"T1", "T2", true},
		{"T2", "T0", false},
		{"T2", "T1", false},
		{"T2", "T2", true},
		{"invalid", "T1", false},
		{"T1", "invalid", false},
	}

	for _, tc := range tests {
		result := tierAtOrBelow(tc.actual, tc.threshold)
		if result != tc.expected {
			t.Errorf("tierAtOrBelow(%q, %q) = %v, want %v", tc.actual, tc.threshold, result, tc.expected)
		}
	}
}
