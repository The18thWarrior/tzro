package turnreduction

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestGradeFixture_GoFixtures_BrokenCodeFails verifies that the grading
// framework correctly fails all Go fixtures in their broken state.
func TestGradeFixture_GoFixtures_BrokenCodeFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go fixture tests require Unix")
	}

	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	goFixtures := 0
	for _, f := range fixtures {
		if f.Language != "go" {
			continue
		}
		goFixtures++
		t.Run(f.ID, func(t *testing.T) {
			// Copy subject to temp dir.
			ws := t.TempDir()
			copyDir(t, f.SubjectDir, ws)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			passed, log, err := GradeFixture(ctx, f, ws)
			if err != nil {
				t.Fatalf("GradeFixture error: %v", err)
			}
			if passed {
				t.Errorf("grading should fail on broken code:\n%s", log)
			}
			t.Logf("grading correctly fails: %s", truncateOutput([]byte(log), 200))
		})
	}

	if goFixtures != 3 {
		t.Errorf("expected 3 Go fixtures, found %d", goFixtures)
	}
}

// TestGradeFixture_GoFixtures_FixedCodePasses verifies that the grading
// framework correctly passes all Go fixtures after applying the reference fix.
func TestGradeFixture_GoFixtures_FixedCodePasses(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Go fixture tests require Unix")
	}

	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	fixes := map[string]func(ws string){
		"go-pricing-bugfix": func(ws string) {
			applyReplace(t, ws, "pricing.go", "math.Floor", "math.Round")
		},
		"go-pricing-rename": func(ws string) {
			applyReplace(t, ws, "pricing.go", "func CalcTotal", "func CalculateTotal")
			applyReplace(t, ws, "order.go", "CalcTotal", "CalculateTotal")
		},
		"go-duration-diagnosis": func(ws string) {
			applyReplace(t, ws, "timeutil.go", "if seconds > 0 && hours == 0", "if seconds > 0")
		},
	}

	for _, f := range fixtures {
		if f.Language != "go" {
			continue
		}
		fix, ok := fixes[f.ID]
		if !ok {
			continue
		}

		t.Run(f.ID+"_fixed", func(t *testing.T) {
			ws := t.TempDir()
			copyDir(t, f.SubjectDir, ws)

			// Apply the reference fix.
			fix(ws)

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			passed, log, err := GradeFixture(ctx, f, ws)
			if err != nil {
				t.Fatalf("GradeFixture error: %v", err)
			}
			if !passed {
				t.Errorf("grading should pass on fixed code:\n%s", log)
			}
		})
	}
}

// TestLoadFixtures_ReturnsExpectedCount verifies that LoadFixtures finds
// all 9 fixtures across the three languages.
func TestLoadFixtures_ReturnsExpectedCount(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	if len(fixtures) < 9 {
		t.Errorf("expected at least 9 fixtures, got %d", len(fixtures))
	}

	languages := map[string]int{}
	shapes := map[string]int{}
	for _, f := range fixtures {
		languages[f.Language]++
		shapes[f.Shape]++
		t.Logf("fixture: %s (lang=%s, shape=%s)", f.ID, f.Language, f.Shape)
	}

	for _, lang := range []string{"go", "python", "typescript"} {
		if languages[lang] < 3 {
			t.Errorf("expected at least 3 %s fixtures, got %d", lang, languages[lang])
		}
	}
}

// TestLoadFixtures_GeneratesPrompts verifies that prompts are generated
// for all fixtures.
func TestLoadFixtures_GeneratesPrompts(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatalf("LoadFixtures: %v", err)
	}

	for _, f := range fixtures {
		if f.Prompt == "" {
			t.Errorf("fixture %s has empty prompt", f.ID)
		}
	}
}

func TestValidateFixtures_ReferenceAndBrokenOutcomes(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range fixtures {
		if f.ID != "go-pricing-bugfix" {
			continue
		}
		report := ValidateFixtures(context.Background(), []Fixture{f})
		if !report.Passed || len(report.Fixtures) != 1 {
			t.Fatalf("fixture must fail initially and pass private grading plus prescribed checks after its reference fix: %+v", report)
		}
		return
	}
	t.Fatal("missing fixture")
}

func TestValidateFixtures_FrozenNineCaseSuite(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(fixtures) != 9 {
		t.Fatalf("expected exactly nine fixtures, got %d", len(fixtures))
	}
	mix := map[string]int{}
	for _, f := range fixtures {
		mix[f.Language+"/"+f.Shape]++
		if len(f.RequiredGradeTests) == 0 {
			t.Errorf("%s has no declared private test coverage", f.ID)
		}
	}
	if len(mix) != 9 {
		t.Fatalf("expected one fixture per language and task shape: %v", mix)
	}
	report := ValidateFixtures(context.Background(), fixtures)
	for _, r := range report.Fixtures {
		if !r.Passed {
			t.Errorf("%s: %s\nreference checks: %+v\nreference grade: %s", r.ID, r.Error, r.ReferenceChecks, truncateOutput([]byte(r.ReferenceGradeLog), 800))
		}
	}
	if !report.Passed && !t.Failed() {
		t.Fatal("suite validation failed without a fixture result")
	}
}

// --- helpers ---

func applyReplace(t *testing.T, ws, filename, old, new string) {
	t.Helper()
	path := filepath.Join(ws, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filename, err)
	}
	newData := []byte(replaceStr(string(data), old, new))
	if err := os.WriteFile(path, newData, 0644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func replaceStr(s, old, new string) string {
	result := ""
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result += new
			i += len(old)
		} else {
			result += string(s[i])
			i++
		}
	}
	return result
}
