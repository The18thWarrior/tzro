package main

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestInstallScript(t *testing.T) {
	// Create a temporary install directory
	tempDir, err := os.MkdirTemp("", "tzro-install-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Build the real tzro binary so the installer can copy/use it
	realBinPath := filepath.Join(tempDir, "tzro_bin")
	buildCmd := exec.Command("go", "build", "-o", realBinPath, "./cmd/tzro")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build real tzro binary: %v", err)
	}

	// Prepare environment for install.sh execution
	cmd := exec.Command("bash", "install.sh")
	cmd.Env = append(os.Environ(),
		"TZRO_INSTALL_DIR="+tempDir,
		"TZRO_MOCK_DOWNLOAD=true",
		"TZRO_SOURCE_BIN="+realBinPath,
	)

	// Capture output
	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		t.Fatalf("install.sh failed with error: %v\nOutput:\n%s", err, output)
	}

	// 1. Verify directory structure
	dirs := []string{"bin", "cache", "models"}
	for _, d := range dirs {
		p := filepath.Join(tempDir, d)
		info, err := os.Stat(p)
		if err != nil {
			t.Errorf("expected directory %s to exist, but got err: %v", p, err)
		} else if !info.IsDir() {
			t.Errorf("expected %s to be a directory, but it is not", p)
		}
	}

	// 2. Verify files created
	expectedFiles := []string{
		filepath.Join(tempDir, "bin", "llama-server"),
		filepath.Join(tempDir, "bin", "tzro"),
		filepath.Join(tempDir, "bin", "tzro-mcp"),
		filepath.Join(tempDir, "models", "gemma-4-E4B-it-qat-UD-Q4_K_XL.gguf"),
		filepath.Join(tempDir, "models", "gemma-4-E4B-it-qat-assistant-q4_k_m.gguf"),
		filepath.Join(tempDir, "tzro.db"),
	}
	for _, f := range expectedFiles {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("expected file %s to be created, but got err: %v", f, err)
		}
	}

	// 3. Verify SQLite DB initialization (tzro.db must have tables like node_states, memories, etc.)
	dbPath := filepath.Join(tempDir, "tzro.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open created sqlite db: %v", err)
	}
	defer db.Close()

	// Query for table existence
	var count int
	err = db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='fact_memories'").Scan(&count)
	if err != nil {
		t.Errorf("failed to query fact_memories table: %v", err)
	}
	if count != 1 {
		t.Errorf("expected table 'fact_memories' to be created, but got count %d", count)
	}

	// 4. Verify stdout prints the dashboard
	if !strings.Contains(output, "TZRO") && !strings.Contains(output, "Dashboard") {
		t.Logf("Output: %s", output)
		t.Errorf("expected install output to print premium tzro install dashboard")
	}
}
