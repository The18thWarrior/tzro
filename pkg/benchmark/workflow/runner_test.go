package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledProfilesAreIsolated(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "tzro-source")
	build := exec.Command("go", "build", "-o", binary, "./cmd/tzro")
	build.Dir = "../../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	pi := filepath.Join(root, "pi")
	script := "#!/bin/sh\nif [ \"$1\" = --version ]; then echo fixture-1; exit; fi\nwhile IFS= read -r line; do\nif [ -f \"$PI_CODING_AGENT_DIR/skills/tzro/SKILL.md\" ]; then\nprintf '%s\\n' '{\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[{\"name\":\"skill:tzro\",\"source\":\"skill\"}]}}'\nelse\nprintf '%s\\n' '{\"type\":\"response\",\"command\":\"get_commands\",\"success\":true,\"data\":{\"commands\":[]}}'\nfi\ndone\n"
	if err := os.WriteFile(pi, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	installer, _ := filepath.Abs("../../../install.sh")
	cfg := Config{TzroBinary: binary, Installer: installer, PiBinary: pi, Model: "fixture", BaseURL: "http://127.0.0.1:1/v1", Profiles: []Profile{Baseline, Standard}, WorkDir: filepath.Join(root, "run"), SetupTimeout: 30 * time.Second,
		Tasks: []Task{{ID: "one", Prompt: "Fix this package.", Files: map[string]string{"go.mod": "module fixture\n\ngo 1.22\n"}}}}
	report, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 2 || !report.Ready {
		t.Fatalf("profiles not ready: %+v", report)
	}
	for _, result := range report.Results {
		if result.Status != "ready" || result.Usage.Requests != 0 {
			t.Fatalf("preflight made provider calls: %+v", result)
		}
		_, err := os.Stat(filepath.Join(result.Home, ".tzro", "bin", "tzro"))
		if (result.Profile == Baseline) != os.IsNotExist(err) {
			t.Fatalf("installation isolation failed: %s %v", result.Profile, err)
		}
		if result.SkillLoaded != (result.Profile != Baseline) {
			t.Fatalf("native skill discovery mismatch: %+v", result)
		}
	}
	t.Run("missing Full runtime stops before model calls", func(t *testing.T) {
		full := cfg
		full.Profiles = []Profile{Full}
		full.WorkDir = filepath.Join(root, "missing-full")
		report, err := Run(context.Background(), full)
		if err != nil {
			t.Fatal(err)
		}
		if report.Ready || len(report.Results) != 1 || report.Results[0].Status != "setup_incomplete" {
			t.Fatalf("missing runtime mislabeled ready: %+v", report)
		}
	})
	t.Run("Full rejects placeholder decisions", func(t *testing.T) {
		f := cfg
		f.Profiles = []Profile{Full}
		f.WorkDir = filepath.Join(root, "placeholder-full")
		makeWorker := func(name, response string) string {
			path := filepath.Join(root, name)
			if err := os.WriteFile(path, []byte("#!/bin/sh\necho '{\"status\":\"ready\"}'\nwhile IFS= read -r line; do echo '"+response+"'; done\n"), 0755); err != nil {
				t.Fatal(err)
			}
			return path
		}
		model := filepath.Join(root, "fixture.gguf")
		if err := os.WriteFile(model, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		extractorModel := filepath.Join(root, "extractor-model")
		if err := os.Mkdir(extractorModel, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(extractorModel, "model"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		f.Full = FullConfig{DecisionBin: makeWorker("decision", "{\"answer\":\"yes\",\"confidence\":0.95}"), DecisionModel: model, DecisionVersion: "fixture", ExtractorBin: makeWorker("extract", "{\"spans\":[{\"label\":\"file_path\",\"text\":\"main.go\",\"start\":0,\"end\":7,\"confidence\":0.99}]}"), ExtractorModel: extractorModel, ExtractorVersion: "fixture"}
		report, err := Run(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		if report.Ready {
			t.Fatal("placeholder decision response accepted")
		}
		f.Full.DecisionBin = makeWorker("decision-valid", "{\"answer\":\"Go\",\"confidence\":0.95}")
		f.WorkDir = filepath.Join(root, "ready-full")
		report, err = Run(context.Background(), f)
		if err != nil || !report.Ready || !report.Results[0].RuntimeReady {
			t.Fatalf("ready runtime rejected: %+v %v", report, err)
		}
	})
	t.Run("native execution records usage and grades edits", func(t *testing.T) {
		runPi := filepath.Join(root, "pi-run")
		events := "#!/bin/sh\ncase \"$*\" in\n*'--mode json'*)\nprintf 'package fixture\\nfunc Value() int { return 1 }\\n' > value.go\nprintf '%s\\n' '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"model\":\"fixture\",\"stopReason\":\"stop\",\"content\":[{\"type\":\"text\",\"text\":\"Fixed\"}],\"usage\":{\"input\":10,\"output\":2,\"cacheRead\":4,\"cacheWrite\":0}}}' '{\"type\":\"agent_end\"}'\n;;\n*) exec '" + pi + "' \"$@\" ;;\nesac\n"
		if err := os.WriteFile(runPi, []byte(events), 0755); err != nil {
			t.Fatal(err)
		}
		f := cfg
		f.PiBinary, f.WorkDir, f.Profiles = runPi, filepath.Join(root, "execution"), []Profile{Baseline}
		f.Run, f.APIKey, f.MaxCost = true, "fixture-credential", 1
		f.Prices = &Prices{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1}
		f.Tasks = []Task{{ID: "fix", Prompt: "Fix Value and run the tests.", Files: map[string]string{
			"go.mod":        "module fixture\n\ngo 1.22\n",
			"value.go":      "package fixture\nfunc Value() int { return 0 }\n",
			"value_test.go": "package fixture\nimport \"testing\"\nfunc TestValue(t *testing.T) { if Value()!=1 { t.Fatal(\"wrong value\") } }\n",
		}}}
		report, err := Run(context.Background(), f)
		if err != nil {
			t.Fatal(err)
		}
		r := report.Results[0]
		if r.Status != "completed" || !r.TaskSuccess || r.Usage.Requests != 1 || r.Usage.CacheRead != 4 || r.Usage.EstimatedCostUSD == nil {
			t.Fatalf("execution evidence incomplete: %+v", r)
		}
		for _, tc := range []struct{ name, body, status string }{
			{"missing usage", "echo '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"stopReason\":\"stop\"}}'\necho '{\"type\":\"agent_end\"}'", "failed"},
			{"timeout", "sleep 10", "timed_out"},
			{"provider error with exit zero", "echo '{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"stopReason\":\"error\",\"errorMessage\":\"fixture failure\",\"usage\":{\"input\":0,\"output\":0,\"cacheRead\":0,\"cacheWrite\":0}}}'\necho '{\"type\":\"agent_end\"}'", "failed"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "pi")
				body := "#!/bin/sh\ncase \"$*\" in\n*'--mode json'*)\n" + tc.body + "\n;;\n*) exec '" + pi + "' \"$@\" ;;\nesac\n"
				if err := os.WriteFile(path, []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
				bad := f
				bad.PiBinary, bad.WorkDir, bad.Timeout = path, filepath.Join(t.TempDir(), "profiles"), 100*time.Millisecond
				bad.Tasks = append([]Task{f.Tasks[0]}, f.Tasks[0])
				report, err := Run(context.Background(), bad)
				if err != nil {
					t.Fatal(err)
				}
				if report.Results[0].Status != tc.status {
					t.Fatalf("failure lost: %+v", report.Results)
				}
				if tc.name != "provider error with exit zero" && report.Results[1].Status != "not_run" {
					t.Fatalf("unknown usage did not stop suite: %+v", report.Results)
				}
			})
		}
	})
}
