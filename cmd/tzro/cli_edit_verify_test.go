package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"tzro/pkg/verification"
)

// Exercise process stdin/stdout and domain outcomes, not just the shared service.
func TestCLI_EditAndVerifyObservedOutcomes(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required")
	}
	binary := filepath.Join(t.TempDir(), "tzro")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	cases := []struct {
		name, old    string
		argv         []string
		application  verification.ApplicationStatus
		verification verification.VerificationStatus
		exit         *int
	}{
		{"passed", "old", []string{python, "-c", "from pathlib import Path; assert Path('value.txt').read_text() == 'new'"}, verification.ApplicationApplied, verification.VerificationPassed, intPointer(0)},
		{"failed check retains edit", "old", []string{python, "-c", "print('independent check failed'); raise SystemExit(7)"}, verification.ApplicationApplied, verification.VerificationFailed, intPointer(7)},
		{"missing executable", "old", []string{"tzro-intentionally-missing-check-84019"}, verification.ApplicationApplied, verification.VerificationFailed, nil},
		{"missing preset", "old", nil, verification.ApplicationApplied, verification.VerificationNotConfigured, nil},
		{"conflict rejects edit", "absent", []string{python, "-c", "raise SystemExit(0)"}, verification.ApplicationRejected, verification.VerificationUnavailable, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, "value.txt"), []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			if tc.argv != nil {
				if err := os.Mkdir(filepath.Join(workspace, ".tzro"), 0700); err != nil {
					t.Fatal(err)
				}
				argv, _ := json.Marshal(tc.argv)
				preset := fmt.Sprintf("version: 1\nchecks:\n  - id: independent\n    argv: %s\n    cwd: .\n", argv)
				if err := os.WriteFile(filepath.Join(workspace, ".tzro", "verification.yaml"), []byte(preset), 0600); err != nil {
					t.Fatal(err)
				}
			}
			request, _ := json.Marshal(verification.Request{Edits: []verification.Edit{{Kind: "replace", Path: "value.txt", OldText: tc.old, NewText: "new", ExpectedMatches: 1}}})
			command := exec.Command(binary, "edit-and-verify", "--request", "-")
			command.Dir = workspace
			command.Stdin = bytes.NewReader(request)
			output, err := command.Output()
			if err != nil {
				t.Fatalf("CLI failed to deliver domain outcome: %v", err)
			}
			var summary verification.Summary
			if err := json.Unmarshal(output, &summary); err != nil {
				t.Fatalf("invalid CLI JSON: %v\n%s", err, output)
			}
			if summary.Application != tc.application || summary.Verification != tc.verification {
				t.Fatalf("wrong domain outcome: %s", output)
			}
			content, err := os.ReadFile(filepath.Join(workspace, "value.txt"))
			if err != nil {
				t.Fatal(err)
			}
			want := "new"
			if tc.application == verification.ApplicationRejected {
				want = "old"
			}
			if string(content) != want {
				t.Fatalf("final file=%q, want %q", content, want)
			}
			if tc.exit != nil && (len(summary.Checks) != 1 || summary.Checks[0].ExitCode == nil || *summary.Checks[0].ExitCode != *tc.exit) {
				t.Fatalf("missing observed native check exit: %s", output)
			}
			if tc.name == "missing executable" && (len(summary.Checks) != 1 || len(summary.Checks[0].Diagnostics) == 0 || summary.Checks[0].ExitCode != nil) {
				t.Fatalf("missing executable was not reported truthfully: %s", output)
			}
		})
	}
}

func intPointer(value int) *int { return &value }
