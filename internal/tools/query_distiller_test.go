package tools

import (
	"context"
	"strings"
	"testing"
)

func TestDistillSearchQuery_ConcisePassedThrough(t *testing.T) {
	short := "golang 1.24 crypto tls"
	res := DistillSearchQuery(context.Background(), short)
	if res != short {
		t.Errorf("expected %q, got %q", short, res)
	}
}

func TestDistillSearchQuery_PreservesSiteAndCVE(t *testing.T) {
	verbose := "Please browse at least 3 distinct source URLs for CVE-2024-45338 details site:pkg.go.dev"
	res := DistillSearchQuery(context.Background(), verbose)

	if !strings.Contains(res, "site:pkg.go.dev") {
		t.Errorf("expected 'site:pkg.go.dev' in result, got %q", res)
	}
	if !strings.Contains(res, "CVE-2024-45338") {
		t.Errorf("expected 'CVE-2024-45338' in result, got %q", res)
	}
}

func TestDistillFallback_NoiseStripping(t *testing.T) {
	input := "deterministic replay, journaled state machine, language SDK support, deployment models (self-hosted, cloud, serverless) site:docs.restate.dev"
	res := distillFallback(input)

	if !strings.Contains(res, "site:docs.restate.dev") {
		t.Errorf("expected site:docs.restate.dev in result, got %q", res)
	}
	words := strings.Fields(res)
	if len(words) > 10 {
		t.Errorf("expected condensed query <= 10 words, got %d words: %q", len(words), res)
	}
}
