# Watermelon website

The static GitHub Pages website introduces Watermelon, explains its use cases,
and publishes the repository's technical documentation. It needs Node.js 24+
for building; the deployed site needs no application server. Fonts and search
are served locally, with no external scripts or services.

## Work on the website

From the repository root:

```bash
cd site
npm ci
npm run dev
```

Open the printed local URL (normally `http://127.0.0.1:4321/`). The preview
rebuilds when website sources, docs, the root README, or the changelog change.
Stop it with Ctrl+C.

```bash
npm run check  # Build and check both / and /watermelon/ layouts
npm run build  # Generate dist/
```

To preview the project Pages path locally:

```bash
PAGES_BASE_PATH=/watermelon/ npm run dev
```

`npm run check` verifies that every article is generated and indexed, every
internal URL and section anchor resolves, and downloads and local font assets
exist. It also checks unique HTML IDs and one primary heading per page.

## Content and design

- `../docs/**/*.md` is the source of truth for the technical docs. Edit those
  files directly; do not copy them into the website. New Markdown guides and
  examples are discovered automatically.
- `../README.md`, `../CHANGELOG.md`, and `../skills/watermelon/SKILL.md` are also
  published. Agent frontmatter is omitted from the rendered article.
- `content/use-cases.md` contains the visitor-facing workflow guide.
- `lib/content.mjs` defines titles, reading order, routes, Markdown rendering,
  and repository-relative link resolution. Its fallback includes new guides
  automatically. Heading IDs match GitHub's slug conventions.
- `lib/home.mjs` contains the landing page; `lib/layout.mjs` contains shared
  navigation and article layouts.
- `public/style.css` and `public/site.js` provide the responsive design,
  keyboard-accessible search, copy buttons, tabs, and mobile docs navigation.
- Example `.watermelon.toml` files and helper scripts appear as expandable
  code previews and downloadable files on their example pages.
  Configuration URLs use `watermelon.toml` because Pages does not serve dotfiles;
  the download attribute preserves the intended `.watermelon.toml` filename.

The palette uses slate (`#192b43`), pale blue (`#edf3fa`), soft blue
(`#c4d6ed`), amber (`#f1bb55`), and cobalt (`#26558c`) on a cool white background.
Archivo carries the display headings, DM Sans the reading text, and IBM Plex
Mono the code. Font files and their licenses are supplied by Fontsource.
The custom SVG watermelon mark is the only illustration asset; the VM diagram
is HTML and CSS. Animation respects reduced-motion preferences.

## Publish on GitHub Pages

1. In the repository's **Settings → Pages → Build and deployment**, select
   **GitHub Actions** as the source. This is a one-time repository setting.
2. Merge the website changes into `main`. The **Website** workflow builds,
   verifies, and deploys the site. It also runs build checks on pull requests,
   without deployment permissions or deployment from PR code.
3. For later manual deploys, run **Actions → Website → Run workflow** on `main`.

The expected project URL is `https://somospollo.github.io/watermelon/`.
The workflow uses `actions/configure-pages` outputs for the actual origin and
base path, so custom domains can use the root path. `SITE_ORIGIN` overrides the
origin when building outside Actions; `PAGES_BASE_PATH` sets the URL prefix.

`dist/` and `node_modules/` are ignored. Publish the built artifact through
Actions; no generated documentation or deployment branch is checked in.
