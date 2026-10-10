# Collections

Collections are saved, organised requests with variables, environments, auth, scripts, a runner and
a CI command. They replace the "author in Postman, then import" step: build or import a collection,
send it through the same scope guard and capture path as every other tool, and keep the evidence in
History. Open **Collections** in the Test group (on a phone, use the **All tools** select).

Everything here runs in the single Interseptor binary. There is no Node, no Postman runtime and no
cloud account. Scripts run on a pure-Go JavaScript engine ([ADR 0001](adr/0001-script-engine-goja.md))
behind a `pm.*` compatibility layer.

- [Migrate from Postman](#migrate-from-postman)
- [Variables and scopes](#variables-and-scopes)
- [Requests and auth](#requests-and-auth)
- [Scripts and pm parity](#scripts-and-pm-parity)
- [Quarantine and trust](#quarantine-and-trust)
- [Runner](#runner)
- [CLI and CI](#cli-and-ci)
- [Importers](#importers)
- [Security model for imported scripts](#security-model-for-imported-scripts)
- [Secrets and archives](#secrets-and-archives)
- [Differences from Postman](#differences-from-postman)

## Migrate from Postman

1. In Postman, export the collection (Collection v2.1 is best; v2.0 also imports) and, if you use
   them, each environment and your globals. Keep the files as engagement material.
2. In Interseptor open **Collections → Import**, drop the collection file (or several files), and
   choose **Preview**. Nothing is stored or run by a preview. `format=auto` detects Postman,
   OpenAPI/Swagger, curl, Insomnia, Bruno, HAR and Burp XML.
3. Read the import report. Every construct is labelled `converted`, `degraded`, `preserved-inert`,
   `unsupported`, `blocked` or `needs-review`, with the path of the item it came from. Every literal
   credential in the file (auth, headers, URL query, body) gets its own `embedded-credential` entry
   naming the field, with a suggestion to lift it to a variable. The report never contains credential
   values. `scriptsQuarantined` is true only when the file actually carried scripts.
4. Choose **Commit**. Folders, requests, examples, auth, variables, events and unknown keys are
   stored losslessly. **Credentials you import are kept**: a token, password or API key written
   literally in the file stays in the stored request so it still authenticates when sent (export is
   where credentials are scrubbed, see [Secrets and archives](#secrets-and-archives)). Secret-typed
   variables arrive with a blank initial value; if the file carried a value it is kept only as a local
   current value on this machine. Importing the same file again never duplicates requests: requests
   already present (same place, name, method, URL and body) are skipped, and each skip is listed in
   the report. Two same-named sibling folders, or two requests in one folder that differ only in body,
   stay separate. A UTF-8 byte order mark is ignored. A Postman data dump (several collections in one
   file) and Postman collection v1 are refused with a message telling you to export each collection as
   v2.1.
5. Import the environment files the same way. Each becomes an environment, and secret values stay
   local. Globals become the globals environment.
6. Set the scope. Open **Scope** and include the hosts the collection targets. Runs, scripts and
   MCP sends are blocked outside scope (see [scope policy](#security-model-for-imported-scripts)).
7. Imported scripts are quarantined: the requests run, the scripts do not. Open **Scripts**,
   read each one and its analysis (APIs used, hosts it names, risky constructs), then trust the ones
   you accept. See [Quarantine and trust](#quarantine-and-trust).
8. Send one request, then run the folder. Compare results with Postman. Anything a script needs
   that is not shipped fails with status `unsupported` and names the API, never a false pass.

The older **Import Postman to Repeater tabs** button is unchanged. It flattens a collection into
editable Repeater tabs and does not create a collection. Use it when you only want a few requests in
Repeater.

Postman Collection v3 files, Postman cloud workspaces and monitors are not supported (see
[Differences](#differences-from-postman)).

## Variables and scopes

Variables use double braces around a name: {% raw %}`{{token}}`{% endraw %}. One resolver serves the UI, runner, CLI,
MCP and scripts, so a preview always matches what is sent.

Precedence, narrowest to widest:

| Layer | Source |
| --- | --- |
| local | Values a script sets with `pm.variables.set` for the current request |
| data | The current row of the iteration data file (runner and CLI) |
| environment | The selected environment |
| folder | Variables on the item's folders, inner folder first |
| collection | Variables on the collection |
| global | The globals environment |

Rules that differ from what you may expect:

- **Unresolved variables block the send** by default. The request is not sent with a literal
  {% raw %}`{{name}}`{% endraw %} on the wire. A request or folder can opt into `literal` or `empty` explicitly in its settings.
- **Initial and current values.** The initial value is shared (it travels in exports). The current
  value is local to this machine and is what scripts write. Secret-typed variables have no initial
  value at all: the value lives only in the local current-value table.
- **Variables that look like credentials are secrets.** A script that writes `token`, `password`,
  `api_key` or similar creates a secret-typed variable automatically, so listings mask it and exports
  blank it.
- **Dynamic values** `$guid`, `$randomUUID`, `$timestamp`, `$isoTimestamp`, `$randomInt`,
  `$randomAlphaNumeric`, `$randomBoolean`, `$randomFirstName`, `$randomLastName`, `$randomFullName`,
  `$randomEmail`, `$randomUserName`, `$randomIP`, `$randomPassword`, `$randomHexColor`, `$randomWord`
  and `$randomPhoneNumber` are generated per use. Time and randomness come from an injected clock
  and random source, so runs can be made reproducible.
- **Pipes** transform a value in place: `b64`, `b64url`, `urlenc`, `json`, `md5`, `sha256`,
  `hmac:KEY`, `upper`, `lower`, `trim`.
- **Limits.** Expansion depth is 8, expansions per resolve 10,000, output 1 MiB. Cycles and
  overruns are reported problems, never hangs.
- **Environments** are managed from the **Environments** button in the Collections toolbar: create,
  rename, duplicate or delete one, with the variable count and the active environment marked. When a
  collection has none, a **New environment** button sits beside the picker. Duplicating copies
  variable names and initial values only, never a secret or a locally held current value. They can
  be bound to an identity label and carry a base-target pin so a mis-selected environment cannot
  send to a different target.

## Requests and auth

A request is Postman-shaped: method, URL (host, path, query, path variables), headers, params, body
(raw, urlencoded, multipart, GraphQL, binary placeholders), auth, saved examples, scripts and
per-item settings (redirects, timeout, TLS verification, unresolved policy, codec).

- **Auth types:** basic, bearer, API key, JWT signing (HS/RS/ES), digest, AWS SigV4 (header signing),
  OAuth 2.0 (client credentials, password, refresh with auto-refresh, authorization code with PKCE)
  and mTLS client configuration. Auth is applied after scripts and variable resolution; an explicit
  `Authorization` header always wins.
- **OAuth tokens** are cached per collection, environment and identity, stored locally and never
  returned by an API. Token endpoints go through the same scope guard as ordinary sends. The
  authorization-code callback is served on the control port and shows no token data.
- **Cookies** use a per-collection, per-environment, per-identity jar. Whether a run keeps its
  cookie changes follows the same persist policy as variable writes.
- **Session headers.** Collection requests skip the global session headers (the Authorization or
  Cookie auto-injected into Repeater and Intruder) unless the item opts in, so a collection request
  is reproducible.
- **Codecs.** A [message codec](message-codecs.md) can encode the plaintext body on send.
- **Files.** Local file bodies and multipart file parts are refused: no collection reads from disk.
- **History.** Every send is a normal flow in History, tagged **COLL**, with a **Collections** filter
  chip. `GET /api/flows?collection=0|only` filters it.

Every send path (interactive, runner, CLI, MCP, `pm.sendRequest`, OAuth token fetch) goes through one
pipeline, so scope, dial guard and capture rules are identical.

## Scripts and pm parity

Pre-request and test scripts run on a pure-Go engine with a `pm.*` shim, the legacy `postman.*` /
`tests[]` / `responseBody` globals, `console`, `require()` of a small set of modules, and an additive
`isp.*` namespace. Scripts get a fresh runtime per run, an injected clock and random source, and hard
limits on time, stack, source size, console output, allocation, sends and heap.

The rule is **absent means absent**. The runtime does not stub an unsupported API so a script
"works". An unsupported `pm.*` member, global, module or Chai word stops the script with status
`unsupported` naming the API. That status is distinct from pass and fail, appears in the runner, in
reports (JUnit records it as an `UnsupportedAPI` error, never a pass or a failure) and exits the CLI with code 2. A static
analyser lists unsupported APIs, required modules, literal hosts and risky constructs before a script
runs, from the same data.

The table below is generated from `internal/pmsandbox/pm_coverage.json` and tested against the running
shim, so it cannot drift.

<!-- pm-parity:begin (generated from internal/pmsandbox/pm_coverage.json; run UPDATE_DOCS=1 go test ./internal/pmsandbox -run TestDocsParityTable) -->

| Object | Supported members |
| --- | --- |
| `pm` | `info`, `environment`, `globals`, `collectionVariables`, `iterationData`, `variables`, `request`, `response`, `cookies`, `test`, `expect`, `sendRequest`, `execution`, `visualizer` |
| `pm.collectionVariables` | `get`, `set`, `unset`, `has`, `clear`, `toObject`, `replaceIn`, `toJSON` |
| `pm.cookies` | `get`, `has`, `toObject`, `jar` |
| `pm.environment` | `name`, `get`, `set`, `unset`, `has`, `clear`, `toObject`, `replaceIn`, `toJSON` |
| `pm.execution` | `setNextRequest`, `skipRequest` |
| `pm.globals` | `get`, `set`, `unset`, `has`, `clear`, `toObject`, `replaceIn`, `toJSON` |
| `pm.info` | `eventName`, `iteration`, `iterationCount`, `requestName`, `requestId` |
| `pm.iterationData` | `get`, `has`, `toObject`, `replaceIn`, `toJSON` |
| `pm.request` | `name`, `id`, `method`, `url`, `headers`, `body`, `auth`, `getHeaders`, `addHeader`, `removeHeader`, `upsertHeader`, `update`, `toJSON` |
| `pm.request.body` | `mode`, `raw`, `urlencoded`, `formdata`, `options`, `isEmpty`, `toString`, `update`, `toJSON` |
| `pm.request.headers` | `get`, `one`, `has`, `add`, `append`, `prepend`, `upsert`, `remove`, `clear`, `count`, `all`, `idx`, `each`, `map`, `filter`, `find`, `reduce`, `populate`, `toObject`, `members`, `toJSON` |
| `pm.request.url` | `protocol`, `host`, `port`, `path`, `query`, `hash`, `auth`, `update`, `getHost`, `getPath`, `getQueryString`, `getPathWithQuery`, `getRemote`, `addQueryParams`, `removeQueryParams`, `toString`, `toJSON` |
| `pm.response` | `code`, `status`, `headers`, `responseTime`, `responseSize`, `cookies`, `text`, `json`, `reason`, `size`, `to`, `toJSON` |
| `pm.response.cookies` | `get`, `one`, `has`, `each`, `map`, `filter`, `find`, `count`, `all`, `idx`, `toObject`, `members`, `add`, `append`, `prepend`, `upsert`, `remove`, `clear`, `populate`, `reduce`, `toJSON` |
| `pm.response.headers` | `get`, `one`, `has`, `each`, `map`, `filter`, `find`, `count`, `all`, `idx`, `toObject`, `members`, `add`, `append`, `prepend`, `upsert`, `remove`, `clear`, `populate`, `reduce`, `toJSON` |
| `pm.response.to` | assertion chain: `status`, `header`, `jsonBody`, `body`, `ok`, `success`, `info`, `redirection`, `clientError`, `serverError`, `error`, `accepted`, `badRequest`, `unauthorized`, `forbidden`, `notFound`, `rateLimited`, `withBody`, `json` |
| `pm.variables` | `get`, `set`, `unset`, `has`, `clear`, `toObject`, `replaceIn` |

Chai `pm.expect()` words: `equal`, `equals`, `eq`, `eql`, `eqls`, `include`, `includes`, `contain`, `contains`, `property`, `ownProperty`, `a`, `an`, `instanceof`, `instanceOf`, `above`, `gt`, `greaterThan`, `below`, `lt`, `lessThan`, `least`, `gte`, `greaterThanOrEqual`, `most`, `lte`, `lessThanOrEqual`, `within`, `closeTo`, `approximately`, `lengthOf`, `length`, `oneOf`, `match`, `matches`, `string`, `keys`, `key`, `members`, `satisfy`, `throw`, `throws`, `ok`, `true`, `false`, `null`, `undefined`, `NaN`, `exist`, `exists`, `empty`.

Globals: `pm`, `postman`, `tests`, `responseBody`, `responseCode`, `responseHeaders`, `responseCookies`, `responseTime`, `environment`, `globals`, `data`, `iteration`, `request`, `console`, `CryptoJS`, `Buffer`, `atob`, `btoa`, `TextEncoder`, `TextDecoder`, `URL`, `URLSearchParams`, `require`, `setTimeout`, `setInterval`, `clearTimeout`, `clearInterval`, `setImmediate`, `performance`, `Promise`, `JSON`, `Math`, `Date`, `isp`.

`require()` modules: `crypto`, `crypto-js`, `buffer`, `uuid`, `url`, `chai`, `querystring`, `atob`, `btoa`, `util`.

| Not shipped | Result |
| --- | --- |
| `pm.execution.location` (pm API) | `unsupported`: pm.execution.location is not shipped |
| `pm.execution.runRequest` (pm API) | `unsupported`: pm.execution.runRequest is not shipped |
| `pm.iterationData.clear` (pm API) | `unsupported`: pm.iterationData is read-only |
| `pm.iterationData.set` (pm API) | `unsupported`: pm.iterationData is read-only |
| `pm.iterationData.unset` (pm API) | `unsupported`: pm.iterationData is read-only |
| `pm.request.certificate` (pm API) | `unsupported`: client certificates are configured per item, not from scripts |
| `pm.require` (pm API) | `unsupported`: pm.require (package library) is not shipped |
| `pm.response.stream` (pm API) | `unsupported`: streamed response bodies are not shipped |
| `pm.response.to.have.jsonSchema` (pm API) | `unsupported`: JSON-schema assertions (ajv/tv4) are not shipped |
| `pm.response.to.have.jsonSchemaValidate` (pm API) | `unsupported`: JSON-schema assertions (ajv/tv4) are not shipped |
| `pm.vault` (pm API) | `unsupported`: pm.vault is not shipped |
| `WebSocket` (global) | `unsupported`: network egress is only available through pm.sendRequest |
| `XMLHttpRequest` (global) | `unsupported`: network egress is only available through pm.sendRequest |
| `_` (global) | `unsupported`: lodash is not shipped (deferred, data-driven) |
| `ajv` (global) | `unsupported`: ajv is not shipped |
| `cheerio` (global) | `unsupported`: cheerio is not shipped (deferred, data-driven) |
| `fetch` (global) | `unsupported`: network egress is only available through pm.sendRequest |
| `moment` (global) | `unsupported`: moment is not shipped (deferred, data-driven) |
| `process` (global) | `unsupported`: no process access in the sandbox |
| `tv4` (global) | `unsupported`: tv4 is not shipped |
| `xml2Json` (global) | `unsupported`: xml2Json is not shipped |
| `ajv` (module, deferred) | `unsupported`: ajv is not shipped |
| `cheerio` (module, deferred) | `unsupported`: cheerio is not shipped (deferred, data-driven) |
| `csv-parse/lib/sync` (module, deferred) | `unsupported`: csv-parse is not shipped |
| `lodash` (module, deferred) | `unsupported`: lodash is not shipped (deferred, data-driven) |
| `moment` (module, deferred) | `unsupported`: moment is not shipped (deferred, data-driven) |
| `postman-collection` (module, deferred) | `unsupported`: postman-collection is not shipped |
| `tv4` (module, deferred) | `unsupported`: tv4 is not shipped |
| `xml2js` (module, deferred) | `unsupported`: xml2js is not shipped (deferred, data-driven) |
| `child_process` (module, never) | `unsupported`: no process access |
| `cluster` (module, never) | `unsupported`: no process access |
| `dgram` (module, never) | `unsupported`: no raw sockets |
| `dns` (module, never) | `unsupported`: no DNS access |
| `fs` (module, never) | `unsupported`: no filesystem access |
| `http` (module, never) | `unsupported`: network egress is only available through pm.sendRequest |
| `http2` (module, never) | `unsupported`: network egress is only available through pm.sendRequest |
| `https` (module, never) | `unsupported`: network egress is only available through pm.sendRequest |
| `net` (module, never) | `unsupported`: no raw sockets |
| `os` (module, never) | `unsupported`: no host information |
| `path` (module, never) | `unsupported`: not available |
| `stream` (module, never) | `unsupported`: not available |
| `tls` (module, never) | `unsupported`: no raw sockets |
| `vm` (module, never) | `unsupported`: no code loading |
| `worker_threads` (module, never) | `unsupported`: no threads |
| `zlib` (module, never) | `unsupported`: not available |
| `pm.visualizer` (stub) | `unsupported`: pm.visualizer is a no-op stub; visualizer output is not rendered |

<!-- pm-parity:end -->

Scripts can send requests with `pm.sendRequest`. The send uses the collection pipeline: scope policy,
dial guard, send budget and flow capture all apply, and the request needs the `net.send` capability.

## Quarantine and trust

Imported, merged, vault-restored, MCP-supplied and CLI-supplied scripts are **quarantined**: the
request still runs, the script does not, and the result says how many scripts were skipped.

- **Trust is bound to content.** The trust hash covers the exact source, the loaded libraries and the
  capability set. Any edit from an import, merge or AI write resets it.
- **Only a person in the UI can trust.** Open **Collections → Scripts**, review each script's source
  and analysis, pick the capabilities to grant and confirm. AI, MCP, API-key and agent callers, and
  archives, can never trust a script or set a capability (HTTP 403). There is no MCP trust tool.
  The UI session is identified by a fixed request header, not a per-session secret, so a local shell
  process that can reach the control port and omits the agent headers is inside the trust boundary
  (it is the same user on the same machine). Callers that label themselves AI, MCP or API are refused.
- **Your own edits** typed in the UI are trusted when their hash is new. A rename or an AI edit never
  approves an existing script.
- **Capabilities are default-deny**, per collection: `vars.read`, `vars.write`, `cookies.read`,
  `cookies.write`, `net.send`, `secrets.read`. Sending outside scope is not grantable.
- **Revoke** from the same sheet at any time. Trust is never exported, imported or merged.

## Runner

**Run** (collection, folder or a selection) starts a run that belongs to the server, so closing the
view does not stop it. The view follows it live with a progress bar, per-request results with their
tests and console, and Pause, Resume and Abort.

Before anything is sent, the Run sheet shows the plan: its scope in the title (`Folder "Users"` or
`Whole collection "Demo API"`), the number of live requests, a per-method breakdown with
state-changing methods marked, and the target hosts. A run that contains a POST, PUT, PATCH or
DELETE needs `RUN` typed to confirm, because those can change state on the target. The same review
gates the identity matrix, which multiplies the request count by every identity it runs as.

A request's verdict distinguishes what was asserted from what merely happened:

| Verdict | Meaning |
| --- | --- |
| Passed | Assertions ran and all of them passed |
| Sent 200 | Sent, nothing asserted, the response was below 400 |
| 401 Unauthorized | Sent, nothing asserted, the response was 400 or above |
| Failed | An assertion failed |
| Error, Blocked, Skipped, Unsupported | As before |

Only an assertion can produce a pass, so an imported collection with no tests reports what the
target actually returned instead of a column of green rows. The run summary counts the groups apart,
and 4xx/5xx rows appear under **Show problems only** and **Rerun failed**.

- **Iterations and data.** Upload a CSV or JSON data file (max 10,000 rows, 32 MiB). Each row is one
  iteration and its columns are the `data` variable layer. The run records the data file's hash.
- **Options:** stop on first failure or error, delay between requests, requests per second, rerun
  only the failures of a previous run, no scripts.
- **Persist.** Script variable and cookie writes follow a policy: `ask` (the default for the UI; a
  prompt after the run offers keep or discard), `keep` or `discard`. Discard is exact: writes go to an
  overlay, so token chains still work inside the run and nothing is left behind.
- **Order.** `pm.execution.setNextRequest` is honoured with a loop guard (a hard cap, never a hang).
- **Reports.** A finished run produces one model that is scrubbed once and rendered as CLI text,
  JSON, JUnit or HTML. Rows list tests, `unsupported` results and skipped scripts separately, never as
  passes.
- **Scope policy.** Runner, MCP and script sends use the collection's policy, default `block`.
  Interactive single sends default to `warn`.
- **Getting work back.** The editor status line always says whether what you see matches the stored
  request, with **Revert** while it differs. Sending an edited request saves it, and the version it
  replaced is offered as **Restore previous version**. Deleting a request or folder offers **Undo**
  for 15 seconds. Both are in-session only and are lost on reload, and a restored item drops any
  script that was not already trusted.

REST and MCP reach the same runner. See [API and MCP](api-and-mcp.md#collections-api-and-mcp).

## CLI and CI

```text
interseptor run <collection> [options]
interseptor lint <collection> [options]
```

Run a collection headless, for a pipeline or a pre-engagement check:

```text
interseptor run "Orders API" -e staging --scope api.example.com \
  --data users.csv --report cli,junit=results.xml,json=results.json --out reports
```

| Option | Meaning |
| --- | --- |
| `-e, --env` | Environment name or uid |
| `-d, --data FILE` | Iteration data (`.csv` or `.json`); `-n` sets the iteration count |
| `--folder` | Run only one folder |
| `--bail[=on-failure\|on-error]` | Stop at the first failing test, or only at the first runtime error |
| `--delay-request`, `--rps` | Pace the run (`250ms`, `2s`, requests per second) |
| `--env-var K=V` | Set a variable for the whole run (repeatable) |
| `--scope HOSTS` | Extra in-scope hosts; a run refuses to send without any declared scope |
| `--no-scripts` | Run no script at all |
| `--allow-scripts --trust-hash SHA` | Run only scripts whose hash you pin, for this process only |
| `--persist` | Store script variable writes (the CLI default is discard) |
| `--report SPEC` | `cli`, `json=FILE`, `junit=FILE`, `html=FILE`, comma separated; `--out DIR` for bare names |
| `--project`, `--data-dir` | Which project and data directory |

The CLI opens no listeners and never trusts anything by itself. Quarantined scripts do not run; a
script becomes runnable for one process only through `--allow-scripts` with its hash pinned. Run
`interseptor lint` to list the hashes, check undefined variables and unapproved scripts (use
`--strict` to fail on warnings).

**Exit codes** are a contract:

| Code | Meaning |
| --- | --- |
| 0 | Everything passed |
| 1 | One or more tests failed |
| 2 | Runtime, transport, unresolved variable, unsupported API or loop-guard error |
| 3 | Scripts not approved (checked before any request is sent) |
| 4 | A request was blocked by scope |
| 5 | Import/lint error or bad usage |

In CI, run `interseptor lint` first, then `run` with `junit=results.xml`, publish the XML with your
CI's test reporter, and treat a non-zero code as a failed job. The JUnit file validates against the
JUnit 10 schema; unsupported tests are `UnsupportedAPI` errors naming the API, and secrets are masked
everywhere.

## Importers

| Source | Notes |
| --- | --- |
| Postman Collection v2.0/v2.1, environments, globals | Lossless: key order, unknown keys, scripts and variables are preserved |
| OpenAPI 3.x, Swagger 2 (JSON or YAML) | Tags become folders; servers become environments with a `baseUrl` variable; security schemes become auth plus blank secret variables; external `$ref` is reported, never fetched |
| curl commands | Quotes, continuations, Chrome and PowerShell forms; unknown flags reported; `@file` becomes a needs-asset row, never read |
| Insomnia v4 JSON and v5 YAML | Template tags map to variables where they have an equivalent; chained response tags are reported and left unresolved |
| Bruno `.bru` files or a folder | Declarative `assert` rows become assertions; unsupported operators become `unsupported` assertions |
| HAR and Burp saved-items XML | Requests only; header order is not preserved. To keep exact captured bytes use the History import instead |

All importers are bounded (size, nesting, item and alias budgets), never execute or fetch anything,
and quarantine every script. Insomnia and Bruno scripts that use their own APIs (`bru.*`, `insomnia.*`)
are marked `unsupported`: there is no shim for them in this release.

Moving a collection between machines works through the project bundle and the full project archive,
which carry collections in scrubbed form (see [Secrets and archives](#secrets-and-archives)). A
single collection also exports on its own, see [Export](#export).

## Export

The **Export** button next to Import opens a sheet with three downloads for the selected collection:

- **Postman v2.1.** Lossless for collections that came from Postman: key order, unknown keys and
  script text are kept, so importing the file again and exporting it returns the same bytes (apart
  from blanked secrets).
- **curl script.** One `curl` command per request, in tree order. A credential that was removed is
  written as `REDACTED`; `{{variable}}` references are kept for you to substitute.
- **Interseptor JSON** (`.ixcol.json`). The native format with environments and variable
  declarations.

REST: `GET /api/collections/{uid}/export?format=postman|curl|native` returns the file with a
`Content-Disposition` filename. An unknown format is a 400 that lists the supported ones; an unknown
collection is a 404.

**Every export is scrubbed, for every caller.** Tokens, passwords, API keys, secret variable values
and credential-named variables are blanked, script trust and capabilities are not exported, and local
current values, cookies and OAuth tokens never leave. There is no option to include secrets: a
request carrying `secrets`, `includeSecrets`, `reveal`, `scrub` or `raw` is refused with a 400
instead of being ignored. Review the file before you share it; a secret pasted into a request name or
a free-text body field cannot be recognised.

## Security model for imported scripts

An imported collection is untrusted code from someone else. The design assumes a malicious one.

- **Quarantine first.** No script from any import, merge, restore, MCP or CLI source runs until a
  person trusts its exact hash in the UI. Trust is invalidated by any change and is never
  transferable ([ADR 0003](adr/0003-script-trust-model.md)).
- **Default-deny capabilities.** Variables, cookies, sends and secrets each need a grant.
- **No ambient authority.** There is no filesystem, process, environment, DNS, socket, `fetch` or
  `XMLHttpRequest`. `require('fs')`, `child_process`, `net`, `http` and the like are refused. The only
  egress is `pm.sendRequest` through the proxied sender.
- **Process isolation.** Approved scripts run in a separate worker process (`interseptor
  __scriptworker`) with no inherited environment, a memory limit, a wall-clock deadline and a kill on
  breach. The worker never dials; the parent re-checks scope and the send budget. Setting
  `INTERSEPTOR_SCRIPTS_INPROCESS=1` lets owner-trusted scripts run in-process instead.
- **Scope policy.** `block` for runner, CLI, scripts and MCP; `warn` for interactive sends from the UI.
  The collection's own policy can be set by a person, but an AI caller cannot loosen it. Loopback,
  link-local, private (RFC 1918) and 100.64/10 destinations and Interseptor's own listeners are denied
  unless the exact host is in scope, enforced at dial time and again on every redirect hop.
- **Hard limits** on source size, stack, console output, timers, allocations, sends per script and
  heap growth. Time and randomness are injectable for reproducible runs.
- **Honest failure.** Anything the engine cannot run is `unsupported`, not a pass.

Residual risk: a script you trust is trusted code. Review it, grant the smallest capability set, and
prefer the CLI's `--no-scripts` for untrusted collections.

## Secrets and archives

Collections hold secrets (secret variable values, auth credentials, cookies, OAuth tokens). One scrub
function removes them from everything that leaves the machine, unless you explicitly opt in.

- **Archives and vault.** Full project archives (download, file export, merge push) and vault backups
  snapshot the database through the scrubbed copy: secret variables, current values, cookies, tokens,
  auth credentials and script trust are removed from the file itself, not only from a view of it.
  The same scrub blanks literal credentials in a request's URL (userinfo password and secret-named
  query values) and body (secret-named JSON members, form pairs and XML elements); `{{references}}`
  and every other value stay. Importers flag these as embedded credentials.
- **Import keeps your credentials; export scrubs them.** A file you deliberately import keeps its
  literal credentials at rest (the same place real secret values already live), because blanking them
  on the way in silently turned every authenticated request into a 401. Nothing leaves the project
  unscrubbed: archives, vault, project bundle and every export still blank them. Bundles and peer
  projects restored or merged from elsewhere are untrusted and are still scrubbed on the way in.
- **Restore and vault pull** clear script trust, reset collection capabilities to default-deny and
  downgrade scope policy `off` to `block` before the project is installed, so a crafted archive can
  never arrive with scripts already trusted. Re-approve scripts in the UI after a restore.
- **Project bundle** (portable JSON v2) carries a scrubbed `collections` section; scripts arrive
  quarantined, capabilities default-deny.
- **Merge** (peer pull and push, archive and vault merge) unions collections by uid; it never merges
  current values, cookies, tokens, trust or runs, and downgrades scope policy `off` to `block`.
- **MCP and the AI channel** read scrubbed data, get no script source, cannot reveal secrets, and get
  a second masking pass on every response.
- **Reports and logs.** Run reports, step results, console output and test messages are masked with
  the secrets the run used plus common credential shapes. Values shorter than six characters cannot
  be tracked, so use real credentials.

Captured History flows are evidence and keep their exact wire bytes, including an Authorization
header a collection request sent. The AI channel can read those flows (the History list and flow
detail), so a secret a collection request put on the wire is readable through its captured flow even
though the variable itself stays masked; do not treat "cannot reveal secrets" as covering History. Treat History exports, the project bundle's HAR section and
findings evidence as sensitive, as described in [Projects and data](projects-and-data.md#collections-secrets-and-archives).

## Differences from Postman

| Area | Postman | Interseptor |
| --- | --- | --- |
| Unsupported script API | Not applicable (full Node-like sandbox) | Stops with status `unsupported` naming the API; never a pass |
| Script engine | Postman sandbox | Pure-Go engine with a `pm.*` shim; lodash, moment, cheerio, ajv/tv4, `pm.vault`, `pm.require`, `jsonSchema` assertions are not shipped |
| Imported scripts | Run on import | Quarantined until a person trusts the exact hash |
| Unresolved variable | Sent literally | Blocks the send |
| Scope | None | Enforced on every send; policy `block` or `warn`; the CLI refuses to send without a declared scope |
| Network from scripts | Anywhere | Only `pm.sendRequest` through the scope-guarded sender |
| Global session headers | Not applicable | Skipped by collection items unless opted in |
| Variable persistence | Writes are kept | Policy `ask`, `keep` or `discard`; the CLI discards by default |
| Evidence | Console only | Every send is a History flow with exact wire bytes |
| Sync and sharing | Postman cloud | Local projects, bundles and the vault |
| `setNextRequest` | Unbounded | Honoured with a hard loop guard |

Not in this release: gRPC and MQTT requests, a mock server, monitors, Postman Collection v3,
`pm.visualizer` (a no-op stub) and Postman cloud workspaces.
