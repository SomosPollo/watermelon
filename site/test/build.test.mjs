import { after, test } from 'node:test';
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { readFile, readdir, stat, rm } from 'node:fs/promises';
import path from 'node:path';
import { parse } from 'parse5';
import { loadContent, normalizeBase, siteRoot } from '../lib/content.mjs';

const root = path.join(siteRoot, '.check-dist');
const cache = new Map();
after(() => rm(root, { recursive: true, force: true }));
async function files(directory) {
  const entries = await readdir(directory, { withFileTypes: true });
  return (await Promise.all(entries.map((entry) => entry.isDirectory() ? files(path.join(directory, entry.name)) : path.join(directory, entry.name)))).flat();
}
function elements(node) {
  return [node, ...(node.childNodes ?? []).flatMap(elements)];
}
async function document(filename) {
  if (!cache.has(filename)) cache.set(filename, elements(parse(await readFile(filename, 'utf8'))));
  return cache.get(filename);
}
function attr(node, name) { return node.attrs?.find((attribute) => attribute.name === name)?.value; }
async function checkLink(href, current, base) {
  const url = new URL(href, `https://test.invalid${base}${path.relative(root, current).split(path.sep).join('/')}`);
  if (url.origin !== 'https://test.invalid') return;
  assert.ok(url.pathname.startsWith(base), `Link escapes base ${base}: ${href} in ${current}`);
  let target = path.join(root, decodeURIComponent(url.pathname.slice(base.length)));
  try { if ((await stat(target)).isDirectory()) target = path.join(target, 'index.html'); }
  catch { assert.fail(`Missing target ${href} in ${path.relative(root, current)}`); }
  assert.ok((await stat(target)).isFile(), `Missing file ${target}`);
  if (url.hash && target.endsWith('.html')) {
    const id = decodeURIComponent(url.hash.slice(1));
    assert.ok((await document(target)).some((node) => attr(node, 'id') === id), `Missing anchor ${href} in ${path.relative(root, current)}`);
  }
}

test('base paths accept root and project Pages paths, and reject invalid input', () => {
  assert.equal(normalizeBase('/'), '/');
  assert.equal(normalizeBase('/watermelon'), '/watermelon/');
  assert.equal(normalizeBase('/watermelon/'), '/watermelon/');
  for (const invalid of ['https://example.com', '../', '/a/../b', '/a?b', '/a"b']) assert.throws(() => normalizeBase(invalid));
});

for (const base of ['/', '/watermelon/']) {
  test(`all articles, links, anchors, downloads, assets and search results work at ${base}`, async () => {
    execFileSync(process.execPath, ['build.mjs', '--check'], { cwd: siteRoot, env: { ...process.env, PAGES_BASE_PATH: base }, stdio: 'pipe' });
    cache.clear();
    const { pages, files: sources } = await loadContent();
    const built = await files(root);
    assert.equal(built.filter((file) => file.endsWith('.html')).length, pages.length + 2);
    for (const page of pages) assert.ok(built.includes(path.join(root, page.route, 'index.html')), `Missing article ${page.source}`);
    for (const source of sources.filter((file) => !file.endsWith('.md'))) assert.ok(built.includes(path.join(root, source)), `Missing download ${source}`);
    for (const file of built.filter((file) => file.endsWith('.html'))) {
      const nodes = await document(file);
      assert.equal(nodes.filter((node) => node.tagName === 'h1').length, 1, `Expected one h1 in ${file}`);
      const ids = nodes.map((node) => attr(node, 'id')).filter(Boolean);
      assert.equal(new Set(ids).size, ids.length, `Duplicate IDs in ${file}`);
      for (const node of nodes) {
        const link = attr(node, 'href') ?? attr(node, 'src');
        if (link) await checkLink(link, file, base);
        if (node.tagName === 'button') assert.ok(attr(node, 'aria-label') || node.childNodes?.length, `Unnamed button in ${file}`);
      }
    }
    const index = JSON.parse(await readFile(path.join(root, 'search-index.json'), 'utf8'));
    for (const page of pages) assert.ok(index.some((entry) => entry.page === page.title), `Not indexed: ${page.title}`);
    for (const entry of index) await checkLink(entry.url, path.join(root, 'index.html'), base);
    assert.ok(index.some((entry) => entry.title === 'watermelon clone'), 'Command-level search is missing');
    const css = await readFile(path.join(root, 'assets/style.css'), 'utf8');
    for (const [, asset] of css.matchAll(/url\(['"]?([^)'"\s]+)['"]?\)/g)) await checkLink(asset, path.join(root, 'assets/style.css'), base);
    const robots = await readFile(path.join(root, 'robots.txt'), 'utf8');
    assert.ok(robots.includes(`https://somospollo.github.io${base}sitemap.xml`));
  });
}
