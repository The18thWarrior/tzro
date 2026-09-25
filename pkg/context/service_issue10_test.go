package context_test

import (
	stdctx "context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tzroctx "tzro/pkg/context"
	"tzro/pkg/store"
	"tzro/pkg/tokenizer"
)

func TestService_QueryAndSymbolMutualExclusion(t *testing.T) {
	tempDir := t.TempDir()
	svc := tzroctx.NewContextService(nil, nil)
	ctx := stdctx.Background()

	// 1. Both query and symbol provided
	_, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Query:         "do something",
		Symbol:        "MyFunc",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot specify both") {
		t.Errorf("expected error for both query and symbol, got: %v", err)
	}

	// 2. Neither query nor symbol provided
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
	})
	if err == nil || !strings.Contains(err.Error(), "must specify either") {
		t.Errorf("expected error for neither query nor symbol, got: %v", err)
	}

	// 3. File without symbol
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Query:         "do something",
		File:          "foo.go",
	})
	if err == nil || !strings.Contains(err.Error(), "--file requires --symbol") {
		t.Errorf("expected error for file without symbol, got: %v", err)
	}

	// 4. Nonpositive budget
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Query:         "something",
		Budget:        -5,
	})
	if err == nil || !errors.Is(err, tokenizer.ErrBudgetTooSmall) {
		t.Errorf("expected ErrBudgetTooSmall for negative budget, got: %v", err)
	}

	// 5. Invalid format
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Query:         "something",
		Format:        "yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported format") {
		t.Errorf("expected error for unsupported format, got: %v", err)
	}
}

func TestService_DuplicateSymbolDisambiguation(t *testing.T) {
	tempDir := t.TempDir()

	runnerDir := filepath.Join(tempDir, "pkg", "runner")
	taskDir := filepath.Join(tempDir, "pkg", "task")
	_ = os.MkdirAll(runnerDir, 0755)
	_ = os.MkdirAll(taskDir, 0755)

	runnerCode := `package runner

import "fmt"

// Execute runs the runner pipeline.
func Execute() {
	fmt.Println("running pipeline")
}
`
	taskCode := `package task

import "fmt"

// Execute performs the task unit of work.
func Execute() {
	fmt.Println("executing task")
}
`
	_ = os.WriteFile(filepath.Join(runnerDir, "exec.go"), []byte(runnerCode), 0644)
	_ = os.WriteFile(filepath.Join(taskDir, "task.go"), []byte(taskCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	svc := tzroctx.NewContextService(s, nil)
	ctx := stdctx.Background()

	// 1. Bare --symbol Execute without --file must fail with actionable candidate error
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "Execute",
		Budget:        2000,
	})
	if err == nil {
		t.Fatal("expected ambiguity error for duplicate symbol Execute, got nil")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "ambiguous symbol \"Execute\"") ||
		!strings.Contains(errMsg, "exec.go") ||
		!strings.Contains(errMsg, "task.go") ||
		!strings.Contains(errMsg, "Use --file to disambiguate") {
		t.Fatalf("actionable error missing candidates:\n%s", errMsg)
	}

	// 2. Disambiguate with --file pkg/runner/exec.go
	resRunner, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "Execute",
		File:          "pkg/runner/exec.go",
		Budget:        2000,
	})
	if err != nil {
		t.Fatalf("unexpected error with --file: %v", err)
	}
	if len(resRunner.Pack.Items) == 0 {
		t.Fatal("expected items in pack")
	}
	if !strings.Contains(resRunner.Pack.Items[0].FilePath, "runner/exec.go") {
		t.Errorf("expected runner/exec.go as anchor, got %s", resRunner.Pack.Items[0].FilePath)
	}
	if !strings.Contains(resRunner.Pack.Items[0].Reason, "Targeted symbol anchor") {
		t.Errorf("expected anchor reason, got %s", resRunner.Pack.Items[0].Reason)
	}

	// 3. Disambiguate with --file pkg/task/task.go
	resTask, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "Execute",
		File:          "pkg/task/task.go",
		Budget:        2000,
	})
	if err != nil {
		t.Fatalf("unexpected error for task: %v", err)
	}
	if !strings.Contains(resTask.Pack.Items[0].FilePath, "task/task.go") {
		t.Errorf("expected task/task.go as anchor, got %s", resTask.Pack.Items[0].FilePath)
	}
}

