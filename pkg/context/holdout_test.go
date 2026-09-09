package context

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"tzro/pkg/probe"
	"tzro/pkg/store"
)


// HoldoutBenchmarkResult holds measured metrics for context retrieval vs probe baseline.
type HoldoutBenchmarkResult struct {
	Query             string
	TargetRecall      float64
	ContextPackRecall float64
	ProbeRecall       float64
	ContextTokens     int
	ProbeMatches      int
	ColdLatencyMs     int64
	WarmLatencyMs     int64
	MemoryAllocMB     uint64
}

func TestContextPack_HoldoutQualityEvaluation(t *testing.T) {
	fixtureDir := t.TempDir()

	// 1. Create a realistic fixture codebase with Go and TS modules
	// Mod 1: Token Validator
	authDir := filepath.Join(fixtureDir, "pkg", "auth")
	_ = os.MkdirAll(authDir, 0755)
	_ = os.WriteFile(filepath.Join(authDir, "validator.go"), []byte(`package auth
func ValidateUserToken(token string) bool { return len(token) > 20 }
func RevokeUserToken(token string) error { return nil }
`), 0644)
	_ = os.WriteFile(filepath.Join(authDir, "validator_test.go"), []byte(`package auth
import "testing"
func TestValidateUserToken(t *testing.T) { ValidateUserToken("abc") }
`), 0644)

	// Mod 2: User Service (caller)
	userDir := filepath.Join(fixtureDir, "pkg", "user")
	_ = os.MkdirAll(userDir, 0755)
	_ = os.WriteFile(filepath.Join(userDir, "service.go"), []byte(`package user
import "tzro/fixture/auth"
type Service struct{}
func (s *Service) Login(t string) bool { return auth.ValidateUserToken(t) }
`), 0644)

	// Mod 3: Ignored vendor / node_modules
	vendorDir := filepath.Join(fixtureDir, "vendor", "fakepkg")
	_ = os.MkdirAll(vendorDir, 0755)
	_ = os.WriteFile(filepath.Join(vendorDir, "dummy.go"), []byte(`package fakepkg
func ValidateUserToken() {}
`), 0644)

	// Ingest into store
	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	assembler := NewAssembler(s)

	query := "ValidateUserToken validator service"
	targetExpectedFiles := []string{
		"pkg/auth/validator.go",
		"pkg/auth/validator_test.go",
		"pkg/user/service.go",
	}

	// Cold run measurement
	startCold := time.Now()
	coldPack, err := assembler.Assemble(fixtureDir, query, 3000)
	coldLatency := time.Since(startCold).Milliseconds()
	if err != nil {
		t.Fatalf("Cold assemble failed: %v", err)
	}

	// Warm run measurement
	startWarm := time.Now()
	warmPack, err := assembler.Assemble(fixtureDir, query, 3000)
	warmLatency := time.Since(startWarm).Milliseconds()
	if err != nil {
		t.Fatalf("Warm assemble failed: %v", err)
	}

	// Baseline Probe run
	probeReport, err := probe.Probe(fixtureDir, "ValidateUserToken", 20, s)
	if err != nil {
		t.Fatalf("Probe failed: %v", err)
	}

	// Evaluate Recall against target files
	checkRecall := func(pack *ContextPack) float64 {
		found := 0
		for _, exp := range targetExpectedFiles {
			for _, item := range pack.Items {
				if strings.Contains(item.FilePath, exp) {
					found++
					break
				}
			}
		}
		return float64(found) / float64(len(targetExpectedFiles))
	}

	coldRecall := checkRecall(coldPack)
	warmRecall := checkRecall(warmPack)

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	benchResult := HoldoutBenchmarkResult{
		Query:             query,
		TargetRecall:      1.0,
		ContextPackRecall: warmRecall,
		ProbeRecall:       float64(len(probeReport.Matches)) / float64(len(targetExpectedFiles)),
		ContextTokens:     warmPack.UsedTokens,
		ProbeMatches:      len(probeReport.Matches),
		ColdLatencyMs:     coldLatency,
		WarmLatencyMs:     warmLatency,
		MemoryAllocMB:     m.Alloc / 1024 / 1024,
	}

	t.Logf("Holdout Benchmark Summary: Recall=%.2f Cold=%dms Warm=%dms RSS=%dMB Tokens=%d",
		benchResult.ContextPackRecall, benchResult.ColdLatencyMs, benchResult.WarmLatencyMs, benchResult.MemoryAllocMB, benchResult.ContextTokens)

	if coldRecall < 0.6 {
		t.Errorf("expected cold recall >= 0.6, got %.2f", coldRecall)
	}
	if warmRecall < 0.6 {
		t.Errorf("expected warm recall >= 0.6, got %.2f", warmRecall)
	}

	// Verify vendor/ was NOT included
	for _, item := range warmPack.Items {
		if strings.Contains(item.FilePath, "vendor") {
			t.Errorf("vendor file leaked into context pack: %s", item.FilePath)
		}
	}

	// Verify memory stays well below lightweight 50MB constraint
	if benchResult.MemoryAllocMB > 50 {
		t.Errorf("resident memory footprint %d MB exceeded lightweight threshold (50MB)", benchResult.MemoryAllocMB)
	}
}
