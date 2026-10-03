package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestTurnReductionCLI_DefaultPreparesWithoutProviderCalls(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	dir := t.TempDir()
	client := filepath.Join(dir, "agy")
	if err := os.WriteFile(client, []byte(`#!/bin/sh
if [ "$1" = --version ]; then echo 1.2.15; exit 0; fi
if [ "$1" = mcp ] && [ "$2" = list ] && [ -f "$HOME/.gemini/config/mcp_config.json" ]; then echo 'tzro stdio enabled tzro mcp'; exit 0; fi
echo 'unexpected provider launch' >&2
exit 99
`), 0755); err != nil {
		t.Fatal(err)
	}
	fixtures, err := filepath.Abs("../../pkg/benchmark/turnreduction/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	cmd := newTurnReductionBenchCmd()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	cmd.SetArgs([]string{"--fixtures", fixtures, "--client", client})
	if err = cmd.Execute(); err != nil {
		t.Fatalf("offline preparation failed: %v\n%s", err, output.String())
	}
	var result struct {
		OfflineReady      bool   `json:"offline_ready"`
		APIKeyPresent     bool   `json:"api_key_present"`
		ModelAvailability string `json:"model_availability"`
	}
	if err = json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatalf("invalid readiness: %v\n%s", err, output.String())
	}
	if !result.OfflineReady || result.APIKeyPresent || result.ModelAvailability != "unobserved" {
		t.Fatalf("readiness overstated live evidence: %+v", result)
	}
}
