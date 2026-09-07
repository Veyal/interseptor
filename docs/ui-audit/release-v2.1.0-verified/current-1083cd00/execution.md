# Current-source execution record

The accepted reports in this directory were generated with the retained
`current-visual-probe.py` and `current-journey-probe.py` against a current-source
`2.0.10-local` binary. Each probe enforces the runtime and binary SHA-256 values
recorded by the parent manifest, and each browser engine owns a disposable,
loopback-only fixture.

The visual probe was executed after its claim-note autosave reached `Saved`, then
reacquired the custom execution select, opened its option list, and selected the
visible `not_executed` option. This prevents a stale element from crossing the
post-save Findings remount while retaining the original interaction coverage.

The exact executed visual probe retains a fixed generic temporary-path helper
dependency. Its content hash is recorded in the report and the helper source is
preserved here as `probes/frozen-helper.py`. That dependency path is historical
execution detail, not a current rerun instruction. A future rerun under a
stricter filesystem boundary must adapt the helper lookup in a new probe copy,
execute that copy, and publish its distinct hash and reports.

Published reports contain only relative artifact links. The raw accepted and
unsuccessful attempts are retained in the isolated evidence root for this run.
