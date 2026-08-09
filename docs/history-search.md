# History search

Interseptor History supports ordinary filters and deterministic Starlark predicates. Saved searches run inside the project that contains them. They don't call an AI model, send traffic, or inspect anything outside captured flow data.

## Anywhere search

`GET /api/flows` accepts `search` and `searchScope`.

```text
GET /api/flows?search=example.com&searchScope=anywhere
GET /api/flows?search=example.com&searchScope=body
GET /api/flows?search=example.com&searchScope=id
```

`anywhere` is default. It checks stored flow metadata, request and response headers, tags, and request and response bodies. `body` limits matching to bodies. `id` is the ID search mode. Other filters, such as `host`, `method`, `status`, `tag`, `hasNote=1`, `inScope=1`, and `includeTools=1`, still apply.

Body and Anywhere searches inspect at most the latest 8,000 filtered flows. Each stored body read is capped at 256 KiB. A capped result includes `searchNote`, for example:

```json
{"searchNote":"Anywhere search scanned the latest 8000 filtered flows. Narrow with host/method filters if results look incomplete."}
```

The normal response contains `flows` and `truncated`. `limit` defaults to 200 and accepts 1 through 5000. The server fetches one extra row to set `truncated`.

## Saved Starlark searches

Use a saved search when string matching isn't enough. Each script defines `match(flow)` and returns a Starlark boolean. The `flow` value exposes:

| Attribute | Value |
|---|---|
| `method`, `scheme`, `host`, `path`, `mime` | strings |
| `port`, `status` | integers |
| `req_body`, `res_body` | strings |
| `req_headers`, `res_headers` | dictionaries, first value per header |

Available helpers:

| Helper | Result |
|---|---|
| `req_header(name)`, `res_header(name)` | first matching header value, or `""` |
| `req_header_all(name)`, `res_header_all(name)` | all matching values as a list |
| `query_param(name)` | first URL query value, or `""` |

Header lookup is case-insensitive. `req_headers` and `res_headers` use canonical header names. Each body attribute contains at most the first 64 KiB of its stored body. A saved-search request evaluates at most 64 filtered flows and reads at most 8 MiB of bodies total. Missing, unreadable, or budget-exhausted bodies appear as empty strings.

### Author, test, save, select

Test source before saving. Include `flowId` to execute it against that captured flow and receive its `matched` result:

```bash
curl -sS -X POST http://127.0.0.1:9966/api/flow-searches/test \
  -H 'Content-Type: application/json' \
  -d '{"name":"example-get","scope":"anywhere","flowId":42,"script":"def match(flow):\n    return flow.host == \"example.com\" and flow.method == \"GET\""}'
```

A valid response has `valid: true`; a selected flow also returns `flowId` and `matched`. Save it with `POST`, or replace it with `PUT`:

```bash
curl -sS -X POST http://127.0.0.1:9966/api/flow-searches \
  -H 'Content-Type: application/json' \
  -d '{"name":"example-get","scope":"anywhere","script":"def match(flow):\n    return flow.host == \"example.com\" and flow.method == \"GET\""}'

curl -sS http://127.0.0.1:9966/api/flow-searches
curl -sS http://127.0.0.1:9966/api/flow-searches/example-get/source
curl -sS 'http://127.0.0.1:9966/api/flows?savedSearch=example-get&limit=200'
```

`GET /api/flow-searches` lists names and scopes. The source endpoint returns the saved `name`, `scope`, and `script`. `PUT /api/flow-searches/{name}` updates the source. `DELETE /api/flow-searches/{name}` removes it. Names cannot contain `/` or `\\`.

Saved searches persist in project settings under `flow.searches`. They are not global. Project switching changes which saved searches are visible.

`scope` accepts `anywhere`, `body`, or `id`; unknown or empty values normalize to `anywhere`. Scope is metadata for the saved search and does not change what the Starlark predicate can inspect. A project stores at most 100 saved searches; names are limited to 128 bytes. Selecting `savedSearch` runs the predicate against at most the latest 64 flows after other filters. If that limit is reached, the response includes `searchNote`:

```json
{"searchNote":"Saved search scanned the latest 64 filtered flows."}
```

## Valid examples

Match response status and MIME type:

```python
def match(flow):
    return flow.status == 200 and flow.mime == "application/json"
```

Match a request header and query parameter:

```python
def match(flow):
    return flow.req_header("Authorization") != "" and flow.query_param("page") == "2"
```

Match any response cookie value:

```python
def match(flow):
    return "session" in flow.res_header("Set-Cookie") or "session" in flow.res_header_all("Set-Cookie")
```

The last example is valid Starlark because `in` works on strings and lists. Use `flow.res_header_all("Set-Cookie")` when repeated header values matter.

## Safety and troubleshooting

Scripts are compiled and validated before test or save. Compilation requires callable `match(flow)`. Validation invokes it once with an empty flow, so guard assumptions about body, headers, and status. Runtime errors return safe API errors and don't expose flow content in the error text.

Source is capped at 64 KiB. Compilation and each predicate run are capped at 100,000 Starlark steps. Active predicate evaluation stops when its HTTP request context is cancelled. `load()` has no loader. Scripts have no file, network, environment, or wall-clock access. They cannot mutate the supplied flow or its headers.

Common errors:

- `missing a match(flow) function`: add `def match(flow):` at top level.
- ``match(flow) must return bool``: return a comparison or `True`/`False`, not a string, list, or dictionary.
- `source exceeds 64 KiB`: shorten the script.
- `execution failed` or a step-limit error: remove unbounded work and narrow filters before selecting the saved search.
- Empty results with a limit note: add `host`, `method`, or another flow filter, then retry. The candidate limit is intentional.

Search scripts are predicates only. External agents can use the same REST routes or the control API, but no built-in AI is involved.
