const base = document.body.dataset.base;
const dialog = document.querySelector('.search-dialog');
const input = document.querySelector('#search-input');
const results = document.querySelector('#search-results');
const status = document.querySelector('#search-status');
let indexPromise;
let queryVersion = 0;

for (const key of document.querySelectorAll('.search-trigger kbd')) {
  key.textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? '⌘ K' : 'Ctrl K';
}

function openSearch() {
  if (!dialog.open) dialog.showModal();
  input.focus();
  if (!indexPromise) indexPromise = fetch(`${base}search-index.json`).then((response) => {
    if (!response.ok) throw new Error('Search index unavailable');
    return response.json();
  }).catch((error) => { indexPromise = undefined; throw error; });
  // Observe loading errors even if the user has not typed yet.
  indexPromise.catch(() => { status.textContent = 'Search could not load. Try again or browse the documentation.'; });
}
document.querySelectorAll('.search-trigger').forEach((button) => button.addEventListener('click', openSearch));
document.querySelector('.close-search').addEventListener('click', () => dialog.close());
dialog.addEventListener('click', (event) => { if (event.target === dialog) dialog.close(); });
document.addEventListener('keydown', (event) => {
  if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'k') {
    event.preventDefault();
    if (dialog.open) dialog.close(); else openSearch();
  }
});

input.addEventListener('input', async () => {
  const version = ++queryVersion;
  const query = input.value.trim().toLowerCase();
  results.replaceChildren();
  if (!query) {
    status.textContent = 'Search across all guides, references, and examples.';
    return;
  }
  status.textContent = 'Searching…';
  try {
    if (!indexPromise) openSearch();
    const index = await indexPromise;
    if (version !== queryVersion) return;
    const terms = query.split(/\s+/);
    const matches = index.map((entry) => {
      const title = `${entry.page} ${entry.title}`.toLowerCase();
      const body = entry.text.toLowerCase();
      if (!terms.every((term) => title.includes(term) || body.includes(term))) return null;
      const score = terms.reduce((total, term) => total + (entry.title.toLowerCase().includes(term) ? 8 : 0) + (entry.page.toLowerCase().includes(term) ? 4 : 0), 0);
      return { ...entry, score };
    }).filter(Boolean).sort((a, b) => b.score - a.score).slice(0, 15);
    status.textContent = matches.length ? `${matches.length} result${matches.length === 1 ? '' : 's'}${matches.length === 15 ? ' (showing the first 15)' : ''}` : 'No results. Try a command, a setting, or a shorter phrase.';
    for (const match of matches) {
      const link = document.createElement('a');
      link.className = 'search-result';
      link.href = match.url;
      const page = document.createElement('small');
      page.textContent = match.page;
      const title = document.createElement('strong');
      title.textContent = match.title;
      const excerpt = document.createElement('span');
      const position = Math.max(0, match.text.toLowerCase().indexOf(terms[0]) - 50);
      excerpt.textContent = `${position ? '…' : ''}${match.text.slice(position, position + 180)}${match.text.length > position + 180 ? '…' : ''}`;
      link.append(page, title, excerpt);
      link.addEventListener('click', () => dialog.close());
      results.append(link);
    }
  } catch {
    if (version === queryVersion) status.textContent = 'Search could not load. Close this window and try again, or browse the documentation.';
  }
});

for (const button of document.querySelectorAll('.copy-code')) {
  button.addEventListener('click', async () => {
    const code = button.closest('.code-block').querySelector('code').textContent;
    try {
      await navigator.clipboard.writeText(code);
      button.textContent = 'Copied';
      document.querySelector('#copy-status').textContent = 'Code copied to clipboard.';
      setTimeout(() => { button.textContent = 'Copy'; }, 1800);
    } catch {
      button.textContent = 'Select code to copy';
      document.querySelector('#copy-status').textContent = 'Clipboard unavailable. Select and copy the code manually.';
    }
  });
}

const tabs = [...document.querySelectorAll('.demo-tabs [role="tab"]')];
function selectTab(tab) {
  for (const other of tabs) {
    const selected = other === tab;
    other.setAttribute('aria-selected', String(selected));
    other.tabIndex = selected ? 0 : -1;
    document.getElementById(other.getAttribute('aria-controls')).hidden = !selected;
  }
}
for (const tab of tabs) {
  tab.addEventListener('click', () => selectTab(tab));
  tab.addEventListener('keydown', (event) => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const next = event.key === 'Home' ? tabs[0] : event.key === 'End' ? tabs.at(-1) : tabs[(tabs.indexOf(tab) + (event.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length];
    selectTab(next);
    next.focus();
  });
}

const menu = document.querySelector('.docs-menu-toggle');
if (menu) {
  document.documentElement.classList.add('has-docs-menu');
  menu.addEventListener('click', () => {
    const expanded = menu.getAttribute('aria-expanded') !== 'true';
    menu.setAttribute('aria-expanded', String(expanded));
    menu.querySelector('span').textContent = expanded ? '−' : '+';
  });
}

// Only highlight the outline; normal anchor navigation still works without JS.
const outline = [...document.querySelectorAll('.page-toc a')];
if (outline.length && 'IntersectionObserver' in window) {
  const observer = new IntersectionObserver((entries) => {
    const heading = entries.filter((entry) => entry.isIntersecting).sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)[0];
    if (!heading) return;
    for (const link of outline) {
      if (link.hash === `#${heading.target.id}`) link.setAttribute('aria-current', 'location');
      else link.removeAttribute('aria-current');
    }
  }, { rootMargin: '-90px 0px -65% 0px' });
  for (const link of outline) {
    const heading = document.getElementById(decodeURIComponent(link.hash.slice(1)));
    if (heading) observer.observe(heading);
  }
}
