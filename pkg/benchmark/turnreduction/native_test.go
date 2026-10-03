package turnreduction

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunAntigravity_OneTaskIncludesIndependentGrading(t *testing.T) {
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
	client := fakeNativeClient(t, `
import json, os, pathlib, sys
args=sys.argv[1:]
assert args[0]=='-p'
assert args[args.index('--model')+1]=='gemini-3.8-flash-low'
assert args[args.index('--output-format')+1]=='stream-json'
settings=json.loads((pathlib.Path.home()/'.gemini/antigravity-cli/settings.json').read_text())
assert settings['modelProvider']=='gemini'
p=pathlib.Path('pricing.go')
p.write_text(p.read_text().replace('math.Floor','math.Round'))
print(json.dumps({'event':'init','conversation_id':'test','init':{'cwd':os.getcwd(),'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':['run_command','write_to_file']}}))
print(json.dumps({'event':'result','result':{'conversation_id':'test','status':'SUCCESS','num_turns':1,'usage':{'input_tokens':10,'output_tokens':3,'total_tokens':13}}}))
`)
	output := filepath.Join(t.TempDir(), "run")
	report, err := RunAntigravity(context.Background(), EvaluationConfig{
		ClientPath: client, OutputDir: output, Fixtures: []Fixture{fixture}, Conditions: []Condition{ConditionNative},
		Model: "gemini-3.8-flash-low", TaskTimeout: 30 * time.Second, Offline: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cells) != 1 {
		t.Fatalf("missing task result: %+v", report)
	}
	cell := report.Cells[0]
	if !cell.Passed || cell.VerifiedCompletionSeconds == nil || cell.GradeSeconds <= 0 || len(cell.Checks) == 0 {
		t.Fatalf("missing verified completion with final check receipts: %+v", cell)
	}
	if *cell.VerifiedCompletionSeconds < cell.AgentSeconds+cell.GradeSeconds {
		t.Fatalf("verified completion excludes measured work: %+v", cell)
	}
	if cell.Events.CloudDecisionRounds != nil {
		t.Fatal("native num_turns must not be treated as cloud rounds")
	}
	if _, err := os.Stat(filepath.Join(output, "report.json")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"events.ndjson", "stderr.log", "grade.log", "result.json", "workspace/pricing.go"} {
		if _, err := os.Stat(filepath.Join(cell.ArtifactDir, path)); err != nil {
			t.Fatalf("missing durable %s: %v", path, err)
		}
	}
}

func fakeNativeClient(t *testing.T, source string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skipf("python3 required: %v", err)
	}
	resolved, err := exec.Command(python, "-c", "import sys; print(sys.executable)").Output()
	if err != nil {
		t.Fatal(err)
	}
	python = strings.TrimSpace(string(resolved))
	path := filepath.Join(t.TempDir(), "fake-agy")
	if err := os.WriteFile(path, []byte("#!"+python+"\n"+source), 0755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunAntigravity_TzroUsesInstalledProductionMCP(t *testing.T) {
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
	binary := filepath.Join(t.TempDir(), "tzro")
	build := exec.Command("go", "build", "-o", binary, "./cmd/tzro")
	build.Dir = filepath.Clean(filepath.Join(fixtureDir(t), "../../../.."))
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production CLI: %v\n%s", err, out)
	}
	client := fakeNativeClient(t, `
import json, os, pathlib, subprocess, sys
prompt=sys.argv[sys.argv.index('-p')+1]
assert 'ALWAYS' not in prompt and 'edit_and_verify' not in prompt
config=json.loads((pathlib.Path.home()/'.gemini/config/mcp_config.json').read_text())['mcpServers']['tzro']
skill=(pathlib.Path.home()/'.gemini/config/skills/tzro/SKILL.md').read_text()
assert 'tzro_edit_and_verify' in skill
server=subprocess.Popen([config['command']]+config['args'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
def call(i,method,params):
    server.stdin.write(json.dumps({'jsonrpc':'2.0','id':i,'method':method,'params':params})+'\n')
    server.stdin.flush()
    while True:
        line=server.stdout.readline()
        if not line: raise RuntimeError('MCP server ended')
        response=json.loads(line)
        if response.get('id')==i: return response
try:
    call(1,'initialize',{'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'offline-native-test','version':'1'}})
    tools=call(2,'tools/list',{})['result']['tools']
    assert any(t['name']=='tzro_edit_and_verify' for t in tools)
    result=call(3,'tools/call',{'name':'tzro_edit_and_verify','arguments':{'edits':[{'kind':'replace','path':'pricing.go','old_text':'math.Floor','new_text':'math.Round'}]}})
    assert 'verification: passed' in json.dumps(result),result
finally:
    server.stdin.close()
    server.wait(timeout=5)
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':[t['name'] for t in tools]}}))
print(json.dumps({'event':'step_update','step_update':{'conversation_id':'offline','step_index':2,'state':'DONE','step_type':'tool','tool_name':'tzro_edit_and_verify','tool_info':{'name':'tzro_edit_and_verify','output':result}}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS','num_turns':1}}))
`)
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, TzroPath: binary, OutputDir: filepath.Join(t.TempDir(), "run"), Fixtures: []Fixture{fixture}, Conditions: []Condition{ConditionTzro}, Offline: true, TaskTimeout: 45 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cell := report.Cells[0]
	if !cell.Passed || cell.Events.ToolCalls["tzro_edit_and_verify"] != 1 {
		stderr, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "stderr.log"))
		t.Fatalf("production MCP path failed: %+v\n%s", cell, stderr)
	}
}

