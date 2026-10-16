---
name: postman-lossless-roundtrip
description: Conventions and gotchas for the Postman importer/exporter (internal/collimport/postman, internal/collexport/postman) so imports stay lossless and scripts stay quarantined.
---

# Postman import/export

- Lossless means: ordered keys (`postman.OMap`, raw values kept byte-exact), unknown keys in `Sidecar.Extra`, original `variable[]`/`values[]` arrays in the sidecar (secret values blanked), exec line arrays verbatim. The exporter overlays the variable table and edited columns on those originals.
- Never `json.Marshal` raw values on the export path: it HTML-escapes `<>&` and breaks the golden byte test. Use `postman.Marshal` / `MarshalArray`.
- Request descriptions live in `request.description` for most collections; the importer maps them to `Item.DescriptionMD` and sets `ItemSidecar.DescInRequest`.
- Scripts are analysed statically (`scripts.go`), never executed or translated; the importer never writes script trust. Every script gets a `blocked/script-quarantined` report entry.
- Secret-type variables: initial value blanked, the original value goes only to `Result.SecretValues` (json:"-") for a local current value. The report never contains literal credentials, only paths.
- Exports that leave the machine must take their bundle from `store.ExportCollectionsBundle` (the one scrub). The exporter only blanks secret variables on top; `TestCanaryThroughStoreScrub` proves the chain.
- Round-trip tests: `TestGoldenRoundTripBytes` (export(import(x)) == compact(x) apart from blanked secrets) and `TestFieldCoverage` (every fixture key path survives). Add new v2.1 keys to the fixture first.
- Compare stored RawMessages as JSON in tests; nil and empty are different under reflect.DeepEqual.
