---
name: telemetry-suppression
description: Maintain Interseptor's conservative browser and Android capture-noise suppression without hiding authorized targets or changing forwarded traffic.
---

# Telemetry suppression maintenance

Use this checklist when changing browser or Android capture-noise suppression.

## Contract

- Suppression forwards matching requests and responses unchanged. It does not block the network.
- A suppressed flow must bypass request and response interception, request and response rules,
  History insertion/events, and request/response body storage.
- Existing History remains evidence and is never deleted automatically when a toggle changes.
- Keep normal application traffic, authentication, sync, downloads, add-on services, and FCM visible.
- Do not consume one-time History or notification dedup markers when suppression prevents the
  underlying record from being persisted; disabling suppression must restore future observability.

## Endpoint updates

1. Confirm the endpoint in current primary browser source or vendor documentation.
2. Classify it as telemetry, crash reporting, update/configuration, connectivity, or another dedicated
   browser-managed background service.
3. Prefer an exact hostname. Never add broad patterns such as `*.mozilla.org` or
   `*.googleapis.com`; they can hide an authorized target.
4. Add positive classifier and persistence tests plus nearby negative hosts that must remain visible.
5. Cover case, optional port, and trailing-dot normalization when introducing a new host family.

Mozilla references:

- <https://support.mozilla.org/kb/domains-allow-firefox>
- <https://searchfox.org/mozilla-central/source/modules/libpref/init/all.js>
- <https://searchfox.org/mozilla-central/source/browser/app/profile/firefox.js>

## Cross-path regression

Exercise a suppressed flow with request and response rules configured. Assert that it reaches the
upstream unchanged, creates no request or response hold, inserts no flow, and emits no stored body.
Also keep an ordinary target-flow test proving that normal rules and capture still work.
