package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

func TestAssembler_PrivacyBlocksEnvFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create a normal Go file and a .env file in the workspace
	_ = os.MkdirAll(filepath.Join(tempDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "auth.go"), []byte(`package pkg
func Auth() string { return "ok" }
`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".env"), []byte(`DATABASE_URL=postgres://localhost/mydb
SECRET_KEY=supersecret123
`), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, ".env.local"), []byte(`API_KEY=sk-test-key-12345
`), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Policy that blocks .env* files
	policy := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{PathPattern: ".env*", Action: dlp.ActionBlock, Description: "Block env files"},
		},
	})

	assembler := NewAssembler(s, policy)
	pack, err := assembler.Assemble(tempDir, "auth env database", 4000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	// Verify that .env files are excluded from the context pack
	for _, item := range pack.Items {
		base := filepath.Base(item.FilePath)
		if strings.HasPrefix(base, ".env") {
			t.Errorf("privacy-blocked file %q should not appear in context pack", item.FilePath)
		}
	}
}

func TestAssembler_PrivacyDropsBlockedContent(t *testing.T) {
	tempDir := t.TempDir()

	// Create a file containing an API key
	_ = os.MkdirAll(filepath.Join(tempDir, "config"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "config", "keys.go"), []byte(`package config
// APIKey is the production key
var APIKey = "sk-proj-1234567890abcdef1234567890abcdef"
`), 0644)

	// Create a clean file
	_ = os.WriteFile(filepath.Join(tempDir, "config", "settings.go"), []byte(`package config
var MaxRetries = 3
`), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Policy that blocks api_key data class
	policy := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{DataClass: "api_key", Action: dlp.ActionBlock, Description: "Block API keys"},
		},
	})

	assembler := NewAssembler(s, policy)
	pack, err := assembler.Assemble(tempDir, "config keys settings", 4000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	// Verify that the file containing the API key is excluded
	for _, item := range pack.Items {
		if strings.Contains(item.Content, "sk-proj-") {
			t.Errorf("file with blocked API key content should not appear in context pack: %s", item.FilePath)
		}
	}
}

func TestAssembler_PrivacyRedactsCandidateContent(t *testing.T) {
	tempDir := t.TempDir()

	// Create a file with a secret that should be redacted
	_ = os.MkdirAll(filepath.Join(tempDir, "deploy"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "deploy", "setup.go"), []byte(`package deploy
// Deployment config
var Token = "sk-proj-abcdefghijklmnopqrstuvwxyz1234"
func Deploy() {}
`), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Policy that redacts api_key data class
	policy := dlp.NewPolicyEngine(&dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{DataClass: "api_key", Action: dlp.ActionRedact, Description: "Redact API keys"},
		},
	})

	assembler := NewAssembler(s, policy)
	pack, err := assembler.Assemble(tempDir, "deploy setup token", 4000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	if len(pack.Items) == 0 {
		t.Fatal("expected at least one item in context pack")
	}

	// Verify the secret is not present in any packed item content
	foundSetup := false
	for _, item := range pack.Items {
		if strings.Contains(item.Content, "sk-proj-") {
			t.Errorf("secret should be redacted in context pack item %s", item.FilePath)
		}
		if strings.Contains(item.FilePath, "setup") {
			foundSetup = true
			// Content should contain a redaction placeholder if the body was preserved
			if strings.Contains(item.Content, "Token") && !strings.Contains(item.Content, "REDACTED") {
				t.Errorf("expected redaction placeholder in content for %s", item.FilePath)
			}
		}
	}
	if !foundSetup {
		t.Error("expected deploy/setup.go in context pack items")
	}
}

func TestAssembler_NilPolicyBehavesLikeNoPolicy(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.MkdirAll(filepath.Join(tempDir, "pkg"), 0755)
	_ = os.WriteFile(filepath.Join(tempDir, "pkg", "main.go"), []byte(`package pkg
func Main() {}
`), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// nil policy — should work exactly like before
	assembler := NewAssembler(s, nil)
	pack, err := assembler.Assemble(tempDir, "main", 4000)
	if err != nil {
		t.Fatalf("Assemble with nil policy failed: %v", err)
	}

	if len(pack.Items) == 0 {
		t.Fatal("expected at least one item with nil policy")
	}
}
