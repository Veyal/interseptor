// Package collmatrix holds the Collections differentiators that Postman does
// not have, built on the shared collection runner:
//
//   - identity matrix: run a request or a whole collection as each authz
//     identity (and anonymous) and classify the differential, reusing the
//     authz outcome classes and the authz-matrix evidence render;
//   - Intruder handoff: turn a collection item into an Intruder spec with
//     {{var}} (or chosen) positions and dataset/enum payloads;
//   - OpenAPI coverage: which operations of an imported spec were exercised;
//   - saved-example diff: a recorded example against an actual response;
//   - timing breakdown for run reports;
//   - run results as finding evidence (typed flow attachments).
//
// The package is a library plus an http.Handler and MCP tool descriptors that
// the control layer mounts; it never imports control or mcp, so there is no
// cycle. Every send goes through collrun (and therefore the collexec
// pipeline's scope guard); nothing here talks to the network. Strings that
// leave the package pass through the injected Scrub function.
package collmatrix
