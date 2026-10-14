---
name: public-documentation
description: Maintain Interseptor's generated documentation and GitHub Pages publication.
---

# Public documentation

- The site is built by our own generator, `tools/docscheck` (no Jekyll, no Liquid). Edit canonical
  guides under `docs/`; register a new public guide in the `publicDocs` map and give it a
  `llmsDescriptions` line. Standalone pages live in `website/pages/` (`.html` Go templates or `.md`)
  with YAML front matter; templates in `website/templates/`; sidebar in `website/data/navigation.yml`;
  feature cards in `website/data/features.yml`, kept aligned with `docs/FEATURES.md`.
- `go run ./tools/docscheck generate` rewrites only `llms.txt`, `llms-full.txt` and the reference block
  in `docs/agents.md`; commit those. `check` validates features, agent files, project links and table
  pipes. The site, search index, sitemap and release badge are build output, never committed.
- Preview with `go run ./tools/docscheck serve` (http://127.0.0.1:4000/interseptor/, rebuilds on
  change; Go code changes need a restart). Validate with `go run ./tools/docscheck build _site` then
  `go run ./tools/docscheck check-site _site`, and inspect real desktop and mobile pages.
- Heading ids follow kramdown's GFM rules so old deep links keep working. Markdown is not templated:
  write `{{name}}` literally. A `|` inside code in a table row must be written `\|`.
- Design: `DESIGN.md` is the contract. Its section 9 lists the shared hooks: per-page `css`/`js`
  front matter, `data-copy`, `data-search-open`, `themechange`, `data-color`, `prose: true`. Tokens
  and primitives live only in `website/assets/site.css`; surfaces use `css/chrome.css`,
  `css/docs.css`, `css/landing.css` and `css/pages.css`, and never raw hex.
- Go templates fail on an unknown struct field, so add a field to the `View` contract in
  `tools/docscheck/site.go` before a template uses it. `asset` and `icon` fail the build on a
  missing file. Icons are lucide-static 0.453.0 SVGs fetched into `website/assets/icons/`; never
  draw icon paths by hand.
- Product media must be real: record from the binary on a throwaway data dir with synthetic
  `*.example.com` traffic answered by a loopback mock. Mascot rasters come from
  `website/assets/img/mascot/src/*.svg`.
- The site identity (`https://veyal.github.io`, base `/interseptor`) lives in `tools/docscheck/site.go`
  and must match `version.DocsSite`.
- A passing PR does not prove deployment: the deploy job runs on main pushes. If Pages returns
  404 and `configure-pages` fails to enable it, check repository `has_pages` and Pages settings.
  `GITHUB_TOKEN` can deploy but cannot provision the site; existing authorized admin access is
  needed to restore GitHub Actions build mode. Verify the workflow and public pages afterward.
