---
name: sqlite-uri-paths
description: Opening a SQLite database by path with a `file:` URI. Use when adding any `sql.Open("sqlite", "file:"+path+...)`, a read-only open of another project's database, or when a project, data dir or archive whose name contains `?`, `#` or `%` behaves as if it were empty.
---

# SQLite `file:` URIs and paths

Every database here is opened through a `file:` URI so that pragmas can ride on the DSN. The path
part of that URI is not a path: `?` starts the query, `#` starts the fragment and `%` starts an
escape. A directory called `client#2` or `what?now` names a different file.

## What goes wrong

- A read-write open creates the wrong file next to the data directory and works on it. Data
  "disappears" and a stray file is left behind.
- A read-only open (`mode=ro`) of a missing file **succeeds lazily**. The first query fails with
  `no such table`, or, when the caller treats that as "older peer without this table", returns
  success with nothing in it. `MergeCollectionsFrom` reported a clean merge of zero collections.
- A directory named `q?_pragma=journal_mode(DELETE)` injects a pragma.

## Rule

Never concatenate a path into a DSN. Escape the three characters, in this order:

```go
dsnPathEscaper.Replace(path)      // internal/store: "%"→"%25", "?"→"%3f", "#"→"%23"
sqliteURIPathEscaper.Replace(path) // internal/control/fullproject.go, same mapping
```

`%` must be first, or the escapes themselves get escaped. Do not use `url.PathEscape`: it also
encodes `/` and spaces in ways the driver does not undo the same way on every platform.

## Test

Open in a temp dir for each of `a?b#c`, `100%done`, `q?_pragma=journal_mode(DELETE)` and
`plain dir`, write a row, close, check that the database file is inside the directory and that
nothing else was created beside it, reopen and read the row. For a read-only peer open, assert on
the data that came back, not on the absence of an error. See
`internal/store/nits_r2_test.go` and `internal/store/dsn_peers_r2_test.go`.

`grep -n '"file:"' internal --include=*.go` should show an escaper on every line.
