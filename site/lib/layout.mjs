import { escape, metadata, repository } from './content.mjs';

export const icon = (name, extra = '') => {
  const paths = {
    search: '<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4 4"/>',
    arrow: '<path d="M5 12h14m-6-6 6 6-6 6"/>',
    terminal: '<path d="m5 7 5 5-5 5m8 0h6"/>',
    code: '<path d="m8 6-6 6 6 6m8-12 6 6-6 6m-3-14-2 16"/>',
    box: '<path d="m12 3 9 5-9 5-9-5 9-5Zm-9 5v9l9 5 9-5V8M12 13v9M7.5 5.5l9 5"/>',
    lock: '<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2"/>',
    check: '<path d="m5 12 4 4L19 6"/>',
    globe: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3c5 5 5 13 0 18-5-5-5-13 0-18Z"/>',
    github: '<path d="M9 19c-4 1-4-2-6-2m12 5v-4c0-1 .2-2-1-3 4-.4 7-2 7-6 0-2-.5-3-2-4 .3-1 .4-2 0-3-2 0-3 1-4 2a14 14 0 0 0-6 0C8 3 7 2 5 2c-.4 1-.3 2 0 3-1.5 1-2 2-2 4 0 4 3 5.6 7 6-1 1-1 2-1 3v4"/>',
    close: '<path d="m6 6 12 12M6 18 18 6"/>',
  };
  return `<svg ${extra} width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] ?? paths.arrow}</svg>`;
};
export const mark = `<svg class="brand-mark" width="34" height="34" viewBox="0 0 40 40" aria-hidden="true"><path d="M4 13h32a16 16 0 0 1-32 0Z" fill="#192b43"/><path d="M7 13h26a13 13 0 0 1-26 0Z" fill="#c4d6ed"/><path d="M10 13h20a10 10 0 0 1-20 0Z" fill="#f1bb55"/><path d="m15 16 1 2m4-2v3m4-3-1 2" stroke="#192b43" stroke-width="1.7" stroke-linecap="round"/></svg>`;

export function header(base, section) {
  return `<header class="site-header"><div class="header-inner"><a href="${base}" class="brand" aria-label="Watermelon home">${mark}<span>watermelon</span></a><nav aria-label="Main navigation"><a href="${base}use-cases/"${section === 'use-cases' ? ' aria-current="page"' : ''}>Use cases</a><a href="${base}docs/"${section === 'docs' ? ' aria-current="true"' : ''}>Docs</a><a class="github-nav" href="${repository}" aria-label="Watermelon on GitHub">${icon('github')}<span>GitHub</span></a></nav><button type="button" class="search-trigger" aria-label="Search documentation" aria-haspopup="dialog">${icon('search')}<span>Search docs</span><kbd>⌘ K</kbd></button></div></header>`;
}

export function footer(base) {
  return `<footer class="site-footer"><div><a class="brand" href="${base}">${mark}<span>watermelon</span></a><p>A little boundary for your next big idea.</p></div><nav aria-label="Footer navigation"><a href="${base}docs/">Documentation</a><a href="${base}docs/security/">Security model</a><a href="${repository}">Source on GitHub</a></nav><p class="license">Open source. MIT licensed.</p></footer>`;
}

