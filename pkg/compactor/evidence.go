package compactor

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"tzro/pkg/dlp"
	"tzro/pkg/store"
)

// Diagnostic severities.
const (
	SeverityError   = "error"
	SeverityWarning = "warning"
	SeveritySkip    = "skip"
)

// Exit source confidence tiers.
const (
	ExitSourceObserved = "observed"
	ExitSourceInferred = "inferred"
	ExitSourceUnknown  = "unknown"
)

// Compaction status constants.
const (
	StatusCompacted   = "compacted"
	StatusFallbackRaw = "fallback_raw"
)

// Diagnostic represents an individually surfaced failure, warning, or skip.
type Diagnostic struct {
	Severity          string  `json:"severity"`
	ID                string  `json:"id"`
	Message           string  `json:"message"`
	Location          string  `json:"location,omitempty"`
	Duration          float64 `json:"duration,omitempty"`
	Output            string  `json:"output,omitempty"`
	OmittedSpanNotice string  `json:"omitted_span_notice,omitempty"`
	ArtifactRef       string  `json:"artifact_ref,omitempty"`
	ExpandHash        string  `json:"expand_hash,omitempty"`
}

// CompactedEvidence is the structured result type emitted by tzro compact.
type CompactedEvidence struct {
	Diagnostics      []Diagnostic `json:"diagnostics"`
	ExitCode         int          `json:"exit_code"`
	ExitSource       string       `json:"exit_source"` // observed | inferred | unknown
	CompactionStatus string       `json:"compaction_status"`
	Reason           string       `json:"reason,omitempty"`
	PrivacyApplied   bool         `json:"privacy_applied"`
	RawOutput        string       `json:"raw_output,omitempty"`
	ArtifactRef      string       `json:"artifact_ref,omitempty"`
	Format           string       `json:"format,omitempty"`
}

