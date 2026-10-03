package turnreduction

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAntigravity_ObservesInvocationsEquallyAcrossConditions(t *testing.T) {
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
	recorder := filepath.Join(t.TempDir(), "observer with spaces")
	build := exec.Command("go", "build", "-o", recorder, "./cmd/tzro-invocation-recorder")
	build.Dir = filepath.Clean(filepath.Join(fixtureDir(t), "../../../.."))
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build observer: %v %s", err, output)
	}
	client := fakeNativeClient(t, `
import json, os, pathlib, subprocess
config=json.loads((pathlib.Path.home()/'.gemini/config/hooks.json').read_text())
observer=config['tzro-benchmark-invocations']
is_tzro=(pathlib.Path.home()/'.gemini/config/mcp_config.json').exists()
assert ('tzro' in config)==is_tzro
def hook(event,payload):
    for command in observer[event]:
        run=subprocess.run(command['command'],input=json.dumps(payload),shell=True,text=True,capture_output=True)
        assert run.returncode==0,run.stderr
        assert json.loads(run.stdout)=={}
metadata={'conversationId':'hooks','modelName':'gemini-3.8-flash-low'}
for n in range(2):
    payload=dict(metadata,invocationNum=n,initialNumSteps=1+n*5)
    hook('PreInvocation',payload)
    hook('PostInvocation',payload)
hook('Stop',dict(metadata,executionNum=1,terminationReason='model_stop',fullyIdle=True))
p=pathlib.Path('pricing.go');p.write_text(p.read_text().replace('math.Floor','math.Round'))
print(json.dumps({'event':'init','conversation_id':'hooks','init':{'cwd':os.getcwd(),'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':['run_command']}}))
for i in [1,1,6]:
    print(json.dumps({'event':'step_update','step_update':{'conversation_id':'hooks','step_index':i,'state':'DONE','step_type':'agent_response'}}))
for i in range(4):
    print(json.dumps({'event':'step_update','step_update':{'conversation_id':'hooks','step_index':10+i,'state':'DONE','step_type':'tool','tool_name':'run_command'}}))
print(json.dumps({'event':'result','result':{'conversation_id':'hooks','status':'SUCCESS','num_turns':1,'usage':{'total_tokens':1}}}))
`)
	guard, err := DefaultGuardPath(fixture.SubjectDir)
	if err != nil {
		t.Fatal(err)
	}
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, TzroPath: client, HelperPath: filepath.Join(filepath.Dir(guard), "bulk_update.py"), InvocationRecorderPath: recorder, Fixtures: []Fixture{fixture}, OutputDir: filepath.Join(t.TempDir(), "run"), Offline: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Cells) != 3 {
		t.Fatalf("missing matched conditions: %+v", report)
	}
	for _, cell := range report.Cells {
		if cell.Events.AgentResponseSteps != 2 {
			t.Fatalf("native response cross-check missing: %+v", cell.Events)
		}
		if !cell.Passed || cell.Events.CloudDecisionRounds == nil || *cell.Events.CloudDecisionRounds != 2 || cell.Events.ToolCalls["run_command"] != 4 {
			t.Fatalf("incorrect invocation evidence: %+v", cell)
		}
		if cell.Events.ProviderRequests != nil {
			t.Fatal("native invocations cannot establish HTTP requests")
		}
		for _, file := range []string{"invocations.ndjson", "invocation-summary.json", "client-config/hooks.json"} {
			if _, err := os.Stat(filepath.Join(cell.ArtifactDir, file)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestRunAntigravity_MissingInvocationHooksStopBeforeAnotherLaunch(t *testing.T) {
	fixtures, err := LoadFixtures(fixtureDir(t))
	if err != nil {
		t.Fatal(err)
	}
	client := fakeNativeClient(t, `
import json,os
print(json.dumps({'event':'init','conversation_id':'nohooks','init':{'cwd':os.getcwd(),'model':'gemini-3.8-flash-low','permission_mode':'always-proceed','tools':[]}}))
print(json.dumps({'event':'result','result':{'conversation_id':'nohooks','status':'SUCCESS','num_turns':1,'usage':{'total_tokens':1}}}))
`)
	report, err := RunAntigravity(context.Background(), EvaluationConfig{ClientPath: client, InvocationRecorderPath: client, Fixtures: fixtures[:2], Conditions: []Condition{ConditionNative}, OutputDir: filepath.Join(t.TempDir(), "run"), Offline: true})
	if err == nil || !strings.Contains(err.Error(), "invocation evidence") || report == nil || len(report.Cells) != 1 || report.Cells[0].Events.CloudDecisionRounds != nil {
		t.Fatalf("missing observer must stop matrix: report=%+v err=%v", report, err)
	}
}
