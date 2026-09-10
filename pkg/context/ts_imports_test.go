package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestContextPack_TypeScriptImportGraph(t *testing.T) {
	tempDir := t.TempDir()

	srcDir := filepath.Join(tempDir, "src")
	_ = os.MkdirAll(srcDir, 0755)

	// File 1: utils/token.ts
	utilsDir := filepath.Join(srcDir, "utils")
	_ = os.MkdirAll(utilsDir, 0755)
	tokenTS := `export interface TokenConfig {
  secret: string;
  expiresIn: number;
}

export function validateToken(token: string): boolean {
  return token.length > 10;
}
`
	_ = os.WriteFile(filepath.Join(utilsDir, "token.ts"), []byte(tokenTS), 0644)

	// File 2: authService.ts importing token.ts
	authTS := `import { validateToken, TokenConfig } from './utils/token';

export class AuthService {
  check(token: string): boolean {
    return validateToken(token);
  }
}
`
	_ = os.WriteFile(filepath.Join(srcDir, "authService.ts"), []byte(authTS), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s, nil)

	// Query for authService: should bring in authService.ts AND follow the import graph to include utils/token.ts
	pack, err := assembler.Assemble(tempDir, "authService", 2000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	foundAuthService := false
	foundTokenUtils := false
	tokenReason := ""

	for _, item := range pack.Items {
		if strings.Contains(item.FilePath, "authService.ts") {
			foundAuthService = true
		}
		if strings.Contains(item.FilePath, "token.ts") {
			foundTokenUtils = true
			tokenReason = item.Reason
		}
	}

	if !foundAuthService {
		t.Errorf("expected authService.ts in context pack")
	}
	if !foundTokenUtils {
		t.Errorf("expected utils/token.ts to be resolved via import graph")
	}
	if !strings.Contains(tokenReason, "Imported by") {
		t.Errorf("expected explainable reason citing import relationship, got %q", tokenReason)
	}
}
