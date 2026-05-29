package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestCLI_FlagResolution(t *testing.T) {
	// Create mock Cobra command structure to test flags
	cmd := &cobra.Command{
		Use: "tzro",
		Run: func(cmd *cobra.Command, args []string) {},
	}

	initRootFlags(cmd)

	// 1. Assert default values
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	if err != nil {
		t.Fatalf("failed to execute default command: %v", err)
	}

	if globalFlags.URL != "http://localhost:8080" {
		t.Errorf("expected default URL http://localhost:8080, got: %s", globalFlags.URL)
	}
	if globalFlags.Offline {
		t.Error("expected default Offline to be false, got true")
	}
	if globalFlags.DBPath != "tzro.db" {
		t.Errorf("expected default DBPath tzro.db, got: %s", globalFlags.DBPath)
	}
	if globalFlags.JSONOut {
		t.Error("expected default JSONOut to be false, got true")
	}

	// 2. Assert customized flags
	cmd.SetArgs([]string{"--url", "http://localhost:9090", "--offline", "--db", "test.db", "--json"})
	err = cmd.Execute()
	if err != nil {
		t.Fatalf("failed to execute command with flags: %v", err)
	}

	if globalFlags.URL != "http://localhost:9090" {
		t.Errorf("expected custom URL http://localhost:9090, got: %s", globalFlags.URL)
	}
	if !globalFlags.Offline {
		t.Error("expected custom Offline to be true, got false")
	}
	if globalFlags.DBPath != "test.db" {
		t.Errorf("expected custom DBPath test.db, got: %s", globalFlags.DBPath)
	}
	if !globalFlags.JSONOut {
		t.Error("expected custom JSONOut to be true, got false")
	}
}

func TestCLI_ClientResolution(t *testing.T) {
	// Test direct client resolution routing based on flags
	globalFlags.Offline = true
	globalFlags.DBPath = "temp_tzro_test.db"

	client, err := GetClient()
	if err != nil {
		t.Fatalf("failed to GetClient in offline mode: %v", err)
	}

	directClient, ok := client.(*DirectDBClient)
	if !ok {
		t.Errorf("expected *DirectDBClient in offline mode, got: %T", client)
	} else if directClient.DBPath != "temp_tzro_test.db" {
		t.Errorf("expected client DB path temp_tzro_test.db, got: %s", directClient.DBPath)
	}
}

func TestCLI_JSONFormatHelper(t *testing.T) {
	// Verify JSON output printing format helper
	data := map[string]string{"foo": "bar"}
	var buf bytes.Buffer

	err := printJSON(&buf, data)
	if err != nil {
		t.Fatalf("printJSON failed: %v", err)
	}

	output := strings.TrimSpace(buf.String())
	expected := `{"foo":"bar"}`
	if output != expected {
		t.Errorf("expected JSON output %s, got: %s", expected, output)
	}
}

func TestCLI_BenchmarkFlagResolution(t *testing.T) {
	// Assert default mode
	benchmarkRunCmd.ParseFlags([]string{})
	if benchmarkMode != "interactive" {
		t.Errorf("expected default mode interactive, got: %s", benchmarkMode)
	}

	// Parse benchmark flags manually to assert registration and parsing
	benchmarkRunCmd.SetArgs([]string{"--dataset", "complexfuncbench", "--mode", "interactive", "--verbose"})
	err := benchmarkRunCmd.ParseFlags([]string{"--dataset", "complexfuncbench", "--mode", "interactive", "--verbose"})
	if err != nil {
		t.Fatalf("failed to parse benchmark flags: %v", err)
	}

	if benchmarkDataset != "complexfuncbench" {
		t.Errorf("expected dataset complexfuncbench, got: %s", benchmarkDataset)
	}
	if benchmarkMode != "interactive" {
		t.Errorf("expected mode interactive, got: %s", benchmarkMode)
	}
	if !benchmarkVerbose {
		t.Error("expected benchmarkVerbose to be true, got false")
	}
}
