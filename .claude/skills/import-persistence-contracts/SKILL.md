---
name: import-persistence-contracts
description: Keep Interseptor traffic and portable-project imports GC-safe and truthful about parse and persistence failures.
---

# Import persistence contracts

Use this when adding or changing HAR, Burp, or portable project imports.

- Stage request/response bodies with `NewFlowBodyWriter`, not an unprotected body writer, so body GC
  cannot reclaim a finalized blob before `InsertFlow` publishes its hash.
- Abort an already staged request writer if staging the response fails before `InsertFlow`; once
  `InsertFlow` is called, its publication defer releases both pending hashes on success or failure.
- Treat parser errors inside a container format (such as a project bundle's embedded HAR) as client
  errors. Do not silently turn malformed data into a zero-item success.
- Validate imported numeric values before narrowing or unit conversion. HAR elapsed milliseconds, for
  example, must be non-negative and small enough to multiply into `time.Duration` without wrapping.
- For a streaming format whose callback mutates the store, spool the bounded upload to a temporary
  file and run a validation-only parser pass first. Rewind and import only after the entire document
  is valid, so a malformed later item cannot leave earlier items committed behind a `400` response.
- Check every database mutation and stop on failure. Increment response counts only after the row is
  committed, and use a scrubbed `500` for persistence failures.
- When imported metadata is mandatory (for example merge provenance tags), insert the primary row,
  its FTS projection, and that metadata in one transaction. A later best-effort metadata call can
  report a successful import whose origin or operator notes have silently disappeared.

Test with a closed store to make ignored errors deterministic, and use only generic traffic fixtures.
