---
name: full-project-archive-integrity
description: Make Interseptor full-project exports fail visibly instead of silently omitting project files.
---

# Full-project archive integrity

Use this when changing full-project ZIP export or filesystem traversal.

An absent optional source directory is valid, but any other `WalkDir`, relative-path, file-open,
copy, or ZIP-close error must abort the export. Never suppress a traversal error and return a
successful archive: operators treat that artifact as a lossless backup and may only discover the
missing body or codec files after the source project is gone.

Keep the distinction explicit in tests: a missing optional directory succeeds, while a deterministic
invalid path or injected walk/read failure propagates an error.

For browser downloads, build the archive completely in a temporary file before setting ZIP response
headers. Streaming directly to `ResponseWriter` commits `200 OK` before a late source-file error is
known and turns a server failure into a corrupt download that looks successful.

Server-side path exports must also build into a same-directory temporary file. Close the complete ZIP
before publishing it, preserve any existing destination until the build succeeds, and use a
backup/restore replacement sequence so Windows does not require deleting the known-good file first.

Portable JSON projects are backups too. Before encoding their HAR, open and close each referenced
content-addressed request and response body; a resolver that maps an open failure to `nil` silently
turns corruption into an empty exported payload. Read errors for rules, scope, settings, and notes
must likewise abort before response headers are committed.
