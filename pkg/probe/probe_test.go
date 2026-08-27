package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestProbe_Search(t *testing.T) {
	tempDir := t.TempDir()

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Create sample files
	authGo := `package auth

func ValidateJWT(token string) bool {
	// check expiration
	return token != ""
}
`
	err = os.WriteFile(filepath.Join(tempDir, "auth.go"), []byte(authGo), 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	mainGo := `package main

func main() {
	println("server starting")
}
`
	err = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(mainGo), 0644)
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	report, err := Probe(tempDir, "ValidateJWT", 10, s)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	if len(report.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d", len(report.Matches))
	}

	if report.Matches[0].FilePath != "auth.go" {
		t.Errorf("expected auth.go, got %s", report.Matches[0].FilePath)
	}

	markdown := report.FormatMarkdown()
	if !strings.Contains(markdown, "Found 1 matches for \"ValidateJWT\"") {
		t.Errorf("expected markdown summary header, got:\n%s", markdown)
	}
}
