# Interseptor — Success Metrics

*Owner: Product · Last updated: 2026-06-22*

## North Star Metric

**Weekly Active Hunters (WAH)** — distinct installs that, in a 7-day window, **capture ≥1 flow
*and* take ≥1 analysis/attack action** (open the inspector, intercept, send to Repeater/Intruder,
or run the Scanner).

Why this metric: it captures the **core value loop** — *intercept → inspect → act* — not vanity
reach. A captured flow alone is a tourist; a captured flow plus an action is someone getting value.
It's the single number that, if it grows healthily, means the product is working.

## Guardrail / trust metrics (never trade these for growth)

- **Capture never breaks forwarding** — 0 proxied requests failed *because of* capture/analysis.
- **Crash-free sessions** ≥ 99.5%.
- **TLS/secret correctness** — 0 incidents of mis-issued certs or traffic content leaving the host.
- **Idle RSS** (our core thesis) — track p50/p95; alert on regression. Target: an order of
  magnitude under Burp/ZAP idle.

## Funnel KPIs (AARRR)

**Acquisition** — awareness & download
- GitHub stars, forks, unique repo visitors; release **download** counts; inbound mentions
  (Reddit/HN/X, bug-bounty blogs, "Burp alternatives" listicles).

**Activation** — first value ("aha")
- **Time-to-first-flow** (install → first captured flow). Target: < 5 min.
- **% of new installs that capture HTTPS** (i.e., installed + trusted the CA) within session 1 — the
  real "aha", since HTTPS is where the value is. Target: > 50%.
- % of new installs that take ≥1 analysis/attack action in session 1.

**Retention** — does it stick
- **W1 / W4 retention** of activated installs; WAH / Monthly Active Hunters ratio (stickiness).

**Referral** — advocacy & contribution
- New external contributors / PRs; community size (if a Discord/forum exists); "recommended in"
  mentions and ⭐ velocity.

**Revenue** — n/a (free + open). Sustainability proxy instead: sponsors/maintainer-hours, and
whether the project stays releasable (cadence).

## Quality / product-health metrics

- **Scanner precision** — confirmed-issue rate vs total findings (our anti-ZAP-noise edge). Target:
  high precision, "quiet by default."
- **UI responsiveness** — p95 time from `flow.new` event to row rendered; large-history scroll FPS.
- **Cold-start time** to a usable UI.

## Measurement approach (privacy-first — this is a security tool)

The audience is privacy-sensitive, so:

1. **Telemetry is opt-in and off by default.** First-run prompt; one toggle in Settings; clearly
   documented.
2. **Never transmit captured traffic, hosts, URLs, headers, or bodies.** Only coarse, anonymous
   *product* events: feature used (Repeater/Intruder/Scanner/intercept), counts/buckets (e.g.
   "captured 10–100 flows"), version, OS, idle-RSS bucket. A random install id, rotated, no PII.
3. **Proxies that need no telemetry:** GitHub stars/traffic/release-download counts, package-manager
   installs, community mentions — track these regardless.
4. **Local-only "Stats" view** (future): show users *their own* numbers in-app even if they never
   share them, so the metric is useful to them too.

*Until opt-in telemetry ships, the North Star is estimated from release downloads × an activation
assumption, triangulated with community signal — explicitly a proxy, to be replaced by real WAH.*

## Targets (first horizon — directional, revisit quarterly)

| Metric | Baseline | Target |
|---|---|---|
| Time-to-first-flow | TBD | < 5 min |
| New-install → captures HTTPS (session 1) | TBD | > 50% |
| W4 retention of activated installs | TBD | > 25% |
| Idle RSS vs Burp idle | TBD | ≤ 1/10 |
| Scanner precision | TBD | high (few false positives) |
| Capture-broke-forwarding incidents | 0 | 0 |
