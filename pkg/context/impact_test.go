package context_test

import (
	"os"
	"path/filepath"
	"testing"
	tzroctx "tzro/pkg/context"
	"tzro/pkg/store"
)

func TestImpactAnalyzer_SymbolReferences(t *testing.T) {
	tempDir := t.TempDir()

	// Create test codebase:
	// interface.go defines Greeter
	// impl.go implements Greeter and calls Greeter
	// app.go calls Greeter
	// app_test.go tests Greeter
	// config.yaml references Greeter package

	interfaceFile := filepath.Join(tempDir, "greeter.go")
	interfaceCode := `package hello

type Greeter interface {
	Greet(name string) string
}
`
	_ = os.WriteFile(interfaceFile, []byte(interfaceCode), 0644)

	implFile := filepath.Join(tempDir, "impl.go")
	implCode := `package hello

type EnglishGreeter struct{}

func (g EnglishGreeter) Greet(name string) string {
	return "Hello " + name
}
`
	_ = os.WriteFile(implFile, []byte(implCode), 0644)

	consumerFile := filepath.Join(tempDir, "consumer.go")
	consumerCode := `package hello

func RunGreeting(g Greeter) string {
	return g.Greet("World")
}
`
	_ = os.WriteFile(consumerFile, []byte(consumerCode), 0644)

	testFile := filepath.Join(tempDir, "greeter_test.go")
	testCode := `package hello

import "testing"

func TestGreeter(t *testing.T) {
	g := EnglishGreeter{}
	if g.Greet("Alice") != "Hello Alice" {
		t.Fail()
	}
}
`
	_ = os.WriteFile(testFile, []byte(testCode), 0644)

	configFile := filepath.Join(tempDir, "config.yaml")
	configCode := `service:
  handler: hello.Greeter
`
	_ = os.WriteFile(configFile, []byte(configCode), 0644)

	s, err := store.OpenStore(":memory:")
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}
	defer s.Close()

	analyzer := tzroctx.NewImpactAnalyzer(s, nil)

	// Analyze impact of Greeter symbol
	pack, err := analyzer.AnalyzeSymbol(tempDir, "Greeter", 4000, false)
	if err != nil {
		t.Fatalf("AnalyzeSymbol failed: %v", err)
	}

	if pack == nil {
		t.Fatalf("expected non-nil pack")
	}

	// Verify CoverageReport
	cov := pack.Coverage
	if cov == nil {
		t.Fatalf("expected CoverageReport on ContextPack")
	}
	if cov.TotalCandidates < 3 {
		t.Errorf("expected at least 3 candidates, got %d", cov.TotalCandidates)
	}

	// Verify relationship types and precision tiers
	foundConsumer := false
	foundTest := false
	foundConfig := false

	for _, item := range pack.Items {
		if item.Precision == "" {
			t.Errorf("item %s missing Precision tier", item.FilePath)
		}
		if item.Relationship == "" {
			t.Errorf("item %s missing Relationship type", item.FilePath)
		}
		if item.StartLine <= 0 {
			t.Errorf("item %s missing StartLine", item.FilePath)
		}

		if item.FilePath == "consumer.go" && item.Relationship == "caller" {
			foundConsumer = true
			if item.Precision != "precise" && item.Precision != "syntactic" {
				t.Errorf("unexpected precision for consumer: %s", item.Precision)
			}
		}
		if item.FilePath == "greeter_test.go" && item.Relationship == "test" {
			foundTest = true
		}
		if item.FilePath == "config.yaml" && item.Relationship == "config" {
			foundConfig = true
		}
	}

	if !foundConsumer {
		t.Errorf("consumer.go caller reference not found in pack")
	}
	if !foundTest {
		t.Errorf("greeter_test.go test reference not found in pack")
	}
	if !foundConfig {
		t.Errorf("config.yaml config reference not found in pack")
	}
}

func TestImpactAnalyzer_GeneratedCodeExclusion(t *testing.T) {
	tempDir := t.TempDir()

	mainFile := filepath.Join(tempDir, "main.go")
	_ = os.WriteFile(mainFile, []byte("package main\nfunc Target() {}\n"), 0644)

	genFile := filepath.Join(tempDir, "main.pb.go")
	_ = os.WriteFile(genFile, []byte("package main\nfunc CallTarget() { Target() }\n"), 0644)

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	// By default, generated code should be excluded
	packDefault, err := analyzer.AnalyzeSymbol(tempDir, "Target", 4000, false)
	if err != nil {
		t.Fatalf("AnalyzeSymbol failed: %v", err)
	}
	for _, it := range packDefault.Items {
		if it.FilePath == "main.pb.go" {
			t.Errorf("main.pb.go should be excluded by default")
		}
	}

	// Opt-in via includeGenerated = true
	packOptIn, err := analyzer.AnalyzeSymbol(tempDir, "Target", 4000, true)
	if err != nil {
		t.Fatalf("AnalyzeSymbol failed: %v", err)
	}
	foundGen := false
	for _, it := range packOptIn.Items {
		if it.FilePath == "main.pb.go" {
			foundGen = true
			break
		}
	}
	if !foundGen {
		t.Errorf("expected main.pb.go to be included when includeGenerated is true")
	}
}

func TestImpactAnalyzer_BudgetTruncationManifest(t *testing.T) {
	tempDir := t.TempDir()

	_ = os.WriteFile(filepath.Join(tempDir, "def.go"), []byte("package test\nfunc Root() {}\n"), 0644)

	// Create 5 consumer files
	for i := 1; i <= 5; i++ {
		name := filepath.Join(tempDir, string(rune('a'+i))+".go")
		content := "package test\nfunc Caller" + string(rune('a'+i)) + "() {\n\tRoot()\n\t// lots of padding content to consume tokens\n}\n"
		_ = os.WriteFile(name, []byte(content), 0644)
	}

	analyzer := tzroctx.NewImpactAnalyzer(nil, nil)

	// Run with very small budget (10 tokens) -> forces truncation
	pack, err := analyzer.AnalyzeSymbol(tempDir, "Root", 10, false)
	if err != nil {
		t.Fatalf("AnalyzeSymbol failed: %v", err)
	}

	if pack.Coverage == nil {
		t.Fatalf("expected CoverageReport")
	}
	if pack.Coverage.TruncatedCount == 0 {
		t.Errorf("expected candidates to be truncated under tiny budget")
	}
	if len(pack.Coverage.TruncatedManifest) != pack.Coverage.TruncatedCount {
		t.Errorf("TruncatedManifest length %d != TruncatedCount %d",
			len(pack.Coverage.TruncatedManifest), pack.Coverage.TruncatedCount)
	}
}