func TestRunAntigravity_SimpleUsesRealHelper(t *testing.T) {
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
	client := fakeNativeClient(t, `
import json, pathlib, subprocess, sys
assert not pathlib.Path('.agents/mcp_config.json').exists()
assert '.tools/bulk_update.py' in pathlib.Path('AGENTS.md').read_text()
subprocess.run([sys.executable,'.tools/bulk_update.py','math.Floor','math.Round','pricing.go'],check=True,stdout=subprocess.PIPE)
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':['run_command','write_to_file']}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS'}}))
`)
	helper := filepath.Clean(filepath.Join(fixtureDir(t), "../../../../scripts/bulk_update.py"))
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, HelperPath: helper, OutputDir: filepath.Join(t.TempDir(), "run"), Fixtures: []Fixture{fixture}, Conditions: []Condition{ConditionSimple}, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	cell := report.Cells[0]
	if !cell.Passed {
		stderr, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "stderr.log"))
		t.Fatalf("helper condition failed: %+v\n%s", cell, stderr)
	}
	original, _ := os.ReadFile(helper)
	installed, err := os.ReadFile(filepath.Join(cell.ArtifactDir, "workspace/.tools/bulk_update.py"))
	if err != nil || string(original) != string(installed) {
		t.Fatal("the measured helper differs from the production script")
	}
}

func TestRunAntigravity_DeadlineStopsClientDescendants(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, `
import pathlib,subprocess,time
subprocess.Popen(['sh','-c','sleep 2; echo survived > descendant.txt'])
pathlib.Path('started.txt').write_text('child launched')
time.sleep(10)
`)
	start := time.Now()
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: filepath.Join(t.TempDir(), "run"), Fixtures: fixtures[:1], Conditions: []Condition{ConditionNative}, Offline: true, TaskTimeout: 500 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	cell := report.Cells[0]
	if _, err := os.Stat(filepath.Join(cell.ArtifactDir, "workspace/started.txt")); err != nil {
		stderr, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "stderr.log"))
		t.Fatalf("deadline test never launched its child: %v\n%s", err, stderr)
	}
	if time.Since(start) > 1500*time.Millisecond {
		t.Fatalf("task exceeded its deadline while waiting for descendants: %v", time.Since(start))
	}
	if cell.Passed || cell.VerifiedCompletionSeconds != nil {
		t.Fatalf("interrupted task received verified completion: %+v", cell)
	}
	if _, err := os.Stat(filepath.Join(cell.ArtifactDir, "workspace/descendant.txt")); !os.IsNotExist(err) {
		t.Fatal("client descendant survived task cleanup")
	}
}

func TestRunAntigravity_ProtectedChecksCannotBeRemoved(t *testing.T) {
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
	client := fakeNativeClient(t, `
import json,pathlib
p=pathlib.Path('pricing.go');p.write_text(p.read_text().replace('math.Floor','math.Round'))
pathlib.Path('pricing_test.go').write_text('package pricing\n')
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS'}}))
`)
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: filepath.Join(t.TempDir(), "run"), Fixtures: []Fixture{fixture}, Conditions: []Condition{ConditionNative}, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	cell := report.Cells[0]
	if cell.Passed || cell.VerifiedCompletionSeconds != nil || !strings.Contains(strings.Join(cell.Errors, " "), "protected input changed: pricing_test.go") {
		t.Fatalf("removed tests were accepted: %+v", cell)
	}
}

