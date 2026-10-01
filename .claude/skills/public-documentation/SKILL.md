---
name: public-documentation
description: Maintain Interseptor's generated documentation and GitHub Pages publication.
---

# Public documentation

- Edit canonical guides under `docs/`, register new public guides in `tools/docscheck`, and run
  `go run ./tools/docscheck generate`. Root guide Markdown, search data, and release metadata are
  generated files. Keep `_data/features.yml`, `docs/FEATURES.md`, and sidebar navigation aligned.
- Keep `_config.yml` `url` at `https://veyal.github.io` and `baseurl` at `/interseptor`; putting the
  path in both doubles canonical and Open Graph URLs.
- Use the workflow's Jekyll image for rendered validation. Stage tracked files plus intended
  changes, excluding ignored local binaries and session artifacts. Validate with
  `go run ./tools/docscheck check-site <output>` and inspect actual desktop/mobile pages.
- A passing PR does not prove deployment: the deploy job runs on main pushes. If Pages returns
  404 and `configure-pages` fails to enable it, check repository `has_pages` and Pages settings.
  `GITHUB_TOKEN` can deploy but cannot provision the site; existing authorized admin access is
  needed to restore GitHub Actions build mode. Verify the workflow and public pages afterward.
