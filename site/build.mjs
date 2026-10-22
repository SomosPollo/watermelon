import { mkdir, rm, writeFile, readFile, cp } from 'node:fs/promises';
import path from 'node:path';
import { createRenderer, escape, loadContent, normalizeBase, repoRoot, siteRoot } from './lib/content.mjs';
import { article, layout } from './lib/layout.mjs';
import { home } from './lib/home.mjs';

const base = normalizeBase(process.env.PAGES_BASE_PATH ?? '/');
const origin = new URL(process.env.SITE_ORIGIN ?? 'https://somospollo.github.io').origin;
// Keep validation builds separate from a running local preview.
const out = path.join(siteRoot, process.argv.includes('--check') ? '.check-dist' : 'dist');
const { pages, files } = await loadContent();
const render = createRenderer(pages, base);
await rm(out, { recursive: true, force: true });
await mkdir(path.join(out, 'assets/fonts'), { recursive: true });
await cp(path.join(siteRoot, 'public'), path.join(out, 'assets'), { recursive: true });
for (const family of ['archivo', 'dm-sans', 'ibm-plex-mono']) {
  await cp(path.join(siteRoot, 'node_modules/@fontsource', family, 'LICENSE'), path.join(out, 'assets/fonts', `${family}-LICENSE.txt`));
}
for (const [family, weights] of [['archivo', [700]], ['dm-sans', [400, 500, 600, 700]], ['ibm-plex-mono', [400]]]) {
  for (const weight of weights) {
    const file = `${family}-latin-${weight}-normal.woff2`;
    await cp(path.join(siteRoot, 'node_modules/@fontsource', family, 'files', file), path.join(out, 'assets/fonts', file));
  }
}
async function output(route, text) {
  const destination = path.join(out, route, 'index.html');
  await mkdir(path.dirname(destination), { recursive: true });
  await writeFile(destination, text);
}
await output('', layout({ title: 'Watermelon', description: 'Run dependencies, builds, and AI coding tools in a Lima-managed Linux VM. Define the files and network destinations they can reach.', base, origin, body: home(base) }));
const search = [];
for (const page of pages) {
  const html = render(page, files);
  let attachments = '';
  if (page.example) {
    const directory = path.posix.dirname(page.source);
    const assets = files.filter((file) => path.posix.dirname(file) === directory && !file.endsWith('.md'));
    if (assets.length) {
      attachments = '<section class="example-files"><h2 id="example-files">Example files</h2><p>Review the configuration before using it in a local project. Preserve an existing managed workspace’s settings.</p>';
      for (const source of assets) {
        const content = await readFile(path.join(repoRoot, source), 'utf8');
        const previewPage = { ...page, markdown: `\`\`\`${source.endsWith('.toml') ? 'toml' : 'bash'}\n${content}\n\`\`\`` };
        attachments += `<details><summary>${escape(path.basename(source))}</summary>${render(previewPage, files)}</details><a class="download-link" download="${escape(path.basename(source))}" href="${base}${source}">Download ${escape(path.basename(source))}</a>`;
      }
      attachments += '</section>';
      page.headings.push({ id: 'example-files', label: 'Example files', level: 2 });
    }
  }
  await output(page.route, layout({ title: page.title, description: page.description, base, origin, route: page.route, section: page.group === null ? 'use-cases' : 'docs', body: article(page, html, pages, base, attachments) }));
  // Index individual sections, so results can lead directly to commands and settings.
  const sections = html.split(/(?=<h[1-3]\b)/);
  for (const section of sections) {
    const heading = section.match(/^<h[1-3] id="([^"]+)">([\s\S]*?)<\/h[1-3]>/);
    const plain = section.replace(/<button\b[\s\S]*?<\/button>/g, '').replace(/<[^>]*>/g, ' ').replace(/&(?:amp|lt|gt|quot|#39);/g, (value) => ({'&amp;': '&', '&lt;': '<', '&gt;': '>', '&quot;': '"', '&#39;': "'"})[value]).replace(/\s+/g, ' ').trim();
    if (!plain) continue;
    search.push({ page: page.title, title: heading ? heading[2].replace(/<[^>]+>/g, '') : page.title, url: `${base}${page.route}${heading ? '#' + heading[1] : ''}`, text: plain });
  }
}
// Copy the downloadable files, including dotfiles that Pages won't otherwise generate.
for (const file of files.filter((file) => !file.endsWith('.md'))) {
  const target = path.join(out, file);
  await mkdir(path.dirname(target), { recursive: true });
  await cp(path.join(repoRoot, file), target);
}
await writeFile(path.join(out, 'search-index.json'), JSON.stringify(search));
await writeFile(path.join(out, '.nojekyll'), '');
await writeFile(path.join(out, '404.html'), layout({ title: 'Page not found', description: 'Find your way back to the Watermelon documentation.', base, origin, route: '404.html', noindex: true, body: `<main id="main" class="not-found page-width"><p>404</p><h1>Outside the boundaries.</h1><p>This page does not exist. Find a guide in the documentation or head back home.</p><a class="button button-primary" href="${base}docs/">Open documentation</a><a href="${base}">Back to Watermelon</a></main>` }));
await writeFile(path.join(out, 'sitemap.xml'), `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">${['', ...pages.map((page) => page.route)].map((route) => `<url><loc>${origin}${base}${route}</loc></url>`).join('')}</urlset>`);
await writeFile(path.join(out, 'robots.txt'), `User-agent: *\nAllow: /\nSitemap: ${origin}${base}sitemap.xml\n`);
console.log(`Built homepage + ${pages.length} articles in ${path.relative(repoRoot, out)} (base ${base}).`);