export function layout({ title, description, base, origin, route = '', body, section = 'home', wide = false, noindex = false }) {
  return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${escape(title === 'Watermelon' ? title + ' — Development inside a VM' : title + ' · Watermelon')}</title><meta name="description" content="${escape(description)}"><meta name="theme-color" content="#192b43">${noindex ? '<meta name="robots" content="noindex">' : ''}<link rel="canonical" href="${origin}${base}${route}"><meta property="og:type" content="website"><meta property="og:title" content="${escape(title)}"><meta property="og:description" content="${escape(description)}"><meta property="og:url" content="${origin}${base}${route}"><meta property="og:image" content="${origin}${base}assets/social.svg"><meta name="twitter:card" content="summary"><link rel="icon" href="${base}assets/favicon.svg" type="image/svg+xml"><link rel="stylesheet" href="${base}assets/style.css"><link rel="preload" href="${base}assets/fonts/archivo-latin-700-normal.woff2" as="font" type="font/woff2" crossorigin><script type="module" src="${base}assets/site.js"></script></head>
<body data-base="${base}" class="${wide ? 'wide' : ''}"><a href="#main" class="skip-link">Skip to content</a>${header(base, section)}${body}${footer(base)}<dialog class="search-dialog" aria-labelledby="search-title"><div class="search-heading"><h2 id="search-title">Search documentation</h2><button class="close-search icon-button" type="button" aria-label="Close search">${icon('close')}</button></div><label for="search-input" class="sr-only">Search guides, commands, and examples</label><div class="search-input-wrap">${icon('search')}<input id="search-input" type="search" placeholder="Try “network”, “clone”, or “ports”…" autocomplete="off" spellcheck="false"></div><p id="search-status" role="status">Search across all guides, references, and examples.</p><div id="search-results"></div><div class="search-footer"><span>Tab to navigate results</span><kbd>Esc to close</kbd></div></dialog><div class="sr-only" id="copy-status" role="status"></div></body></html>`;
}

export function sidebar(pages, current, base) {
  const groups = ['Start here', 'Guides', 'Reference', 'Examples', 'Contributing'];
  const order = Object.keys(metadata);
  const navigation = groups.map((group) => {
    const links = pages.filter((page) => page.group === group && page.source !== 'docs/README.md').sort((a, b) => {
      const ai = order.indexOf(a.source), bi = order.indexOf(b.source);
      return (ai < 0 ? 100 : ai) - (bi < 0 ? 100 : bi) || a.title.localeCompare(b.title);
    }).map((page) => `<a${page.source === current.source ? ' aria-current="page"' : ''} href="${base}${page.route}">${escape(page.title.replace(/ Example$/, ''))}</a>`).join('');
    if (group === 'Examples' || group === 'Contributing') {
      return `<details class="nav-group"${current.group === group ? ' open' : ''}><summary>${group}</summary>${links}</details>`;
    }
    return `<div class="nav-group"><h2>${group}</h2>${links}</div>`;
  }).join('');
  return `<aside class="docs-sidebar"><button class="docs-menu-toggle" type="button" aria-expanded="false" aria-controls="docs-navigation">Browse documentation <span aria-hidden="true">+</span></button><nav id="docs-navigation" aria-label="Documentation">${navigation}</nav></aside>`;
}

export function article(page, html, pages, base, attachments = '') {
  const toc = page.headings.filter((heading) => heading.level <= 3);
  const docs = page.group !== null;
  const outline = toc.length ? `<aside class="page-toc"><details open><summary>On this page</summary><nav aria-label="On this page">${toc.map((heading) => `<a class="toc-level-${heading.level}" href="#${escape(heading.id)}">${escape(heading.label)}</a>`).join('')}</nav></details></aside>` : '';
  return `<main id="main" class="${docs ? 'docs-layout' : 'guide-layout'}">${docs ? sidebar(pages, page, base) : ''}<article class="doc-article"><div class="article-meta"><a href="${base}${docs ? 'docs/' : ''}">${docs ? 'Documentation' : 'Watermelon'}</a><span aria-hidden="true">/</span><span>${escape(page.title)}</span></div>${page.source === 'docs/UX_BACKLOG.md' ? '<aside class="notice">This is a contributor backlog. Proposed items do not describe current product behavior.</aside>' : ''}<div class="prose">${html}</div>${attachments}<div class="article-footer"><a href="${repository}/edit/main/${page.source}">${icon('code')} Edit this page on GitHub</a><a href="${base}docs/">All documentation</a></div></article>${outline}</main>`;
}
