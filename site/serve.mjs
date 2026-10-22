import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import { watch, readdirSync, statSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { normalizeBase, repoRoot, siteRoot } from './lib/content.mjs';

const base = normalizeBase(process.env.PAGES_BASE_PATH ?? '/');
const root = path.join(siteRoot, 'dist');
const build = () => execFileSync(process.execPath, [path.join(siteRoot, 'build.mjs')], { stdio: 'inherit' });
build();
const types = { '.html': 'text/html; charset=utf-8', '.css': 'text/css; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.json': 'application/json', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.xml': 'application/xml', '.txt': 'text/plain; charset=utf-8', '.toml': 'text/plain; charset=utf-8', '.sh': 'text/plain; charset=utf-8' };
const server = createServer(async (request, response) => {
  try {
    const url = new URL(request.url, 'http://localhost');
    if (!url.pathname.startsWith(base)) {
      response.writeHead(302, { Location: base }); response.end(); return;
    }
    const filename = path.resolve(root, decodeURIComponent(url.pathname.slice(base.length)));
    if (filename !== root && !filename.startsWith(root + path.sep)) {
      response.writeHead(403); response.end(); return;
    }
    const info = await stat(filename);
    if (info.isDirectory() && !url.pathname.endsWith('/')) {
      response.writeHead(301, { Location: url.pathname + '/' + url.search }); response.end(); return;
    }
    const target = info.isDirectory() ? path.join(filename, 'index.html') : filename;
    response.writeHead(200, { 'Content-Type': types[path.extname(target)] ?? 'application/octet-stream', 'Cache-Control': 'no-store' });
    response.end(await readFile(target));
  } catch {
    response.writeHead(404, { 'Content-Type': 'text/html; charset=utf-8' });
    response.end(await readFile(path.join(root, '404.html')));
  }
});
const port = Number(process.env.PORT ?? 4321);
server.listen(port, '127.0.0.1', () => console.log(`Local: http://127.0.0.1:${port}${base}`));
// Some filesystems report metadata/read events too. Only rebuild on a source
// modification, so serving or reading a file cannot cause a build loop.
const sourceDirectories = ['site/public', 'site/lib', 'site/content', 'docs', 'skills/watermelon'];
const sourceFiles = ['README.md', 'CHANGELOG.md', 'site/README.md', 'site/build.mjs'];
function signature() {
  const records = [];
  function record(relative) {
    try {
      const full = path.join(repoRoot, relative);
      const info = statSync(full, { bigint: true });
      if (info.isDirectory()) {
        for (const name of readdirSync(full).sort()) record(`${relative}/${name}`);
      } else records.push(`${relative}:${info.mtimeNs}:${info.size}`);
    } catch { records.push(`${relative}:missing`); }
  }
  [...sourceDirectories, ...sourceFiles].forEach(record);
  return records.join('\n');
}
let lastSignature = signature();
let timer;
function scheduleBuild() {
  clearTimeout(timer);
  timer = setTimeout(() => {
    const next = signature();
    if (next === lastSignature) return;
    lastSignature = next;
    try { build(); } catch { console.error('Build failed. Fix the source and save again.'); }
  }, 150);
}
for (const source of sourceDirectories) watch(path.join(repoRoot, source), { recursive: true }, scheduleBuild);
// Directory watches survive editors that replace files by atomic rename.
for (const [directory, names] of [['', ['README.md', 'CHANGELOG.md']], ['site', ['README.md', 'build.mjs']]]) {
  watch(path.join(repoRoot, directory), (_event, filename) => {
    if (names.includes(String(filename))) scheduleBuild();
  });
}
