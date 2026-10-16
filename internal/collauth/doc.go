// Package collauth applies request authentication for collection execution:
// basic, bearer, API key, JWT bearer signing, digest, AWS SigV4, OAuth2
// (client credentials, password, refresh, authorization code + PKCE) and mTLS
// client certificates. It depends only on the standard library and
// internal/redact; collexec reaches it through the Applier interface, and all
// network access (token endpoints, digest probes) goes through the injected
// Doer so the caller can route it via the scope-guarded sender.
//
// Secrets (tokens, client secrets, keys) are registered with a redact.Registry
// and never appear in errors, Result summaries or token store JSON.
package collauth
