package laya

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

var mockDaemonBin string
var mockDaemonCrashBin string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "laya-test-*")
	if err != nil {
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	mockDaemonBin = filepath.Join(tmpDir, "mock_daemon")
	cmd := exec.Command("go", "build", "-o", mockDaemonBin, "./testdata/mock_daemon.go")
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}

	mockDaemonCrashBin = filepath.Join(tmpDir, "mock_daemon_crash")
	cmd = exec.Command("go", "build", "-o", mockDaemonCrashBin, "./testdata/mock_daemon_crash.go")
	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestDaemonClient_NoulRoundtrip(t *testing.T) {
	client := NewDaemonClient(mockDaemonBin)
	ctx := context.Background()
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer client.Close()

	req := &DecisionRequest{
		QuestionType: "noul",
		Prompt:       "test?",
	}
	resp, err := client.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if resp.Answer != "yes" {
		t.Errorf("Expected 'yes', got %q", resp.Answer)
	}
}

func TestDaemonClient_ChoiceRoundtrip(t *testing.T) {
	client := NewDaemonClient(mockDaemonBin)
	ctx := context.Background()
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer client.Close()

	req := &DecisionRequest{
		QuestionType: "choice",
		Prompt:       "pick",
		Options:      []string{"option1", "option2"},
	}
	resp, err := client.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("Evaluate error: %v", err)
	}
	if resp.Answer != "option1" {
		t.Errorf("Expected 'option1', got %q", resp.Answer)
	}
}

func TestDaemonClient_ContextCancellation(t *testing.T) {
	client := NewDaemonClient(mockDaemonBin)
	ctx, cancel := context.WithCancel(context.Background())
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer client.Close()

	cancel()
	req := &DecisionRequest{
		QuestionType: "noul",
	}
	_, err = client.Evaluate(ctx, req)
	if err == nil {
		t.Fatal("Expected error on canceled context, got nil")
	}
}

func TestDaemonClient_CrashRestart(t *testing.T) {
	client := NewDaemonClient(mockDaemonCrashBin)
	ctx := context.Background()
	err := client.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer client.Close()

	req := &DecisionRequest{QuestionType: "noul"}
	resp, err := client.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("First Evaluate error: %v", err)
	}
	if resp.Answer != "yes" {
		t.Errorf("Expected 'yes', got %q", resp.Answer)
	}

	time.Sleep(100 * time.Millisecond)

	resp2, err := client.Evaluate(ctx, req)
	if err != nil {
		t.Fatalf("Second Evaluate error: %v", err)
	}
	if resp2.Answer != "yes" {
		t.Errorf("Expected 'yes', got %q", resp2.Answer)
	}
}
