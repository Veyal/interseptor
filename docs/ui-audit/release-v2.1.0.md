# v2.1.0 candidate UI verification

The candidate retains the behavior verified in the
[Findings improvement audit](findings-improvements.md). Its only subsequent
runtime change advances the development fallback to the previously published
2.0.10 release, as required by the release contract. Release artifacts receive
their own 2.1.0 version through linker flags.

All six visual/recovery checks passed in Chromium, Firefox, and WebKit. Thirty
screenshots cover desktop and 390px light/dark views, expanded revision diffs,
restoration after a newer edit, target cleanup, claim controls, CVSS, deleted
findings, and passive session inspection.

The initial Chromium screenshot pass encountered a detached element during
scrolling and a transient surface that did not settle. An unchanged-probe retry
passed all six checks. Both reports are retained; Firefox and WebKit passed in
the initial run. This is bounded candidate verification, not a claim that every
application feature was retested.

- [Initial report](release-v2.1.0/initial-run.json)
- [Accepted Chromium retry](release-v2.1.0/chromium-retry.json)
- [Exact executed probe](release-v2.1.0/probe.py)
- [Source, binary, and artifact hashes](release-v2.1.0/manifest.json)

Runtime SHA-256: `7f342c0709e2ba0620072b020245fd61c744ac39045ec2bdd8acac824e52edf0`
across 276 files. Candidate CLI: `2.0.10-local`, SHA-256
`1db1e11eb1881b4d6295749f459bf2737938d217ef459e4aaf970bf288a593d7`.
All traffic came from generic disposable loopback fixtures.
