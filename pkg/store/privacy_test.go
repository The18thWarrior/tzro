package store

import (
	"strings"
	"testing"

	"tzro/pkg/dlp"
)

func TestPutArtifact_PrivacyBlocksDeniedContent(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	pe := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{DataClass: "api_key", Action: dlp.ActionBlock},
		},
	})
	s.SetPolicy(pe)

	art := &Artifact{
		Type:      "text",
		Workspace: "default",
		Body:      "sk-proj-1234567890abcdef1234567890abcdef",
	}

	_, err = s.PutArtifact(art)
	if err == nil {
		t.Fatalf("expected error due to privacy policy block, got nil")
	}
	if !strings.Contains(err.Error(), "privacy policy") {
		t.Errorf("expected error to contain 'privacy policy', got: %v", err)
	}
}

func TestPutArtifact_PrivacyRedactsContent(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	pe := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{DataClass: "api_key", Action: dlp.ActionRedact},
		},
	})
	s.SetPolicy(pe)

	originalBody := "config key=sk-proj-1234567890abcdef1234567890abcdef"
	art := &Artifact{
		Type:      "text",
		Workspace: "default",
		Body:      originalBody,
	}

	id, err := s.PutArtifact(art)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	gotArt, err := s.GetArtifact(id, "default")
	if err != nil {
		t.Fatalf("expected artifact, got error: %v", err)
	}

	if !gotArt.IsRedacted {
		t.Errorf("expected IsRedacted == true")
	}
	if strings.Contains(gotArt.Body, "sk-proj-") {
		t.Errorf("expected Body not to contain sk-proj-, got %s", gotArt.Body)
	}
	if !strings.Contains(gotArt.Body, "[REDACTED_OPENAI_KEY_") {
		t.Errorf("expected Body to contain [REDACTED_OPENAI_KEY_, got %s", gotArt.Body)
	}
	if gotArt.SourceHash == "" {
		t.Errorf("expected SourceHash != empty")
	}
	if gotArt.SourceHash == gotArt.Hash {
		t.Errorf("expected SourceHash != Hash")
	}
}

func TestPutArtifact_PrivacyAllowsCleanContent(t *testing.T) {
	s, err := OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	pe := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{DataClass: "api_key", Action: dlp.ActionBlock},
		},
	})
	s.SetPolicy(pe)

	originalBody := "func main() { fmt.Println(\"hello\") }"
	art := &Artifact{
		Type:      "text",
		Workspace: "default",
		Body:      originalBody,
	}

	id, err := s.PutArtifact(art)
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	gotArt, err := s.GetArtifact(id, "default")
	if err != nil {
		t.Fatalf("expected artifact, got error: %v", err)
	}

	if gotArt.IsRedacted {
		t.Errorf("expected IsRedacted == false")
	}
	if gotArt.Body != originalBody {
		t.Errorf("expected Body to match original, got %s", gotArt.Body)
	}
	if gotArt.SourceHash != "" {
		t.Errorf("expected SourceHash == empty")
	}
}
