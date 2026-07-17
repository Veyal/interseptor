# Packaging

Distribution for Homebrew and Scoop. Core release binaries always ship via
GitHub Releases + GoReleaser; package-manager publish is **optional** and never
blocks a release.

## Install (once taps are seeded)

```bash
# macOS / Linux (Homebrew)
brew install Veyal/tap/interseptor
```

```powershell
# Windows (Scoop)
scoop bucket add Veyal https://github.com/Veyal/scoop-bucket
scoop install interseptor
```

Until the tap/bucket have a formula for the latest tag, use the
[Releases](https://github.com/Veyal/interseptor/releases) page or
`interseptor update`.

## How publish works

1. Create (public) repos:
   - `Veyal/homebrew-tap`
   - `Veyal/scoop-bucket`
2. Add repository secrets on **interseptor**:
   - `HOMEBREW_TAP_TOKEN` — PAT with `contents:write` on `homebrew-tap`
   - `SCOOP_BUCKET_TOKEN` — PAT with `contents:write` on `scoop-bucket`
     (or one PAT for both)
3. On each GitHub Release, workflow
   [`packaging/scripts/publish-packages.sh`](scripts/publish-packages.sh)
   (run after a release, or from CI with a `workflow`-scoped token).

If those secrets are unset, the workflow exits 0 and skips — releases stay green.

## Local templates

`homebrew/interseptor.rb` and `scoop/interseptor.json` are reference shapes.
The publish workflow writes live versions into the tap/bucket repos (not these
files). Keep the templates in sync when formula fields change.
