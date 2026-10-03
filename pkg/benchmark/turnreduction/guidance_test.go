package turnreduction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAntigravity_GuidanceIsScopedAndFrozen(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	var fixture Fixture
	for _, f := range fixtures {
		if f.ID == "go-pricing-bugfix" {
			fixture = f
		}
	}
	for _, drift := range []bool{false, true} {
		t.Run(fmt.Sprint("drift=", drift), func(t *testing.T) {
			guidance := filepath.Join(t.TempDir(), "guidance.md")
			if err := os.WriteFile(guidance, []byte("Prefer the installed edit-and-verify tool.\n"), 0600); err != nil {
				t.Fatal(err)
			}
			mutation := ""
			if drift {
				pathJSON, _ := json.Marshal(guidance)
				mutation = "pathlib.Path(" + string(pathJSON) + ").write_text('changed after first cell')"
			}
			client := fakeNativeClient(t, `
import json, os, pathlib
is_tzro=(pathlib.Path.home()/'.gemini/config/mcp_config.json').exists()
agents=pathlib.Path('AGENTS.md')
text=agents.read_text() if agents.exists() else ''
assert ('Prefer the installed edit-and-verify tool.' in text)==is_tzro
assert 'edit-and-verify' not in __import__('sys').argv[2]
p=pathlib.Path('pricing.go')
p.write_text(p.read_text().replace('math.Floor','math.Round'))
`+mutation+`
print(json.dumps({'event':'init','conversation_id':'guidance','init':{'cwd':os.getcwd(),'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':['run_command']}}))
print(json.dumps({'event':'result','result':{'conversation_id':'guidance','status':'SUCCESS','num_turns':1,'usage':{'total_tokens':1}}}))
`)
			guard, err := DefaultGuardPath(fixture.SubjectDir)
			if err != nil {
				t.Fatal(err)
			}
			report, err := RunAntigravity(context.Background(), EvaluationConfig{
				ClientPath: client, TzroPath: client, HelperPath: filepath.Join(filepath.Dir(guard), "bulk_update.py"),
				TzroGuidancePath: guidance, OutputDir: filepath.Join(t.TempDir(), "evidence"), Fixtures: []Fixture{fixture}, Offline: true,
			})
			if drift {
				if err == nil || !strings.Contains(err.Error(), "frozen inputs changed") || report == nil || len(report.Cells) != 1 {
					t.Fatalf("guidance drift must stop before another launch: report=%+v err=%v", report, err)
				}
				return
			}
			if err != nil || report == nil || len(report.Cells) != 3 || report.TzroGuidanceSHA256 == "" {
				t.Fatalf("missing labeled guided matrix: report=%+v err=%v", report, err)
			}
			for _, cell := range report.Cells {
				if !cell.Passed {
					t.Fatalf("condition instructions or grading failed: %+v", cell)
				}
			}
		})
	}
}
