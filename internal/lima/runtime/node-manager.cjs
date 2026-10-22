'use strict';
const fs = require('fs');
const path = require('path');
const cp = require('child_process');
const config = JSON.parse(fs.readFileSync('/watermelon-runtime/projects.json', 'utf8'));
const name = process.argv[2];
if (!['pnpm', 'yarn'].includes(name)) throw new Error('Unknown package manager');
const cwd = process.cwd();
const jobs = config.projects.filter(p => p.manager && p.manager.startsWith(name + '@'));
jobs.sort((a,b) => b.directory.length - a.directory.length);
const matches = p => { const dir = path.resolve(config.root, p.directory); return cwd === dir || cwd.startsWith(dir + '/'); };
const job = jobs.find(matches) || (jobs.length === 1 ? jobs[0] : undefined);
if (!job) throw new Error('Choose a project directory with --cwd before invoking ' + name);
const version = job.manager.split('@')[1];
const pin = /^(\d+\.\d+\.\d+)(?:\+(sha224|sha256|sha384|sha512)\.([a-f0-9]+))?$/.exec(version);
const lengths = {sha224:56, sha256:64, sha384:96, sha512:128};
if (!pin || (pin[2] && pin[3].length !== lengths[pin[2]])) throw new Error('Invalid package manager version');
const prefix = path.join(process.env.HOME, '.watermelon-tools', 'corepack-' + config.corepack);
const corepack = path.join(prefix, 'node_modules', 'corepack', 'dist', 'corepack.js');
if (!fs.existsSync(corepack)) {
  fs.mkdirSync(prefix, {recursive:true});
  const result = cp.spawnSync('/usr/local/bin/npm', ['install', '--prefix', prefix, '--ignore-scripts', '--no-audit', '--no-fund', '--registry=https://registry.npmjs.org', 'corepack@' + config.corepack], {stdio:'inherit'});
  if (result.status !== 0) process.exit(result.status || 1);
}
const env = {...process.env, COREPACK_ENABLE_DOWNLOAD_PROMPT:'0', COREPACK_ENABLE_AUTO_PIN:'0', COREPACK_DEFAULT_TO_LATEST:'0'};
// Fetch only the approved manager before running any project command. Registry
// failures are safe to retry here; an install can execute non-idempotent hooks.
let prepared;
for (let attempt = 0; attempt < 3; attempt++) {
  prepared = cp.spawnSync('/usr/local/bin/node', [corepack, 'install', '--global', name+'@'+version], {stdio:['ignore','ignore','inherit'],env,timeout:120000});
  if (prepared.error && prepared.error.code !== 'ETIMEDOUT') throw prepared.error;
  if (prepared.status === 0) break;
  if (prepared.signal && !prepared.error) process.exit(1);
  if (attempt < 2) Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, (attempt + 1) * 1000);
}
if (prepared.error) throw prepared.error;
if (prepared.status !== 0) process.exit(prepared.status === null ? 1 : prepared.status);
const result = cp.spawnSync('/usr/local/bin/node', [corepack, name+'@'+version, ...process.argv.slice(3)], {stdio:'inherit',env});
if (result.error) throw result.error;
process.exit(result.status === null ? 1 : result.status);