// FormatMarkdown returns an agent-friendly markdown representation of the evidence.
func (ce *CompactedEvidence) FormatMarkdown() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Compaction Evidence (Exit: %d [%s] | Status: %s)\n\n", ce.ExitCode, ce.ExitSource, ce.CompactionStatus))

	if ce.ArtifactRef != "" {
		sb.WriteString(fmt.Sprintf("> Full original output stored as artifact `%s` (retrieve via `tzro expand %s`)\n\n", ce.ArtifactRef, ce.ArtifactRef))
	}

	if len(ce.Diagnostics) == 0 {
		sb.WriteString("✓ Zero failures or warnings detected.\n")
		if ce.RawOutput != "" && ce.CompactionStatus == StatusFallbackRaw {
			sb.WriteString("\n```\n" + ce.RawOutput + "\n```\n")
		}
		return sb.String()
	}

	sb.WriteString(fmt.Sprintf("Found %d diagnostic(s):\n\n", len(ce.Diagnostics)))
	for i, d := range ce.Diagnostics {
		loc := ""
		if d.Location != "" {
			loc = fmt.Sprintf(" at `%s`", d.Location)
		}
		sb.WriteString(fmt.Sprintf("### %d. [%s] `%s`%s\n", i+1, strings.ToUpper(d.Severity), d.ID, loc))
		if d.Duration > 0 {
			sb.WriteString(fmt.Sprintf("- **Duration:** %.3fs\n", d.Duration))
		}
		if d.Message != "" {
			sb.WriteString(fmt.Sprintf("- **Message:** %s\n", d.Message))
		}
		if d.Output != "" {
			sb.WriteString("```\n" + d.Output + "\n```\n")
		}
		if d.OmittedSpanNotice != "" {
			sb.WriteString(d.OmittedSpanNotice + "\n")
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// capOutput limits output to 10 lines, storing full body in Content-Hash Store if s is provided.
func capOutput(text string, s *store.Store) (inline string, notice string, hash string) {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	if len(lines) <= 10 {
		return text, "", ""
	}

	inline = strings.Join(lines[:10], "\n")
	elidedCount := len(lines) - 10

	if s != nil {
		h, err := s.PutBlob("compact_diff", 1, len(lines), text)
		if err == nil {
			notice = fmt.Sprintf("// [%d lines elided: run `tzro expand #%s` to view full output]", elidedCount, h)
			return inline, notice, h
		}
	}

	notice = fmt.Sprintf("// [%d lines elided: full original retained in artifact]", elidedCount)
	return inline, notice, ""
}

// CompactEvidence parses input against supported formats (JUnit, JSON, GNU) or falls back to plain text.
func CompactEvidence(input, workspace string, s *store.Store, policy *dlp.PolicyEngine, observedExitCode *int) (*CompactedEvidence, error) {
	trimmed := strings.TrimSpace(input)
	var artifactID string

	// Store full uncompressed original in Content-Hash Store
	if s != nil && trimmed != "" {
		id, err := s.PutArtifact(&store.Artifact{
			Type:             "log",
			Workspace:        workspace,
			TransformVersion: "v2.0",
			Body:             input,
		})
		if err == nil {
			artifactID = id
		}
	}

	// Determine initial exit code and exit source
	exitCode := 0
	exitSource := ExitSourceUnknown
	if observedExitCode != nil {
		exitCode = *observedExitCode
		exitSource = ExitSourceObserved
	}

	res := &CompactedEvidence{
		ExitCode:         exitCode,
		ExitSource:       exitSource,
		CompactionStatus: StatusCompacted,
		ArtifactRef:      artifactID,
	}

	// 1. Check for JUnit XML
	if strings.HasPrefix(trimmed, "<?xml") || (strings.Contains(trimmed, "<testsuite") || strings.Contains(trimmed, "<testsuites")) {
		if diags, inferredCode, ok := parseJUnit(trimmed, s, artifactID); ok {
			res.Diagnostics = diags
			res.Format = "junit"
			if exitSource != ExitSourceObserved {
				res.ExitCode = inferredCode
				res.ExitSource = ExitSourceInferred
			}
			applyPrivacy(res, policy)
			return res, nil
		}
	}

	// 2. Check for Go test JSON or Jest JSON
	if strings.HasPrefix(trimmed, "{") {
		if diags, inferredCode, ok := parseJSONLines(trimmed, s, artifactID); ok {
			res.Diagnostics = diags
			res.Format = "json"
			if exitSource != ExitSourceObserved {
				res.ExitCode = inferredCode
				res.ExitSource = ExitSourceInferred
			}
			applyPrivacy(res, policy)
			return res, nil
		}
	}

	// 3. Check for GNU / compiler diagnostics
	if diags, ok := parseGNUDiagnostics(trimmed, s, artifactID); ok && len(diags) > 0 {
		res.Diagnostics = diags
		res.Format = "gnu"
		if exitSource != ExitSourceObserved {
			hasError := false
			for _, d := range diags {
				if d.Severity == SeverityError {
					hasError = true
					break
				}
			}
			if hasError {
				res.ExitCode = 1
				res.ExitSource = ExitSourceInferred
			}
		}
		applyPrivacy(res, policy)
		return res, nil
	}

	// 4. Fallback: Plain text with StackTraceElider
	res.Format = "text"
	res.CompactionStatus = StatusFallbackRaw
	res.Reason = "format_unsupported"
	res.RawOutput = StackTraceElider(trimmed)

	applyPrivacy(res, policy)
	return res, nil
}

// applyPrivacy ensures DLP redaction runs on both diagnostics and raw output fallback.
func applyPrivacy(res *CompactedEvidence, policy *dlp.PolicyEngine) {
	if policy == nil {
		return
	}

	redactor := dlp.NewRedactor()

	// Redact diagnostics
	for i := range res.Diagnostics {
		if res.Diagnostics[i].Message != "" {
			redacted, m := redactor.Redact(res.Diagnostics[i].Message)
			if len(m) > 0 {
				res.Diagnostics[i].Message = redacted
				res.PrivacyApplied = true
			}
		}
		if res.Diagnostics[i].Output != "" {
			redacted, m := redactor.Redact(res.Diagnostics[i].Output)
			if len(m) > 0 {
				res.Diagnostics[i].Output = redacted
				res.PrivacyApplied = true
			}
		}
	}

	// Redact raw output fallback
	if res.RawOutput != "" {
		eval := policy.EvaluateContent(res.RawOutput)
		if !eval.Allowed {
			res.PrivacyApplied = true
		}
		redacted, m := redactor.Redact(res.RawOutput)
		if len(m) > 0 {
			res.RawOutput = redacted
			res.PrivacyApplied = true
		}
	}
}

// JUnit XML structures
type jUnitTestSuites struct {
	XMLName  xml.Name         `xml:"testsuites"`
	Failures int              `xml:"failures,attr"`
	Errors   int              `xml:"errors,attr"`
	Time     float64          `xml:"time,attr"`
	Suites   []jUnitTestSuite `xml:"testsuite"`
}

type jUnitTestSuite struct {
	XMLName   xml.Name         `xml:"testsuite"`
	Name      string           `xml:"name,attr"`
	Failures  int              `xml:"failures,attr"`
	Errors    int              `xml:"errors,attr"`
	Time      float64          `xml:"time,attr"`
	TestCases []jTestCase      `xml:"testcase"`
	Suites    []jUnitTestSuite `xml:"testsuite"`
}

type jTestCase struct {
	XMLName   xml.Name `xml:"testcase"`
	Name      string   `xml:"name,attr"`
	ClassName string   `xml:"classname,attr"`
	Time      float64  `xml:"time,attr"`
	Failure   *jItem   `xml:"failure"`
	Error     *jItem   `xml:"error"`
	Skipped   *jItem   `xml:"skipped"`
}

type jItem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Content string `xml:",innerxml"`
}

var gnuLocationRe = regexp.MustCompile(`([a-zA-Z0-9_\-\./\\]+\.[a-zA-Z0-9]+):(\d+)(?::(\d+))?`)

func extractLocation(text string) string {
	match := gnuLocationRe.FindString(text)
	return match
}

func parseJUnit(input string, s *store.Store, artifactID string) ([]Diagnostic, int, bool) {
	var suites jUnitTestSuites
	var testSuiteList []jUnitTestSuite

	if err := xml.Unmarshal([]byte(input), &suites); err == nil && (len(suites.Suites) > 0 || suites.Failures > 0 || suites.Errors > 0) {
		testSuiteList = suites.Suites
	} else {
		var single jUnitTestSuite
		if err := xml.Unmarshal([]byte(input), &single); err == nil && len(single.TestCases) > 0 {
			testSuiteList = []jUnitTestSuite{single}
		} else {
			return nil, 0, false
		}
	}

	var diags []Diagnostic
	totalFailures := 0

	var collectFromSuite func(suite jUnitTestSuite)
	collectFromSuite = func(suite jUnitTestSuite) {
		for _, tc := range suite.TestCases {
			id := tc.Name
			if tc.ClassName != "" {
				id = tc.ClassName + "." + tc.Name
			}

			if tc.Failure != nil {
				totalFailures++
				msg := tc.Failure.Message
				rawOutput := tc.Failure.Content
				if msg == "" && rawOutput != "" {
					msg = strings.Split(strings.TrimSpace(rawOutput), "\n")[0]
				}
				inline, notice, h := capOutput(rawOutput, s)
				loc := extractLocation(msg + " " + rawOutput)

				diags = append(diags, Diagnostic{
					Severity:          SeverityError,
					ID:                id,
					Message:           msg,
					Location:          loc,
					Duration:          tc.Time,
					Output:            inline,
					OmittedSpanNotice: notice,
					ArtifactRef:       artifactID,
					ExpandHash:        h,
				})
			} else if tc.Error != nil {
				totalFailures++
				msg := tc.Error.Message
				rawOutput := tc.Error.Content
				if msg == "" && rawOutput != "" {
					msg = strings.Split(strings.TrimSpace(rawOutput), "\n")[0]
				}
				inline, notice, h := capOutput(rawOutput, s)
				loc := extractLocation(msg + " " + rawOutput)

				diags = append(diags, Diagnostic{
					Severity:          SeverityError,
					ID:                id,
					Message:           msg,
					Location:          loc,
					Duration:          tc.Time,
					Output:            inline,
					OmittedSpanNotice: notice,
					ArtifactRef:       artifactID,
					ExpandHash:        h,
				})
			} else if tc.Skipped != nil {
				diags = append(diags, Diagnostic{
					Severity:    SeveritySkip,
					ID:          id,
					Message:     tc.Skipped.Message,
					Duration:    tc.Time,
					ArtifactRef: artifactID,
				})
			}
		}
		for _, sub := range suite.Suites {
			collectFromSuite(sub)
		}
	}

	for _, suite := range testSuiteList {
		collectFromSuite(suite)
	}

	// Sort diagnostics: errors before warnings before skips
	for i := 0; i < len(diags)-1; i++ {
		for j := i + 1; j < len(diags); j++ {
			sevRank := func(sev string) int {
				switch sev {
				case SeverityError:
					return 0
				case SeverityWarning:
					return 1
				case SeveritySkip:
					return 2
				default:
					return 3
				}
			}
			if sevRank(diags[j].Severity) < sevRank(diags[i].Severity) {
				diags[i], diags[j] = diags[j], diags[i]
			}
		}
	}

	inferredExit := 0
	if totalFailures > 0 {
		inferredExit = 1
	}

	return diags, inferredExit, true
}

type goTestEvent struct {
	Time    time.Time `json:"Time"`
	Action  string    `json:"Action"`
	Package string    `json:"Package"`
	Test    string    `json:"Test"`
	Elapsed float64   `json:"Elapsed"`
	Output  string    `json:"Output"`
}

func parseJSONLines(input string, s *store.Store, artifactID string) ([]Diagnostic, int, bool) {
	scanner := bufio.NewScanner(strings.NewReader(input))
	var diags []Diagnostic
	testOutputs := make(map[string]*strings.Builder)
	inferredExit := 0
	validEvents := 0

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}

		var ev goTestEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		validEvents++

		if ev.Test != "" {
			if _, ok := testOutputs[ev.Test]; !ok {
				testOutputs[ev.Test] = &strings.Builder{}
			}
			if ev.Output != "" {
				testOutputs[ev.Test].WriteString(ev.Output)
			}
		}

		if ev.Action == "fail" {
			inferredExit = 1
			if ev.Test != "" {
				rawOutput := ""
				if b, ok := testOutputs[ev.Test]; ok {
					rawOutput = b.String()
				}
				inline, notice, h := capOutput(rawOutput, s)
				loc := extractLocation(rawOutput)
				msg := fmt.Sprintf("Test %s failed in package %s", ev.Test, ev.Package)

				diags = append(diags, Diagnostic{
					Severity:          SeverityError,
					ID:                ev.Test,
					Message:           msg,
					Location:          loc,
					Duration:          ev.Elapsed,
					Output:            inline,
					OmittedSpanNotice: notice,
					ArtifactRef:       artifactID,
					ExpandHash:        h,
				})
			}
		}
	}

	if validEvents == 0 {
		return nil, 0, false
	}

	return diags, inferredExit, true
}

