package compactor_test

import (
	"strings"
	"testing"
	"tzro/pkg/compactor"
	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

func TestCompactor_JUnitXML(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	junitXML := `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="3" failures="1" errors="0" skipped="1" time="1.234">
  <testsuite name="TestSuite1" tests="3" failures="1" skipped="1" time="1.234">
    <testcase name="TestPass" classname="pkg.Test1" time="0.100"/>
    <testcase name="TestSkip" classname="pkg.Test1" time="0.050">
      <skipped message="reason for skip"/>
    </testcase>
    <testcase name="TestFail" classname="pkg.Test1" time="0.500">
      <failure message="assertion failed at math_test.go:42: expected 4 got 5" type="AssertionError">
math_test.go:42: expected 4 got 5
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
line 10
line 11
line 12
      </failure>
    </testcase>
  </testsuite>
</testsuites>`

	res, err := compactor.CompactEvidence(junitXML, "test-ws", s, nil, nil)
	if err != nil {
		t.Fatalf("CompactEvidence failed: %v", err)
	}

	if res.ExitSource != compactor.ExitSourceInferred {
		t.Errorf("expected ExitSource inferred, got %s", res.ExitSource)
	}
	if res.ExitCode == 0 {
		t.Errorf("expected non-zero exit code inferred from JUnit failure")
	}

	// Must surface failure AND skip/warning
	if len(res.Diagnostics) != 2 {
		t.Fatalf("expected 2 diagnostics (1 fail, 1 skip), got %d", len(res.Diagnostics))
	}

	failDiag := res.Diagnostics[0]
	if failDiag.Severity != compactor.SeverityError {
		t.Errorf("expected SeverityError, got %s", failDiag.Severity)
	}
	if failDiag.ID != "pkg.Test1.TestFail" && failDiag.ID != "TestFail" {
		t.Errorf("unexpected ID: %s", failDiag.ID)
	}
	if failDiag.Duration != 0.500 {
		t.Errorf("expected numeric duration 0.500, got %f", failDiag.Duration)
	}
	if failDiag.Location != "math_test.go:42" {
		t.Errorf("expected Location math_test.go:42, got %s", failDiag.Location)
	}

	// Check 10-line inline cap with expand-hash overflow
	lines := strings.Split(strings.TrimSpace(failDiag.Output), "\n")
	if len(lines) > 10 {
		t.Errorf("expected at most 10 inline lines, got %d", len(lines))
	}
	if failDiag.OmittedSpanNotice == "" {
		t.Errorf("expected non-empty OmittedSpanNotice when output > 10 lines")
	}
	if failDiag.ArtifactRef == "" {
		t.Errorf("expected non-empty ArtifactRef")
	}

	// Verify expanding hash from store returns exact content
	if failDiag.ExpandHash != "" {
		blob, err := s.GetBlob(failDiag.ExpandHash)
		if err != nil {
			t.Fatalf("GetBlob failed: %v", err)
		}
		if !strings.Contains(blob.Body, "line 12") {
			t.Errorf("expanded body missing expected lines: %s", blob.Body)
		}
	}
}

func TestCompactor_GoTestJSON(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	goTestJSON := `{"Time":"2026-09-09T20:00:00Z","Action":"run","Package":"tzro/pkg/calc","Test":"TestAdd"}
{"Time":"2026-09-09T20:00:01Z","Action":"output","Package":"tzro/pkg/calc","Test":"TestAdd","Output":"=== RUN   TestAdd\n"}
{"Time":"2026-09-09T20:00:02Z","Action":"output","Package":"tzro/pkg/calc","Test":"TestAdd","Output":"    calc_test.go:25: expected 10 got 20\n"}
{"Time":"2026-09-09T20:00:03Z","Action":"fail","Package":"tzro/pkg/calc","Test":"TestAdd","Elapsed":0.02}
{"Time":"2026-09-09T20:00:04Z","Action":"fail","Package":"tzro/pkg/calc","Elapsed":0.05}
`

	res, err := compactor.CompactEvidence(goTestJSON, "test-ws", s, nil, nil)
	if err != nil {
		t.Fatalf("CompactEvidence failed: %v", err)
	}

	if res.ExitSource != compactor.ExitSourceInferred {
		t.Errorf("expected ExitSource inferred, got %s", res.ExitSource)
	}
	if res.ExitCode != 1 {
		t.Errorf("expected ExitCode 1, got %d", res.ExitCode)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 failure diagnostic, got %d", len(res.Diagnostics))
	}

	d := res.Diagnostics[0]
	if d.ID != "TestAdd" {
		t.Errorf("expected ID TestAdd, got %s", d.ID)
	}
	if d.Location != "calc_test.go:25" {
		t.Errorf("expected Location calc_test.go:25, got %s", d.Location)
	}
	if d.Duration != 0.02 {
		t.Errorf("expected Duration 0.02, got %f", d.Duration)
	}
}

func TestCompactor_GNUDiagnostics(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	gnuOutput := `src/main.c:15:5: error: expected ';' before 'return'
src/utils.c:42:10: warning: unused variable 'temp' [-Wunused-variable]
make: *** [Makefile:10: build] Error 2
`

	res, err := compactor.CompactEvidence(gnuOutput, "test-ws", s, nil, nil)
	if err != nil {
		t.Fatalf("CompactEvidence failed: %v", err)
	}

	if len(res.Diagnostics) != 2 {
		t.Fatalf("expected 2 diagnostics (1 error, 1 warning), got %d", len(res.Diagnostics))
	}

	errDiag := res.Diagnostics[0]
	if errDiag.Severity != compactor.SeverityError || errDiag.Location != "src/main.c:15:5" {
		t.Errorf("unexpected error diagnostic: %+v", errDiag)
	}

	warnDiag := res.Diagnostics[1]
	if warnDiag.Severity != compactor.SeverityWarning || warnDiag.Location != "src/utils.c:42:10" {
		t.Errorf("unexpected warning diagnostic: %+v", warnDiag)
	}
}

func TestCompactor_PrivacyRedactionOnRawFallback(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	wp := &dlp.WorkspacePolicy{
		Version:       "1.0",
		DefaultAction: dlp.ActionAllow,
		Rules: []dlp.PolicyRule{
			{PathPattern: "*", DataClass: "api_key", Action: dlp.ActionRedact},
		},
	}
	policy := dlp.NewPolicyEngine(wp)

	rawWithSecret := "Build failed with secret key sk-ant-api03-secret1234567890abcdef at deploy step"

	res, err := compactor.CompactEvidence(rawWithSecret, "test-ws", s, policy, nil)
	if err != nil {
		t.Fatalf("CompactEvidence failed: %v", err)
	}

	if !res.PrivacyApplied {
		t.Errorf("expected PrivacyApplied to be true")
	}
	if strings.Contains(res.RawOutput, "sk-ant-api03-secret1234567890abcdef") {
		t.Errorf("raw output contains unredacted secret: %s", res.RawOutput)
	}
	if !strings.Contains(res.RawOutput, "[REDACTED") {
		t.Errorf("raw output missing redaction placeholder: %s", res.RawOutput)
	}
}

func TestCompactor_WrapperMode(t *testing.T) {
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	// Run echo command via wrapper mode
	res, err := compactor.RunAndCompact("echo 'src/test.go:10:2: error: syntax error' && exit 1", "test-ws", s, nil)
	if err != nil {
		t.Fatalf("RunAndCompact failed: %v", err)
	}

	if res.ExitSource != compactor.ExitSourceObserved {
		t.Errorf("expected ExitSource observed, got %s", res.ExitSource)
	}
	if res.ExitCode != 1 {
		t.Errorf("expected ExitCode 1, got %d", res.ExitCode)
	}
	if len(res.Diagnostics) != 1 {
		t.Fatalf("expected 1 diagnostic, got %d", len(res.Diagnostics))
	}
}
