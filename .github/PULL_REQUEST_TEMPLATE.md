<!--
Thanks for contributing to replication-manager.
Fill in the sections below. The issue link is REQUIRED — see the checklist.
-->

## Linked issue (required)

<!--
Every PR must reference the issue it addresses (Development Law T9/T10). Use a
CLOSING keyword so the issue auto-closes AND gets this PR's milestone on merge —
this is how the release notes build themselves. Fixes left unlinked sit open and
un-milestoned and have to be reverse-mapped by hand later.
-->

Fixes #<!-- issue number -->

<!-- If it only partially addresses an issue, use `Refs #NNNN` instead and say what's left. -->

## Summary

<!-- What this changes and why. One or two sentences is fine. -->

## How it was tested

<!--
Per T13, a real multi-product regtest (Docker: MariaDB/MySQL/Percona) is the gate
for a feature — not a Go unit test. State what you ran.
-->

## Checklist

- [ ] **Linked an issue** above with `Fixes #NNNN` (or `Refs #NNNN` for partial) — T9/T10
- [ ] Targets `develop` (or a topology branch for failover/rejoin/split-brain changes) — T10/T20
- [ ] Synced from latest `origin/develop` and checked no in-flight branch/PR already does this — T20
- [ ] New config is a TOML key with an off-switch where applicable — T14/T16
- [ ] New API endpoint ships with its GUI surface (no API-only features) — T6
- [ ] User doc (docs.signal18.io) + technical doc (`doc/implementation/`) updated — T8
- [ ] Real regtest / Docker coverage for the behavior — T13
- [ ] No unbounded buffer/queue; producer→sink buffers are bounded + drops tracked as state — T18

<!-- The milestone is assigned by the maintainer when the fix actually lands (merged/ready), not speculatively (T12). Closing keyword above carries this PR's milestone to the issue on merge. -->

> Reference: `doc/implementation/DEVELOPMENT_LAWS.md` (F1–F11 functional, T1–T20 technical).
