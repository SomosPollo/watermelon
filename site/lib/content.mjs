import { readdir, readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import MarkdownIt from 'markdown-it';
import GithubSlugger from 'github-slugger';
import hljs from 'highlight.js';

export const siteRoot = fileURLToPath(new URL('../', import.meta.url));
export const repoRoot = path.resolve(siteRoot, '..');
export const repository = 'https://github.com/SomosPollo/watermelon';
export const escape = (text) => String(text).replace(/[&<>"']/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);

// GitHub Pages does not serve dotfiles. Download links retain the source name
// through their download attribute while using a non-hidden public filename.
export function publicAssetPath(source) {
  return path.posix.join(path.posix.dirname(source), path.posix.basename(source).replace(/^\./, ''));
}

export function normalizeBase(value = '/') {
  if (!/^\/(?:[a-zA-Z0-9_-]+\/)*[a-zA-Z0-9_-]*$/.test(value)) throw new Error('PAGES_BASE_PATH must be a URL path such as / or /watermelon/');
  return value === '/' ? '/' : `${value.replace(/\/$/, '')}/`;
}

export const metadata = {
  'README.md': ['Quick start', 'Install Watermelon, choose a workspace, and run your first sandbox.', 'Start here', 'docs/quick-start/'],
  'docs/README.md': ['Documentation', 'Guides, command and configuration references, and practical examples.', 'Start here', 'docs/'],
  'docs/INSTALLATION.md': ['Installation', 'Requirements, release installs, Go installs, and upgrades.', 'Start here', 'docs/installation/'],
  'docs/WORKSPACES.md': ['Project workflows', 'Clone, prepare, edit, serve, export, and recover your projects.', 'Guides', 'docs/workflows/'],
  'docs/CODING_ASSISTANTS.md': ['Codex and Claude Code', 'Install the assistant skill and run project installs, builds, and tests in Watermelon.', 'Guides', 'docs/coding-assistants/'],
  'docs/TROUBLESHOOTING.md': ['Troubleshooting', 'Diagnose installation, VM, command, network, and port issues.', 'Guides', 'docs/troubleshooting/'],
  'docs/COMMANDS.md': ['Command reference', 'Every command, flag, and project selection rule.', 'Reference', 'docs/commands/'],
  'docs/CONFIG_SPEC.md': ['Configuration reference', 'Every .watermelon.toml field, default, and validation rule.', 'Reference', 'docs/configuration/'],
  'docs/SECURITY.md': ['Security model', 'Understand the VM, network, filesystem, and trust boundaries.', 'Reference', 'docs/security/'],
  'CHANGELOG.md': ['Changelog', 'Release changes and upgrade notes.', 'Reference', 'docs/changelog/'],
  'skills/watermelon/SKILL.md': ['Agent skill', 'Instructions for AI coding agents working with Watermelon.', 'Reference', 'docs/agent-skill/'],
  'docs/DEVELOPMENT.md': ['Development', 'Build Watermelon and run unit, integration, and VM tests.', 'Contributing', 'docs/development/'],
  'docs/RELEASE_REVIEW.md': ['Release review', 'Release verification procedures and contributor checks.', 'Contributing', 'docs/release-review/'],
  'docs/UX_BACKLOG.md': ['UX backlog', 'Proposed improvements and implementation tracking, not current usage instructions.', 'Contributing', 'docs/ux-backlog/'],
  'docs/examples/README.md': ['Examples', 'Reviewed configurations for different languages, tools, and workflows.', 'Examples', 'docs/examples/'],
  'site/README.md': ['Website development', 'Preview, maintain, verify, and deploy the GitHub Pages website.', 'Contributing', 'docs/website/'],
  'site/content/use-cases.md': ['Use cases', 'Use Watermelon for dependencies, AI tools, unfamiliar code, and development servers.', null, 'use-cases/'],
};

async function walk(directory) {
  const entries = await readdir(path.join(repoRoot, directory), { withFileTypes: true });
  const paths = await Promise.all(entries.map((entry) => entry.isDirectory() ? walk(`${directory}/${entry.name}`) : `${directory}/${entry.name}`));
  return paths.flat().sort();
}

export async function loadContent() {
  const files = await walk('docs');
  const sources = ['README.md', ...files.filter((file) => file.endsWith('.md')), 'CHANGELOG.md', 'skills/watermelon/SKILL.md', 'site/content/use-cases.md', 'site/README.md'];
  const pages = await Promise.all(sources.map(async (source) => {
    // The skill's YAML metadata is for agents, not part of its rendered article.
    const markdown = (await readFile(path.join(repoRoot, source), 'utf8')).replace(/^---\r?\n[\s\S]*?\r?\n---\r?\n/, '');
    const fallbackTitle = markdown.match(/^# (.+)$/m)?.[1] ?? path.basename(source, '.md');
    const example = source.startsWith('docs/examples/') && source !== 'docs/examples/README.md';
    const [title, description, group, route] = metadata[source] ?? [fallbackTitle, `Watermelon guide: ${fallbackTitle}.`, example ? 'Examples' : 'Guides', source.replace(/\.md$/, '').replace(/\/README$/, '').toLowerCase().replaceAll('_', '-') + '/'];
    return { source, markdown, title, description, group, route, example, headings: [] };
  }));
  return { pages, files };
}

export function createRenderer(pages, base) {
  const bySource = new Map(pages.map((page) => [page.source, page]));
  const md = new MarkdownIt({ html: false, linkify: true, typographer: false, highlight(code, language) {
    const highlighted = language && hljs.getLanguage(language) ? hljs.highlight(code, { language }).value : escape(code);
    return `<div class="code-block"><div class="code-toolbar"><span>${escape(language || 'text')}</span><button class="copy-code" type="button" aria-label="Copy code">Copy</button></div><pre><code${language ? ` class="language-${escape(language)}"` : ''}>${highlighted}</code></pre></div>`;
  } });
  const defaultLinkOpen = md.renderer.rules.link_open ?? ((tokens, index, options, env, self) => self.renderToken(tokens, index, options));
  md.renderer.rules.link_open = (tokens, index, options, env, self) => {
    const token = tokens[index];
    const href = token.attrGet('href');
    if (href && !/^(?:[a-z][a-z\d+.-]*:|#|\/)/i.test(href)) {
      const [local, fragment] = href.split('#');
      const [pathname, query] = local.split('?');
      const resolved = path.posix.normalize(path.posix.join(path.posix.dirname(env.source), decodeURIComponent(pathname)));
      const target = bySource.get(resolved) ?? bySource.get(`${resolved.replace(/\/$/, '')}/README.md`);
      if (target) token.attrSet('href', base + target.route + (query ? `?${query}` : '') + (fragment ? `#${fragment}` : ''));
      else if (env.files.has(resolved)) token.attrSet('href', base + publicAssetPath(resolved) + (fragment ? `#${fragment}` : ''));
      else if (resolved.endsWith('.md')) throw new Error(`Unknown documentation link ${href} in ${env.source}`);
      else token.attrSet('href', `${repository}/blob/main/${resolved}` + (fragment ? `#${fragment}` : ''));
    }
    return defaultLinkOpen(tokens, index, options, env, self);
  };
  return (page, files) => {
    const tokens = md.parse(page.markdown, { source: page.source });
    const slugger = new GithubSlugger();
    page.headings = [];
    for (let index = 0; index < tokens.length; index++) {
      if (tokens[index].type !== 'heading_open') continue;
      const inline = tokens[index + 1];
      const label = (inline.children ?? []).map((child) => child.type === 'image' ? child.content : ['text', 'code_inline'].includes(child.type) ? child.content : '').join('');
      const id = slugger.slug(label);
      tokens[index].attrSet('id', id);
      if (tokens[index].tag !== 'h1') page.headings.push({ label, id, level: Number(tokens[index].tag.slice(1)) });
    }
    return md.renderer.render(tokens, md.options, { source: page.source, files: new Set(files) });
  };
}