func TestService_IncomingAndOutgoingRelationships(t *testing.T) {
	tempDir := t.TempDir()

	// Anchor: ProcessJob calls ValidatePayload and DispatchNotification
	jobCode := `package job

import "fmt"

// ProcessJob coordinates worker execution.
func ProcessJob(jobID string) error {
	fmt.Println("Processing job", jobID)
	if err := ValidatePayload(jobID); err != nil {
		return err
	}
	DispatchNotification(jobID)
	return nil
}
`
	validateCode := `package job

// ValidatePayload ensures payload integrity.
func ValidatePayload(id string) error {
	return nil
}
`
	notifyCode := `package job

// DispatchNotification notifies subscribers.
func DispatchNotification(id string) {
}
`
	callerCode := `package main

import "pkg/job"

func Run() {
	_ = job.ProcessJob("123")
}
`
	testCode := `package job

import "testing"

func TestProcessJob(t *testing.T) {
	_ = ProcessJob("test-1")
}
`

	_ = os.WriteFile(filepath.Join(tempDir, "job.go"), []byte(jobCode), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "validate.go"), []byte(validateCode), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "notify.go"), []byte(notifyCode), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(callerCode), 0644)
	_ = os.WriteFile(filepath.Join(tempDir, "job_test.go"), []byte(testCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	svc := tzroctx.NewContextService(s, nil)
	ctx := stdctx.Background()

	res, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "ProcessJob",
		Budget:        3000,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	pack := res.Pack
	if len(pack.Items) < 3 {
		t.Fatalf("expected anchor + callees/callers, got %d items", len(pack.Items))
	}

	// 1. Verify anchor
	anchorItem := pack.Items[0]
	if anchorItem.SymbolName != "ProcessJob" || anchorItem.Relationship != "anchor" {
		t.Errorf("anchor item mismatch: %+v", anchorItem)
	}

	// 2. Verify outgoing callees
	hasValidateCallee := false
	hasNotifyCallee := false
	for _, it := range pack.Items {
		if it.Relationship == "callee" {
			if it.Direction != "outgoing" {
				t.Errorf("callee %s has wrong direction %s", it.SymbolName, it.Direction)
			}
			if it.Precision != "precise" {
				t.Errorf("callee %s has wrong precision %s", it.SymbolName, it.Precision)
			}
			if it.SymbolName == "ValidatePayload" {
				hasValidateCallee = true
			}
			if it.SymbolName == "DispatchNotification" {
				hasNotifyCallee = true
			}
		}
	}
	if !hasValidateCallee || !hasNotifyCallee {
		t.Errorf("missing callees: ValidatePayload=%v, DispatchNotification=%v", hasValidateCallee, hasNotifyCallee)
	}

	// 3. Verify incoming callers and tests
	hasCaller := false
	hasTest := false
	for _, it := range pack.Items {
		if it.Direction == "incoming" {
			if it.Relationship == "caller" {
				hasCaller = true
			}
			if it.Relationship == "test" {
				hasTest = true
			}
		}
	}
	if !hasCaller || !hasTest {
		t.Errorf("missing incoming references: caller=%v, test=%v", hasCaller, hasTest)
	}

	// 4. Verify formatted markdown contains impact directions
	md := res.Formatted
	if !strings.Contains(md, "outgoing") || !strings.Contains(md, "callee") {
		t.Errorf("markdown missing outgoing callee impact tags:\n%s", md)
	}
}

func TestService_BudgetPreservationAndDegradation(t *testing.T) {
	tempDir := t.TempDir()

	largeCode := `package service

import "fmt"

// HeavyWorker performs substantial work with large body.
func HeavyWorker(n int) int {
` + strings.Repeat("\tfmt.Println(\"heavy computation loop step\")\n", 40) + `
	return n * 42
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "service.go"), []byte(largeCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	svc := tzroctx.NewContextService(s, nil)
	ctx := stdctx.Background()

	// Budget of 20 tokens — elided span (~28 tokens) exceeds 20, but signature stub (~12 tokens) fits
	res, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HeavyWorker",
		Budget:        20,
	})
	if err != nil {
		t.Fatalf("Execute failed on degraded budget: %v", err)
	}
	if len(res.Pack.Items) == 0 {
		t.Fatal("expected anchor preserved via degradation")
	}
	anchor := res.Pack.Items[0]
	if !strings.Contains(anchor.Content, "/* body omitted to fit budget */") {
		t.Errorf("expected degraded signature stub, got:\n%s", anchor.Content)
	}

	// Tiny budget of 1 token — cannot fit even signature stub -> ErrBudgetTooSmall
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HeavyWorker",
		Budget:        1,
	})
	if err == nil || !errors.Is(err, tokenizer.ErrBudgetTooSmall) {
		t.Fatalf("expected ErrBudgetTooSmall for 1-token budget, got %v", err)
	}
}

func TestService_FormatAndAtomicFileOutput(t *testing.T) {
	tempDir := t.TempDir()

	code := `package api

// HealthCheck reports service status.
func HealthCheck() string {
	return "ok"
}
`
	_ = os.WriteFile(filepath.Join(tempDir, "api.go"), []byte(code), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	svc := tzroctx.NewContextService(s, nil)
	ctx := stdctx.Background()

	// 1. JSON format
	resJSON, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HealthCheck",
		Format:        "json",
		Budget:        1000,
	})
	if err != nil {
		t.Fatalf("JSON execute failed: %v", err)
	}
	var parsed tzroctx.ContextPack
	if err := json.Unmarshal([]byte(resJSON.Formatted), &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, resJSON.Formatted)
	}
	if parsed.Tokenizer == nil || parsed.Tokenizer.Mode != tokenizer.ModeExact {
		t.Errorf("expected exact tokenizer metadata in JSON pack")
	}

	// 2. Atomic file output
	outFile := filepath.Join(tempDir, "out", "pack.md")
	resFile, err := svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HealthCheck",
		Format:        "markdown",
		Output:        outFile,
		Budget:        1000,
	})
	if err != nil {
		t.Fatalf("file output failed: %v", err)
	}
	if resFile.OutputPath != outFile {
		t.Errorf("OutputPath mismatch: %s != %s", resFile.OutputPath, outFile)
	}

	content, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("failed to read written output file: %v", err)
	}
	if !strings.Contains(string(content), "HealthCheck") {
		t.Errorf("output file does not contain expected pack content:\n%s", string(content))
	}

	// 3. Attempting to write to existing file without force must fail
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HealthCheck",
		Output:        outFile,
		Force:         false,
		Budget:        1000,
	})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected error on existing file without --force, got: %v", err)
	}

	// 4. Overwrite with --force must succeed
	_, err = svc.Execute(ctx, tzroctx.ContextRequest{
		WorkspaceRoot: tempDir,
		Symbol:        "HealthCheck",
		Output:        outFile,
		Force:         true,
		Budget:        1000,
	})
	if err != nil {
		t.Fatalf("expected success with --force, got: %v", err)
	}
}
