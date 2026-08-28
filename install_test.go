package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallScript(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tzro-install-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	realBinPath := filepath.Join(tempDir, "tzro_bin")
	buildCmd := exec.Command("go", "build", "-o", realBinPath, "./cmd/tzro")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("failed to build real tzro binary: %v", err)
	}

	fakeHome := filepath.Join(tempDir, "fakehome")
	_ = os.MkdirAll(fakeHome, 0o755)

	cmd := exec.Command("bash", "install.sh")
	cmd.Env = append(os.Environ(),
		"TZRO_INSTALL_DIR="+tempDir,
		"TZRO_MOCK_DOWNLOAD=true",
		"TZRO_SOURCE_BIN="+realBinPath,
		"HOME="+fakeHome,
	)

	outputBytes, err := cmd.CombinedOutput()
	output := string(outputBytes)
	if err != nil {
		t.Fatalf("install.sh failed with error: %v\nOutput:\n%s", err, output)
	}

	// Verify binary was installed
	installedBin := filepath.Join(tempDir, "bin", "tzro")
	info, err := os.Stat(installedBin)
	if err != nil {
		t.Fatalf("expected installed tzro binary at %s, got error: %v", installedBin, err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("expected tzro binary to be executable")
	}

	if !strings.Contains(output, "TZRO v2 INSTALLATION COMPLETE") {
		t.Errorf("expected completion message in output, got:\n%s", output)
	}
}
