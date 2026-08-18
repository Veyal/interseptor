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