func TestRunAntigravity_ReservesBeforeSpawning(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ledger := filepath.Join(root, "launches.json")
	client := fakeNativeClient(t, fmt.Sprintf(`
import json,pathlib
ledger=json.loads(pathlib.Path(%q).read_text())
assert len(ledger['runs'])==1 and ledger['runs'][0]['status']=='reserved'
assert ledger['runs'][0]['fake'] is True
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS','usage':{'total_tokens':11}}}))
`, ledger))
	guard := filepath.Clean(filepath.Join(fixtureDir(t), "../../../../scripts/run_workflow_validation.py"))
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: filepath.Join(root, "run"), Fixtures: fixtures[:1], Conditions: []Condition{ConditionNative}, Offline: true, GuardPath: guard, LedgerPath: ledger, RunID: "offline-run", MaxLaunches: 1})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cells[0].Events.Status != "SUCCESS" {
		stderr, _ := os.ReadFile(filepath.Join(report.Cells[0].ArtifactDir, "stderr.log"))
		t.Fatalf("client ran before reservation: %s", stderr)
	}
	data, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Runs []struct {
			Status       string `json:"status"`
			Charged      string `json:"charged_usd"`
			ContractHash string `json:"contract_hash"`
		}
	}
	if err = json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	if len(saved.Runs) != 1 || saved.Runs[0].Status != "completed" || saved.Runs[0].Charged != "unknown" || saved.Runs[0].ContractHash == "" {
		t.Fatalf("invalid durable launch evidence: %s", data)
	}
}

func TestRunAntigravity_OfflineNeverReceivesLiveCredential(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	secret := "fake-gemini-key-do-not-retain-0123456789"
	client := fakeNativeClient(t, `
import json,os,pathlib,sys
key=os.environ['GEMINI_API_KEY']
assert key == 'offline-invalid-key'
settings=(pathlib.Path.home()/'.gemini/antigravity-cli/settings.json').read_text()
assert key not in settings
(pathlib.Path.home()/'.gemini/antigravity-cli/cli.log').write_text('credential='+key)
print(key,file=sys.stderr)
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS','response':key}}))
`)
	output := filepath.Join(t.TempDir(), "run")
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: output, Fixtures: fixtures[:1], Conditions: []Condition{ConditionNative}, Offline: true, APIKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cells[0].Events.Status != "SUCCESS" {
		t.Fatalf("credential was not delivered to isolated client: %+v", report.Cells[0])
	}
	logData, logErr := os.ReadFile(filepath.Join(report.Cells[0].ArtifactDir, "client.log"))
	if logErr != nil || string(logData) != "credential=[REDACTED]" {
		t.Fatalf("client log was not retained and redacted: %v", logErr)
	}
	err = filepath.Walk(output, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) {
			return fmt.Errorf("credential retained in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunAntigravity_StreamsEvidenceBeforeClientExit(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "run")
	client := fakeNativeClient(t, fmt.Sprintf(`
import json,pathlib,time
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}),flush=True)
trace=pathlib.Path(%q)
deadline=time.monotonic()+2
while time.monotonic()<deadline:
    if trace.exists() and 'init' in trace.read_text(): break
    time.sleep(.01)
else: raise RuntimeError('native evidence was not retained while running')
print(json.dumps({'event':'result','result':{'status':'SUCCESS'}}),flush=True)
`, filepath.Join(output, fixtures[0].ID+"-native", "events.ndjson")))
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: output, Fixtures: fixtures[:1], Conditions: []Condition{ConditionNative}, Offline: true, TaskTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	cell := report.Cells[0]
	if cell.Events.Status != "SUCCESS" {
		stderr, _ := os.ReadFile(filepath.Join(cell.ArtifactDir, "stderr.log"))
		t.Fatalf("evidence is lost on interruption: %+v\n%s", cell, stderr)
	}
}

func TestRunAntigravity_DoesNotInheritArtifactRepositoryInstructions(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	if err = os.Mkdir(filepath.Join(parent, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(parent, "AGENTS.md"), []byte("unrelated repository instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, `
import json,pathlib
assert all(not (parent/'.git').exists() for parent in pathlib.Path.cwd().parents)
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS'}}))
`)
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: filepath.Join(parent, "evidence"), Fixtures: fixtures[:1], Conditions: []Condition{ConditionNative}, Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if report.Cells[0].Events.Status != "SUCCESS" {
		t.Fatalf("native inherited evidence repository: %+v", report.Cells[0])
	}
	if _, err = os.Stat(filepath.Join(report.Cells[0].ArtifactDir, "workspace", "timeutil.go")); err != nil {
		t.Fatalf("final workspace not retained: %v", err)
	}
}

func TestRunAntigravity_DisablesNativeClientSelfUpdate(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, `
import json,os,pathlib
if os.environ.get('AGY_CLI_DISABLE_AUTO_UPDATE') != 'true':
    executable=pathlib.Path(__file__)
    executable.write_text(executable.read_text()+'\n# automatic client update\n')
print(json.dumps({'event':'init','init':{'model':'gemini-3.8-flash-low','permission_mode':'always-proceed'}}))
print(json.dumps({'event':'result','result':{'status':'SUCCESS'}}))
`)
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, OutputDir: filepath.Join(t.TempDir(), "run"), Fixtures: fixtures[:2], Conditions: []Condition{ConditionNative}, Offline: true})
	if err != nil {
		t.Fatalf("client changed between matched tasks: %v", err)
	}
	if len(report.Cells) != 2 {
		t.Fatalf("incomplete matched run: %d", len(report.Cells))
	}
}
