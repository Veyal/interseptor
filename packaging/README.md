# Packaging

Distribution templates for package managers. These are kept here so a maintainer
(or goreleaser, once `brews`/`scoops` are enabled in `.goreleaser.yaml`) can
publish Interseptor to Homebrew, Scoop, etc. without reinventing the metadata.

## Homebrew

`homebrew/interseptor.rb` is a formula template. To serve it from a tap:

1. Create a `homebrew-tap` repo under the org (e.g. `Veyal/homebrew-tap`).
2. Enable the `brews:` block in `.goreleaser.yaml` (currently commented),
   pointing `repository.owner`/`name` at the tap.
3. Users then install with `brew install Veyal/tap/interseptor`.

## Scoop (Windows)

`scoop/interseptor.json` is a Scoop manifest. To publish to a bucket:

1. Create a `scoop-bucket` repo.
2. Enable the `scoops:` block in `.goreleaser.yaml`, pointing at the bucket.
3. Users install with:
   ```powershell
   scoop bucket add Veyal https://github.com/Veyal/scoop-bucket
   scoop install interseptor
   ```

The version + SHA256 in these templates are placeholders; goreleaser fills the
real values per release when the blocks are enabled. Until then, the prebuilt
binaries on the GitHub Releases page (and `interseptor update`) are the
supported install paths.
