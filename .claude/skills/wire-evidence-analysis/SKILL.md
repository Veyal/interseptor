---
name: wire-evidence-analysis
description: Preserve encoded HTTP evidence while giving scanners bounded decoded content.
---

# Wire Evidence and Analysis Bodies

When an HTTP client feeds both stored evidence and security analysis, keep those
representations separate:

1. Disable transport-level transparent decompression. Store the response bytes
   together with the original `Content-Encoding` headers.
2. Decode explicitly at presentation or analysis boundaries with the project's
   bounded codec helpers. Never rewrite the content-addressed evidence blob.
3. Treat send, capture, and body-reader failures as result errors. Do not turn a
   missing/truncated body into an empty grep miss, scanner response, or authz hash.
4. Exclude errored results from baselines, anomaly statistics, and acceptance
   decisions.
5. Test all three layers: encoded bytes remain stored, valid encoded content is
   decoded for detectors, and truncated/unavailable content is rejected.

In this repository, `sender.Sender` owns wire capture, while control-plane raw
views, active scan, Intruder, and authorization comparison own explicit decoding.
