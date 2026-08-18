---
name: archive-upload-limits
description: Keep Interseptor's streaming archive endpoints aligned with their archive-specific limits instead of the generic control JSON backstop.
---

# Archive upload limits

Use this when adding or changing control routes that upload ZIP or tar archives.

Archive handlers spool or process bodies as streams and have format-specific compressed and expanded
limits. Register a true streaming-upload route in `controlRequestBodyLimit`; otherwise the generic
128 MiB request backstop silently overrides the handler's larger advertised cap.

The handler must still enforce its own limit while copying or parsing, including one byte beyond the
cap where needed to distinguish an exact-limit body from overflow. Map both local limit detection
and `http.MaxBytesReader` errors to `413 Request Entity Too Large`.

Do not add ordinary JSON endpoints to the archive exception list. Cover the guard selection with a
tiny test backstop so the regression does not allocate a multi-gigabyte fixture, and keep archive
entry/file/expanded-size validation intact for decompression-bomb defense.
