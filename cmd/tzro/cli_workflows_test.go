package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCLI_WorkflowsEndToEnd(t *testing.T) {
	// 1. Test tzro impact --help
	cmdImpact := &bytes.Buffer{}
	root := newRootCmd()
	root.SetOut(cmdImpact)
	root.SetArgs([]string{"impact", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro impact --help failed: %v", err)
	}
	if !strings.Contains(cmdImpact.String(), "--budget") {
		t.Errorf("impact --help missing --budget flag: %s", cmdImpact.String())
	}

	// 2. Test tzro search --help
	cmdSearch := &bytes.Buffer{}
	root = newRootCmd()
	root.SetOut(cmdSearch)
	root.SetArgs([]string{"search", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro search --help failed: %v", err)
	}
	if !strings.Contains(cmdSearch.String(), "--format") {
		t.Errorf("search --help missing --format flag: %s", cmdSearch.String())
	}

	// 3. Test tzro inspect --help
	cmdInspect := &bytes.Buffer{}
	root = newRootCmd()
	root.SetOut(cmdInspect)
	root.SetArgs([]string{"inspect", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro inspect --help failed: %v", err)
	}
	if !strings.Contains(cmdInspect.String(), "replay") {
		t.Errorf("inspect --help missing replay subcommand: %s", cmdInspect.String())
	}

	// 4. Test tzro session --help
	cmdSession := &bytes.Buffer{}
	root = newRootCmd()
	root.SetOut(cmdSession)
	root.SetArgs([]string{"session", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro session --help failed: %v", err)
	}
	if !strings.Contains(cmdSession.String(), "commit") {
		t.Errorf("session --help missing commit subcommand: %s", cmdSession.String())
	}
	if !strings.Contains(cmdSession.String(), "status") {
		t.Errorf("session --help missing status subcommand: %s", cmdSession.String())
	}

	// 5. Test tzro compact --help
	cmdCompact := &bytes.Buffer{}
	root = newRootCmd()
	root.SetOut(cmdCompact)
	root.SetArgs([]string{"compact", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro compact --help failed: %v", err)
	}
	if !strings.Contains(cmdCompact.String(), "--run") {
		t.Errorf("compact --help missing --run flag: %s", cmdCompact.String())
	}
}

func TestCLI_ContextCommand_Issue10(t *testing.T) {
	// 1. Test tzro context --help
	cmdContext := &bytes.Buffer{}
	root := newRootCmd()
	root.SetOut(cmdContext)
	root.SetArgs([]string{"context", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("tzro context --help failed: %v", err)
	}
	out := cmdContext.String()
	for _, flag := range []string{"--symbol", "--file", "--format", "--output", "--force", "--budget"} {
		if !strings.Contains(out, flag) {
			t.Errorf("context --help missing flag %s:\n%s", flag, out)
		}
	}
}

func TestCLI_InitConfig_Issue11(t *testing.T) {
	// 1. Verify init --help contains --config
	cmdInit := &bytes.Buffer{}
	root := newRootCmd()
	root.SetOut(cmdInit)
	root.SetArgs([]string{"init", "--help"})
	if err := root.Execute(); err != nil {
		t.Fatalf("init --help failed: %v", err)
	}
	if !strings.Contains(cmdInit.String(), "--config") {
		t.Errorf("init --help missing --config flag: %s", cmdInit.String())
	}
}
