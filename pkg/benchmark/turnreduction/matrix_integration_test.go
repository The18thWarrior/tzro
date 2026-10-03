package turnreduction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"tzro/pkg/verification"
)

func TestRunAntigravity_CompleteOfflineMatrixUsesRealGuardToolsAndGrades(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(fixtureDir(t), "../../../.."))
	binary := filepath.Join(t.TempDir(), "tzro")
	build := exec.Command("go", "build", "-o", binary, "./cmd/tzro")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, out)
	}
	jobs := map[string]any{}
	for _, fixture := range fixtures {
		preset, err := verification.LoadPreset(filepath.Join(fixture.SubjectDir, ".tzro/verification.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		jobs[fixture.Prompt] = map[string]any{"edits": fixture.ReferenceEdits, "checks": preset.Checks}
	}
	data, err := json.Marshal(jobs)
	if err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, fmt.Sprintf(offlineMatrixClient, string(data)))
	output := filepath.Join(t.TempDir(), "matrix")
	if evidence := os.Getenv("TZRO_OFFLINE_MATRIX_EVIDENCE"); evidence != "" {
		output = evidence
	}
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, TzroPath: binary, HelperPath: filepath.Join(root, "scripts/bulk_update.py"), OutputDir: output, Fixtures: fixtures, Offline: true, TaskTimeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cells) != 27 || report.Summary.Status != "offline_only" || report.Summary.Confirmed {
		for _, cell := range report.Cells {
			if !cell.Passed {
				t.Logf("failed %s/%s: %+v", cell.FixtureID, cell.Condition, cell.Errors)
				stderr, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "stderr.log"))
				grade, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "grade.log"))
				t.Logf("stderr: %s\ngrade: %s", stderr, grade)
			}
		}
		t.Fatalf("full offline matrix failed: %+v", report.Summary)
	}
	for _, cell := range report.Cells {
		if !cell.Passed || cell.VerifiedCompletionSeconds == nil {
			t.Fatalf("unverified matrix cell: %+v", cell)
		}
		if cell.Condition == ConditionTzro && cell.Events.ToolCalls["tzro_edit_and_verify"] != 1 {
			t.Fatalf("Tzro cell bypassed production MCP: %+v", cell)
		}
	}
	ledger, err := os.ReadFile(filepath.Join(output, "launches.json"))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Runs []struct {
			Status string `json:"status"`
			Fake   bool   `json:"fake"`
		}
	}
	if err = json.Unmarshal(ledger, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Runs) != 27 {
		t.Fatalf("missing launch receipts: %d", len(saved.Runs))
	}
	for _, entry := range saved.Runs {
		if !entry.Fake || entry.Status != "completed" {
			t.Fatalf("invalid offline launch receipt: %+v", entry)
		}
	}
}

// This scripted provider belongs only to the offline test. No solution data is
// installed in agent workspaces or used by the production benchmark runner.
const offlineMatrixClient = `
import json,pathlib,subprocess,sys,os
jobs=json.loads(%q)
job=jobs[sys.argv[sys.argv.index('-p')+1]]
mcp=pathlib.Path.home()/'.gemini/config/mcp_config.json'
tool='run_command'
if mcp.exists():
    config=json.loads(mcp.read_text())['mcpServers']['tzro']
    server=subprocess.Popen([config['command']]+config['args'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    def call(i,method,params):
        server.stdin.write(json.dumps({'jsonrpc':'2.0','id':i,'method':method,'params':params})+'\n');server.stdin.flush()
        while True:
            line=server.stdout.readline()
            if not line: raise RuntimeError('MCP server ended')
            response=json.loads(line)
            if response.get('id')==i: return response
    try:
        call(1,'initialize',{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'offline-matrix','version':'1'}})
        result=call(2,'tools/call',{'name':'tzro_edit_and_verify','arguments':{'edits':job['edits']}})
        assert 'verification: passed' in json.dumps(result),result
    finally:
        server.stdin.close();server.wait(timeout=5)
    tool='tzro_edit_and_verify'
else:
    for edit in job['edits']:
        path=pathlib.Path(edit['path'])
        if pathlib.Path('.tools/bulk_update.py').exists():
            subprocess.run([sys.executable,'.tools/bulk_update.py',edit['old_text'],edit['new_text'],str(path)],check=True,capture_output=True)
        else:
            path.write_text(path.read_text().replace(edit['old_text'],edit['new_text']))
    for check in job['checks']:
        result=subprocess.run(check['Argv'],cwd=check['Cwd'] or '.',capture_output=True,text=True)
        assert result.returncode==0,result.stdout+result.stderr
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':['run_command','write_to_file',tool]}}))
print(json.dumps({'event':'step_update','step_update':{'conversation_id':'offline','step_index':1,'state':'DONE','step_type':'tool','tool_name':tool}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS','usage':{'input_tokens':10,'output_tokens':3,'total_tokens':13}}}))
`
