package lima

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNodeManagerPreservesIntegrityAndRejectsInvalidPins(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is unavailable")
	}
	source, err := runtimeAssets.ReadFile("runtime/node-manager.cjs")
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "node-manager.cjs")
	if err := os.WriteFile(file, source, 0600); err != nil {
		t.Fatal(err)
	}
	// Execute the shipped shim with mocked filesystem/process boundaries. No
	// package download or repository code runs on the test host.
	driver := `const fs = require('fs'), vm = require('vm'), assert = require('assert');
const source = fs.readFileSync(process.argv[1], 'utf8');
function run(pin, valid) {
  const name = pin.split('@')[0], version = pin.split('@')[1];
  const config = {root:'/workspace/project', projects:[{directory:'pkg',manager:pin}], corepack:'0.34.4'};
  let exited, calls = [];
  const fake = {
    fs:{readFileSync:()=>JSON.stringify(config), existsSync:()=>true},
    path:require('path'),
    child_process:{spawnSync:(command,args,options)=>{calls.push({command,args,options}); return {status:0};}}
  };
  const proc = {argv:['node','shim',name,'install'], cwd:()=>'/workspace/project/pkg', env:{HOME:'/workspace/home'}, exit:code=>{exited=code;}};
  let error;
  try { vm.runInNewContext(source, {require:name=>fake[name], process:proc}); } catch(e) { error=e; }
  if (!valid) { assert(error, 'invalid pin reached Corepack'); assert.equal(calls.length,0); return; }
  if (error) throw error;
  assert.equal(exited,0); assert.equal(calls.length,1);
  assert.equal(calls[0].command,'/usr/local/bin/node');
  assert.equal(calls[0].args[1],name+'@'+version);
  assert.equal(calls[0].args[2],'install');
}
run('pnpm@10.20.0',true);
for (const [algorithm,length] of [['sha224',56],['sha256',64],['sha384',96],['sha512',128]]) {
  run('pnpm@10.20.0+'+algorithm+'.'+'a'.repeat(length),true);
  run('yarn@4.10.3+'+algorithm+'.'+'b'.repeat(length),true);
}
run('pnpm@10.20.0+sha512.a',false);
run('yarn@4.10.3+md5.'+'a'.repeat(32),false);
run('pnpm@https://example.com/package.tgz',false);
`
	if out, err := exec.Command(node, "-e", driver, file).CombinedOutput(); err != nil {
		t.Fatalf("runtime integrity handling: %v\n%s", err, out)
	}
}
