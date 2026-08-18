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
- Check every database mutation and stop on failure. Increment response counts only after the row is
  committed, and use a scrubbed `500` for persistence failures.

Test with a closed store to make ignored errors deterministic, and use only generic traffic fixtures.
