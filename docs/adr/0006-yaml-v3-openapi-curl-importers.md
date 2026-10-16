# ADR 0006: gopkg.in/yaml.v3 for the OpenAPI importer

Status: accepted (pre-approved by the owner, see ADR 0002). Date: 2026-10-07.

| Module | Version | Licence | Role |
|---|---|---|---|
| gopkg.in/yaml.v3 | v3.0.1 | MIT + Apache-2.0 (libyaml port) | parse YAML OpenAPI/Swagger documents (direct) |
| github.com/kr/text, github.com/rogpeppe/go-internal | v0.2.0 / v1.16.0 | MIT / BSD-3-Clause | indirect, test-only edges added by `go mod tidy` |

Pure Go, no cgo (`CGO_ENABLED=0` builds). Measured size delta of a minimal program that decodes into `yaml.Node` versus an empty program, stripped: about 0.65 MiB.

Usage rules: decode into `yaml.Node` (never into `map[string]any`), walk it with node, depth and alias-expansion budgets (`internal/collimport/openapi/node.go`), treat anchors/aliases as hostile input. JSON documents bypass yaml.v3 and use the token decoder so key order and big numbers are preserved.
