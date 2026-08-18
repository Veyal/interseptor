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

Never use one limited history query for a portable export. Page newest-to-oldest with `BeforeID`
until the page is short; otherwise projects with more than the page size produce valid-looking but
silently truncated archives.

Full-project restore validation must enumerate all database-backed body evidence: current request
and response hashes, original pre-edit request and response hashes, and finding image blocks. Missing
original hashes break comparison evidence even when the current message still opens normally.

During export, reject symlinks and any other non-regular filesystem entry before `os.Open`. Opening a
symlink follows it, so a link planted in a project codec/body directory can copy an outside file into
an archive that an operator later shares.

Named full-project restores must reject `default` case-insensitively. That name is reserved for the
root project and deliberately omitted from named-project discovery, so accepting it installs a
successful archive into a hidden directory that the project picker cannot select.

The server-side path export/import handlers are filesystem mutation commands. Decode one complete,
bounded JSON value before taking a snapshot, creating a destination, or installing a staged archive;
a valid path object followed by extra JSON must leave both destination and project directory absent.
