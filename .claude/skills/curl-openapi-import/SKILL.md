---
name: curl-openapi-import
description: Conventions and gotchas for the curl and OpenAPI/Swagger importers (internal/collimport/curl, internal/collimport/openapi): bounds, ref cycles, YAML alias bombs, secrets, and the Postman-shaped columns they emit.
---

# curl + OpenAPI importers

- Both emit Postman-v2.1-shaped columns (url object with `raw`/`host`/`path`/`query`/`variable`, headers `[{key,value,disabled}]`, body `{mode,...}`, auth `{type, <type>:[{key,value,type}]}`, examples as `response[]`) so `collexec` and the Postman exporter treat every source alike. Reuse `postman.Report/Entry/Level`, `postman.OMap`, `postman.Marshal` (no HTML escaping). `Report.add/finish` are unexported there, so each package appends entries and has its own `finishReport`.
- Nothing is executed, fetched or read from disk. `@file` data/forms and `-T` become `needs-asset` rows with an empty `src`; external `$ref` (anything not starting with `#`) is reported `blocked`/`path-ref`, never fetched.
- Secrets: curl `-u`, `Authorization`/`Cookie`/token headers and URL userinfo count as `EmbeddedCredentials` and yield a `needs-review` entry that never contains the value. OpenAPI security schemes become auth objects that reference `{{<scheme>_token}}`-style secret variables with a blank initial value; scheme extensions and examples never copy credentials.
- Bounds are part of the contract, each with a test: curl 4 MiB / 200k tokens / 5000 commands / 64 KiB URL; OpenAPI 32 MiB / 2M nodes / depth 128 / 200k alias-expanded nodes / 20000 operations / 5000 nodes and depth 12 per generated example / ref chain 32. When adding a code path that expands data (aliases, `$ref`, `allOf`), count it against a budget.
- `$ref` handling: `deref` follows chains with a loop and length guard; example generation keeps a ref stack and cuts cycles (reported once per operation as `ref-cycle`). A cycle is a degraded example, never an error.
- JSON specs are decoded with a token walker into `*omap` (key order matters for tag/path/response order and examples). YAML goes through `yaml.Node` into the same tree; do not `yaml.Unmarshal` into `map[string]any` (loses order, expands aliases unbounded).
- Tokenizer fuzz invariants: never panic, separators are never first/last/adjacent, output size is bounded by the input. Shell expansions (`$VAR`, `$(..)`, backticks) stay literal and flag the command `needs-review`.
- Goldens: `go test ./internal/collimport/curl ./internal/collimport/openapi -update` rewrites `testdata/*.golden.json` with a deterministic id counter; review the diff before committing. Fixtures use example.com only.