var gnuDiagRe = regexp.MustCompile(`^([a-zA-Z0-9_\-\./\\]+\.[a-zA-Z0-9]+):(\d+)(?::(\d+))?:\s*(error|warning|fatal error):\s*(.*)$`)

func parseGNUDiagnostics(input string, s *store.Store, artifactID string) ([]Diagnostic, bool) {
	scanner := bufio.NewScanner(strings.NewReader(input))
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 10*1024*1024)

	var diags []Diagnostic
	for scanner.Scan() {
		line := scanner.Text()
		// Fast rejection: must contain colon and error/warning keyword
		if !strings.Contains(line, ":") || (!strings.Contains(line, "error") && !strings.Contains(line, "warning")) {
			continue
		}

		m := gnuDiagRe.FindStringSubmatch(line)
		if len(m) == 0 {
			continue
		}

		filePath := m[1]
		lineStr := m[2]
		colStr := m[3]
		sevStr := strings.ToLower(m[4])
		msg := m[5]

		location := fmt.Sprintf("%s:%s", filePath, lineStr)
		if colStr != "" {
			location += ":" + colStr
		}

		severity := SeverityWarning
		if strings.Contains(sevStr, "error") {
			severity = SeverityError
		}

		lineNum, _ := strconv.Atoi(lineStr)
		id := fmt.Sprintf("%s:%d", filePath, lineNum)

		diags = append(diags, Diagnostic{
			Severity:    severity,
			ID:          id,
			Message:     msg,
			Location:    location,
			ArtifactRef: artifactID,
		})
	}

	if len(diags) == 0 {
		return nil, false
	}

	return diags, true
}

// RunAndCompact executes command in wrapper mode, capturing stdout/stderr and exact exit code.
func RunAndCompact(command, workspace string, s *store.Store, policy *dlp.PolicyEngine) (*CompactedEvidence, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}

	outputBytes, err := cmd.CombinedOutput()
	outputStr := string(outputBytes)

	observedExit := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			observedExit = exitErr.ExitCode()
		} else {
			observedExit = 1
		}
	}

	return CompactEvidence(outputStr, workspace, s, policy, &observedExit)
}
