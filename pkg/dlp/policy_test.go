package dlp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPolicyEngine_RulesAndEnforcement(t *testing.T) {
	policy := &WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: ActionAllow,
		Rules: []PolicyRule{
			{PathPattern: ".env*", Action: ActionBlock, Description: "Block env files"},
			{PathPattern: "secrets/*", Action: ActionDeny, Description: "Deny secrets directory"},
			{PathPattern: "src/*", Action: ActionRedact, Description: "Redact source files"},
			{DataClass: "api_key", Action: ActionBlock, Description: "Block all api keys"},
		},
	}

	engine := NewPolicyEngine(policy)

	// Test 1: Blocked by path pattern (.env)
	eval := engine.EvaluatePath(".env.local")
	if eval.Allowed || eval.Action != ActionBlock {
		t.Errorf("expected .env.local to be blocked, got allowed=%v action=%v", eval.Allowed, eval.Action)
	}

	// Test 2: Denied by path pattern (secrets/key.txt)
	eval = engine.EvaluatePath("secrets/database.yml")
	if eval.Allowed || eval.Action != ActionDeny {
		t.Errorf("expected secrets/database.yml to be denied, got allowed=%v action=%v", eval.Allowed, eval.Action)
	}

	// Test 3: Redact by path pattern
	eval = engine.EvaluatePath("src/main.go")
	if !eval.Allowed || eval.Action != ActionRedact {
		t.Errorf("expected src/main.go to be allowed with redact action, got allowed=%v action=%v", eval.Allowed, eval.Action)
	}

	// Test 4: Default allow for other paths
	eval = engine.EvaluatePath("docs/readme.md")
	if !eval.Allowed || eval.Action != ActionAllow {
		t.Errorf("expected docs/readme.md to be allowed, got allowed=%v action=%v", eval.Allowed, eval.Action)
	}

	// Test 5: Content matching blocked data class (API key)
	eval = engine.EvaluateContent("Here is the key: sk-proj-1234567890abcdef1234567890abcdef for testing")
	if eval.Allowed || eval.Action != ActionBlock {
		t.Errorf("expected content with api_key to be blocked, got allowed=%v", eval.Allowed)
	}

	// Test 6: Safe content evaluation
	eval = engine.EvaluateContent("Hello world, this has no secrets.")
	if !eval.Allowed {
		t.Errorf("expected safe content to be allowed, got %v", eval.Reason)
	}
}

func TestLoadWorkspacePolicy_DefaultAndFile(t *testing.T) {
	tempDir := t.TempDir()

	// Loading without privacy.json should return safe default policy
	p, err := LoadWorkspacePolicy(tempDir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got %v", err)
	}
	if len(p.Rules) == 0 {
		t.Errorf("expected default rules, got 0")
	}

	// Now write a custom privacy.json
	tzroDir := filepath.Join(tempDir, ".tzro")
	_ = os.MkdirAll(tzroDir, 0755)
	customJSON := `{
		"version": "1.0",
		"default_action": "deny",
		"rules": [
			{"path_pattern": "public/*", "action": "allow"}
		]
	}`
	_ = os.WriteFile(filepath.Join(tzroDir, "privacy.json"), []byte(customJSON), 0644)

	customP, err := LoadWorkspacePolicy(tempDir)
	if err != nil {
		t.Fatalf("failed to load custom policy: %v", err)
	}
	if customP.DefaultAction != ActionDeny {
		t.Errorf("expected default_action deny, got %v", customP.DefaultAction)
	}
	if len(customP.Rules) != 1 {
		t.Errorf("expected 1 rule, got %d", len(customP.Rules))
	}
}

