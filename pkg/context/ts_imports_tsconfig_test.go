package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"tzro/pkg/store"
)

func TestContextPack_TSConfigPathAlias(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Write tsconfig.json with comments (JSONC) and path aliases
	tsconfigJSONC := `{
		// Compiler configurations
		"compilerOptions": {
			"baseUrl": ".",
			"paths": {
				"@components/*": ["src/components/*"],
				"@/*": ["src/*"]
			}
		} /* end of options */
	}`
	if err := os.WriteFile(filepath.Join(tempDir, "tsconfig.json"), []byte(tsconfigJSONC), 0644); err != nil {
		t.Fatalf("failed to write tsconfig.json: %v", err)
	}

	// 2. Create target directory and file: src/components/Button.tsx
	btnDir := filepath.Join(tempDir, "src", "components")
	_ = os.MkdirAll(btnDir, 0755)
	buttonTSX := `export function Button() { return <button>Click</button>; }`
	_ = os.WriteFile(filepath.Join(btnDir, "Button.tsx"), []byte(buttonTSX), 0644)

	// 3. Create consumer file: src/views/Home.tsx importing via alias @components/Button
	viewsDir := filepath.Join(tempDir, "src", "views")
	_ = os.MkdirAll(viewsDir, 0755)
	homeTSX := `import { Button } from '@components/Button';
export function Home() { return <Button />; }`
	_ = os.WriteFile(filepath.Join(viewsDir, "Home.tsx"), []byte(homeTSX), 0644)

	// 4. Test TSConfig loading directly
	cfg := LoadTSConfig(tempDir)
	if cfg == nil {
		t.Fatalf("expected LoadTSConfig to successfully parse JSONC tsconfig")
	}
	if cfg.BaseURL != "." {
		t.Errorf("expected baseUrl '.', got %q", cfg.BaseURL)
	}
	if len(cfg.Paths["@components/*"]) == 0 {
		t.Errorf("expected @components/* path alias mapped")
	}

	// 5. Assemble context pack for "Home"
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s, nil)
	pack, err := assembler.Assemble(tempDir, "Home", 4000)
	if err != nil {
		t.Fatalf("Assemble failed: %v", err)
	}

	foundHome := false
	foundButton := false
	for _, item := range pack.Items {
		if strings.Contains(item.FilePath, "Home.tsx") {
			foundHome = true
		}
		if strings.Contains(item.FilePath, "Button.tsx") {
			foundButton = true
		}
	}

	if !foundHome {
		t.Errorf("expected Home.tsx in pack")
	}
	if !foundButton {
		t.Errorf("expected Button.tsx to be resolved via @components/* path alias in tsconfig")
	}
}
