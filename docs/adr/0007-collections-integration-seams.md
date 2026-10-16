# ADR 0007: Collections integration seams

Status: accepted (owner decisions in the integration brief). Date: 2026-10-07.

The four waves were built in parallel worktrees; this records the decisions taken when wiring them together.

- **One backend, one runner.** `collrun.StoreBackend` and `collrun.Runner/Manager` are the only way collections execute, for the UI, REST, MCP and the CLI. `internal/control` no longer has its own executor, overlay or run loop. The synchronous `POST /api/collections/run` keeps its compact response shape; the runner view uses the asynchronous `/api/runner/runs*` routes and SSE.
- **Script isolation.** Every approved script runs in the re-exec worker (`interseptor __scriptworker`). `INTERSEPTOR_SCRIPTS_INPROCESS=1` lets owner-trusted scripts run in-process. Quarantined scripts never reach an engine. Library and test default (zero `scriptworker.Router`) is in-process.
- **Auth.** The collauth suite is applied by `collexec.Pipeline` after scripts, variables, cookies and the codec. Token requests go through `collexec.StepDoer`, so they obey the scope policy, own-listener rule and dial guard and are captured as collection flows. OAuth2 tokens persist in `ix_tokens` (local only, scrubbed on export). The authorization-code callback lives on the control port; starting the flow is UI-session only.
- **Persistence per policy.** Variable writes and cookies share one policy (keep, discard, ask). Keep stores them (`ix_var_current`, `ix_cookies`), discard restores the pre-run state, ask follows the answer.
- **Secrets.** Archives, vault pushes, the project bundle (v2, `collections` section) and project merge all go through the one scrub (`BackupToScrubbed`, `ExportCollectionsBundle`, `MergeCollectionsFrom`). Variables written by a script or tool become secret-typed when their name designates a secret. Captured flows are evidence and keep their wire bytes.
- **One egress.** Only `collexec` Step and the auth `StepDoer` touch the sender. A repo-scanning test and a behavioural send-path enumeration pin this.
- **History.** Collection flows stay visible by default (`FlagCollection`, COLL tag, Collections chip, `collection=0|only`).
- **Out of v1** (unchanged): gRPC, MQTT, mock server, monitors, Postman v3, `pm.visualizer`, Postman cloud.
