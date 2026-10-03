package hooks

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPiGraphPrivateInputAndCancellation(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for the Pi extension check")
	}
	dir := t.TempDir()
	extension := filepath.Join(dir, "extension.ts")
	if err := os.WriteFile(extension, []byte(renderPiExtension("/fixture/tzro")), 0600); err != nil {
		t.Fatal(err)
	}
	script := `
import assert from 'node:assert/strict';
import {readFileSync,statSync,existsSync} from 'node:fs';
import {dirname} from 'node:path';
import {pathToFileURL} from 'node:url';
const {default:install}=await import(pathToFileURL(process.argv[1]).href);
let graphTool, mode='success', temp, invoked=0;
const controller=new AbortController();
install({registerTool(t){if(t.name==='tzro_execute_graph')graphTool=t},on(){},async exec(binary,args,opts){
  invoked++; temp=args[1];
  assert.equal(binary,'/fixture/tzro');
  assert.deepEqual(args.slice(2),['--result','selected']);
  assert.equal(opts.cwd,'/fixture/workspace');
  assert.equal(opts.signal,controller.signal);
  assert.equal(statSync(temp).mode & 0o777,0o600);
  assert.equal(statSync(dirname(temp)).mode & 0o777,0o700);
  assert.deepEqual(JSON.parse(readFileSync(temp,'utf8')),graph);
  if(mode==='failure')throw new Error('fixture execution failure');
  if(mode==='cancel')return await new Promise((resolve,reject)=>{
    opts.signal.addEventListener('abort',()=>reject(new Error('cancelled')),{once:true});
    controller.abort();
  });
  return {code:0,stdout:'{"status":"completed","returns":{"value":42}}'};
}});
assert.ok(graphTool);
const graph={version:'3.0',task_id:'quoted " $() input',nodes:[{id:'a',type:'tool',tool:'bash',args:{command:'printf hello'}}]};
const call=()=>graphTool.execute('id',{graph},controller.signal,undefined,{cwd:'/fixture/workspace'});
assert.match((await call()).content[0].text,/42/);
assert.ok(!existsSync(dirname(temp)));
mode='failure'; await assert.rejects(call(),/fixture execution failure/);
assert.ok(!existsSync(dirname(temp)));
mode='cancel'; await assert.rejects(call(),/cancelled/);
assert.ok(!existsSync(dirname(temp)));
const before=invoked; await assert.rejects(call()); assert.equal(invoked,before);
`
	cmd := exec.Command(node, "--experimental-strip-types", "--input-type=module", "-e", script, extension)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("native extension contract: %v\n%s", err, out)
	}
}
