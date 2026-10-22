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
  assert.equal(exited,0); assert.equal(calls.length,2);
  assert.equal(calls[0].command,'/usr/local/bin/node');
  assert.deepEqual(Array.from(calls[0].args.slice(1)),['install','--global',name+'@'+version]);
  assert.equal(calls[1].args[1],name+'@'+version);
  assert.equal(calls[1].args[2],'install');
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

func TestNodeManagerRetriesOnlyTrustedPreparation(t *testing.T) {
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
	driver := `const fs=require('fs'),vm=require('vm'),assert=require('assert');
const source=fs.readFileSync(process.argv[1],'utf8');
function run(failures, projectStatus, kind='status') {
  const config={root:'/workspace/project',projects:[{directory:'pkg',manager:'pnpm@10.20.0'}],corepack:'0.34.4'};
  let prepares=0,projects=0,sleeps=[],exited,thrown;
  const stopped={};
  const fake={fs:{readFileSync:()=>JSON.stringify(config),existsSync:()=>true},path:require('path'),
    child_process:{spawnSync:(command,args,options)=>{
      assert.equal(command,'/usr/local/bin/node');
      assert.equal(options.env.COREPACK_ENABLE_DOWNLOAD_PROMPT,'0');
      if(args[1]==='install') {
        assert.deepEqual(Array.from(args.slice(1)),['install','--global','pnpm@10.20.0']);
        assert.equal(options.timeout,120000);
        assert.deepEqual(Array.from(options.stdio),['ignore','ignore','inherit']);
        prepares++;
        if(prepares>failures)return {status:0};
        if(kind==='timeout'||kind==='missing')return {status:null,error:Object.assign(new Error('preparation failed'),{code:kind==='timeout'?'ETIMEDOUT':'ENOENT'})};
        if(kind==='signal')return {status:null,signal:'SIGTERM'};
        return {status:17};
      }
      projects++; assert.deepEqual(Array.from(args.slice(1)),['pnpm@10.20.0','install']);
      return {status:projectStatus};
    }}};
  const proc={argv:['node','shim','pnpm','install'],cwd:()=>'/workspace/project/pkg',env:{HOME:'/workspace/home'},exit:code=>{exited=code;throw stopped;}};
  try {vm.runInNewContext(source,{require:name=>fake[name],process:proc,Atomics:{wait:(a,b,c,ms)=>sleeps.push(ms)}});}catch(e){if(e!==stopped)thrown=e;}
  if(kind==='missing'||kind==='signal') {
    assert.equal(prepares,1);assert.equal(projects,0);assert.deepEqual(sleeps,[]);
    if(kind==='missing')assert.equal(thrown.code,'ENOENT');else assert.equal(exited,1);
    return;
  }
  assert.equal(prepares,Math.min(failures+1,3));
  assert.deepEqual(sleeps,[1000,2000].slice(0,Math.min(failures,2)));
  if(failures>=3){if(kind==='timeout')assert.equal(thrown.code,'ETIMEDOUT');else assert.equal(exited,17);assert.equal(projects,0);}
  else {assert.equal(thrown,undefined);assert.equal(exited,projectStatus);assert.equal(projects,1);}
}
run(0,0);
run(1,0);
run(2,0);
run(3,0);
run(0,23);
run(2,23);
run(1,0,'timeout');
run(3,0,'timeout');
run(1,0,'signal');
run(1,0,'missing');
`
	if out, err := exec.Command(node, "-e", driver, file).CombinedOutput(); err != nil {
		t.Fatalf("runtime manager retry boundary: %v\n%s", err, out)
	}
}
