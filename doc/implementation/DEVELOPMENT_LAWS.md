# The Laws of replication-manager

*How we build repman, and what it must never do. Two layers: **functional** laws
(what repman must / must not do — these come first) and **technical** laws (how
we build it). When two pull against each other, the functional law wins, and the
**perpetual-monitoring invariant** (F2 + F3 + F4) wins over everything.*

---

## Functional laws — what repman is

**F1. We MONITOR — never change a client's database without explicit consent.**
Their config, their logs, their data — all of it. Default is hands-off: observe,
don't mutate. Touching DB-node-side state (rotating its logs, changing a setting,
truncating anything) requires the client's explicit go. We are a monitor, not an
owner of their data.

**F2. Never compromise our own monitoring capability.** Monitoring is THE job.
No feature — backup, provisioning, log collection, metrics — may ever degrade or
break the monitor's ability to keep monitoring. A feature that can stall the loop
or kill the process is worse than not having the feature. A monitor that stops
monitoring is worthless, and dangerous because it's silent.

**F3. Never fill our own disks.** repman-side storage is always bounded.

**F4. Never collect anything to infinity.** Every collected/emitted series — slow
/error/audit logs, PFS snapshots, event log, **metrics queues**, any buffer —
must be bounded by size + count/age. No unbounded growth, ever. **F3 + F4 = the
"perpetual repman" invariant: it must run forever without self-poisoning.**

**F5. Never assume our view of the world is correct — reconcile it against what
we actually observe.** Our cached state / internal model / assumptions must always
be validated against the OBSERVED reality of the monitored systems. Don't trust
our reconstruction over what the DB/cluster actually reports. This is the root of
most bad calls: theorizing instead of observing.

**F6. We orchestrate — every action must be rewritable by the client via a
script.** repman is a unifying orchestration layer, never a black box. For any
action it performs (failover, switchover, rejoin, backup, dbjob, snapback, alert,
log collection), the client must be able to substitute their own script. The
client keeps ultimate control; our orchestration is a convenience, not an
imposition. This is why repman is built on script hooks — preserve and extend
that, never hard-wire an action that can't be overridden.

**F7. We track STATE changes. Logs are debug; state changes are the law.** The
authoritative source of truth is tracked state (the state machines and their
transitions), not logs. Logs exist only for human debugging. Every decision,
orchestration action, alert, and UI signal is driven from tracked state changes —
never reconstructed from, or gated on, log contents. If something matters for a
decision or a signal, it must be a tracked STATE, not a log line.

**F8. Ship "less broken" over "completely broken" — progress beats perfection.**
When the current state is completely broken, shipping a strictly-less-broken
version now beats waiting for the perfect fix. Guardrail: "less broken" must be
genuinely less harmful overall — never trade one failure for a worse one (a fix
that introduces client data-loss is not "less broken"). Shipping the improvement
wins unless it violates a stronger law.

**F9. Never put a config secret in git without encrypting it with the local
repman key.** repman commits config to the config repos, harvested by the BO — so
any secret (passwords, tokens, credentials) MUST be encrypted with the local
repman key before it ever leaves the instance, never cleartext. A cleartext
secret reaching git is a breach.

**F10. Never leak a secret in logs.** repman never writes secrets in cleartext to
ANY log output — its own logs, collected DB logs, plugin output, error messages,
alerts. Redact/mask them. (F9 = secrets at rest in git; F10 = secrets in log
output.)

**F11. Every repman action on a database is logged AND reproducible by the user.**
Any action repman performs on a DB is logged (what, when, on which target) and the
user can reproduce it by hand — the exact command/script is exposed so the action
is transparent and replayable. No opaque, un-logged, or un-reproducible mutation.
Exception: secrets are redacted (F10), so the reproduction shows the exact action
but the user supplies the secret.

---

## Technical laws — how we build repman

**T1. Unify the SQL across flavors and versions.** Every SQL / replication /
topology / status operation goes through `dbhelper`, which abstracts MariaDB /
MySQL / Percona / PostgreSQL and versions (T23). Never hand-roll SQL in `cluster/` or `server/` —
call or extend `dbhelper`. Manual paths delegate to the same auto path.

**T2. Don't duplicate code (DRY).** One implementation per concern — reuse the
existing helper, never reimplement what exists. If two pieces of code do the same
job, that's a bug: consolidate. Before writing a function, look for the one that
already does it.

**T3. Log — and track state — at the CORRECT level, in the CORRECT domain state
machine and log, with the CORRECT severity.** Three axes, always classify all three:
- **Level:** *server* (one DB instance) vs *cluster* (the whole cluster).
- **Domain:** HA (the main `StateMachine`, the cluster log), maintenance, schema
  (`SchemaStateMachine`, `schema.log`), security (`SecurityStateMachine`,
  `security.log`), workload (`WorkloadStateMachine`, `workload.log`), config
  (`ConfigStateMachine`, the config pill: a fact about a server's configuration that
  needs an operator, e.g. system tables behind the release, deprecated variables). The
  state goes to its domain's state machine AND its line to that domain's log, never to
  another domain's (the HA view above all, T4). A plugin finding names its domain
  explicitly (`Finding.Domain`, or its severity for security/workload/schema), never
  falls into the HA machine by default.
- **Severity:** by consequence, not by habit. ERROR when the server cannot do part of
  its job until someone acts (every dump of it fails, a replica cannot be seeded),
  WARNING for a risk, INFO for a mode. A config fact can be an ERROR (ERR00116: the
  system tables lag the release, every logical backup fails).

Every state and every log line belongs to exactly one level + one domain + one
severity. A domain without its own log yet (config) is a gap to close, not a reason to
use another domain's.

**T4. Log everything, but present only what matters — never pollute a domain's
view, above all HA.** What you *log* (collect broadly, for debug) vs what you
*present* (only the signal that matters, per domain) are separate. The HA view is
sacred: never pollute it with other-domain noise that could bury the one signal
an operator needs.

**T5. Alert from the state machines — on critical states.** An alert fires when a
domain state machine transitions into a critical state — never from log scraping
or ad-hoc checks. Criticality is carried by the tracked state; the transition is
the trigger. Keep it flap-free (a %N-tick state must live in `pstatesN`).

**T6. NEVER implement an API call without a GUI interface — and never without
Swagger, and never without an ACL rule.** Every API endpoint ships with its GUI —
no API-only features. Present a GUI whenever possible; don't leave a capability
CLI/API/config-only. Every new handler MUST also:
- carry its **Swagger annotations** (`// @Summary`, `@Tags`, `@Param`,
  `@Success`/`@Failure`, `@Router …/[method]`) and the generated
  `docs/swagger.{json,yaml}` MUST be regenerated (`swag init`) — no API without
  Swagger; and
- have its **ACL rule** — a write/action endpoint that changes state MUST map to a
  grant in the ACL rule table (`cluster/cluster_acl_rules.go`
  `clusterACLRules` / `globalSettingsACLRules` / the databases/proxies/apps tables),
  reusing the closest existing grant (e.g. `GrantClusterSettings`). Without a rule
  `IsURLPassACL` denies it (403) — the endpoint is unreachable, not open. Add the
  rule WITH the handler, not after a 403.

An endpoint is not "done" until it has GUI + Swagger + ACL. See
`doc/implementation/server/API_SWAGGER.md` for the annotation shape, the
regeneration command, the ACL-rule step, and the verify-before-commit checklist.

**T7. Expose each unified capability behind a programmatic interface (a pluggable
abstraction).** One Go interface, multiple backends — never parallel hard-wired
paths. Routing → `DatabaseProxy` (MaxScale/ProxySQL/HAProxy/Spider/MyProxy);
storage → the DB layer (T1); provisioning → the orchestrator layer (OpenSVC,
localhost). Adding a backend = implementing the interface, not a new path.

**T8. NEVER ship a feature without BOTH user and technical documentation.** User
doc (`docs.signal18.io`: what it does, how to use it) and technical doc
(`doc/implementation/{package_path}/`: implementation decisions). No feature is
"done" doc-less.

**T9. NEVER write code without a GitHub issue.** Every change is tracked by an
issue first — it captures the *why* + scope + blast radius before the code, and
the PR/commits reference it.

**T10. NEVER land a new feature without a PR.** branch → PR → review → merge,
never direct-to-develop. The PR is where the review and go/no-go happen.
Orchestrator/failover/topology changes get their branch for the OpenSVC test
matrix.

**T11. NEVER open a GitHub issue without tags/labels.** Every issue is labelled
(`bug`, `critical`, `blocker`, domain tags…) so it can be triaged and found.

**T12. A milestone marks where a fix ACTUALLY LANDS — assign it only when the
fixing code exists, never speculatively at filing.** The milestone/version is the
release a fix *ships in*, so it goes on an issue **only once that fix is merged or
in a ready PR** — not the moment the issue is filed, and never stamped onto an
unreleased/not-yet-cut version as aspiration. **A filed-but-unfixed issue carries
NO milestone** — it stays in the backlog; the milestone appears when the code
does. Do not auto-stamp a target release on issues you open. Clients file freely
(no milestone); *we* triage — but triage is labels + priority, and the milestone
comes with the fix.

**T13. NEVER ship a feature without a REAL multi-product Docker test.** A "test
case" means the regtest framework spinning up real MariaDB/MySQL/Percona across
versions in Docker and exercising actual cluster behavior — not a Go unit test.

**T14. NEVER code a feature without a way to disable it.** Every feature ships
with its own off-switch (a config flag). *Override:* this does NOT apply to safety
invariants — the perpetual-monitoring invariant (F2+F3+F4) is stronger, so log
bounding/rotation and buffer bounding (T18) are always-on with no off-switch.

**T15. NEVER put client/partner names or internal infra details in the PUBLIC
repo.** The repo is public. Forbidden anywhere public (issues/PRs/commits/
comments/release notes/code/docs): client names (legal/commercial risk), partner
names/contacts, internal infra hostnames, BO internals. Use generic references
("a client", "a managed cluster", "the monitor host"). Real-name coordination
goes to private channels.

**T16. No config outside TOML.** Every config value is a Viper/TOML key
(`config.Config` field + `AddFlags`), never ad-hoc or hardcoded. TOML gives us:
change tracking, per-cluster AND global hot-reload (`SaveDynamic`), and Vault
delegation for secrets. `scope:"server"` = immutable (`/etc`, repman doesn't
write it); other scopes = reloadable.

**T17. NEVER put frequently-changing data in the git push.** Only stable config/
state goes into the config repos. High-churn data (per-tick metrics, fast-changing
state) overloads the git sync and pins the active repman. Ask "how often does this
change?" before committing.

**T18. NEVER build an unbounded in-memory buffer/queue.** Any producer→sink buffer
(metrics, collected logs, events, alerts, jobs — any `append`-on-tick slice or
channel) MUST have:
- a **hard max size** (config-driven per T16, safe non-zero default; `0`/unset
  falls back to the default, never "unbounded"),
- an **explicit drop policy** (drop-oldest/newest) that **frees the backing
  memory** on drop (reslice into a fresh array — a forward reslice keeps the old
  backing array and its strings alive),
- the drop **tracked as a STATE** (a counter + a WARN state when sustained), so
  the operator learns "the sink is failing" from a state (T5), never from a crash.

The trap: a buffer that drains *only on a successful send* is a latent OOM. The
sink WILL fail — down, slow, connection-refused, write-deadline timeout,
backpressure — and requeue-on-failure with no cap grows the buffer until the
monitor dies. **"Embedded/local sink" does NOT make it infallible:** an embedded
carbon still crosses a real TCP socket with a 1s write deadline; if it ingests
slower than we produce, every flush times out and requeues. Bounding is the
code-level enforcement of F3 + F4, so (like T14's log-rotation override) the
*bounding* is always-on — only the size/policy is tunable, never "off".

> **Triage rule — anything that halts the monitor is ALWAYS a `blocker`.**
> Both at the top of that list:
> - a **memory leak** (unbounded growth → OOM), and
> - a **deadlock / hang / loop-freeze** (mutex deadlock, a stall holding a lock,
>   a blocking call with no deadline, a wedged goroutine).
>
> Both kill monitoring — the OOM loudly, the deadlock silently (worse: it looks
> alive). By F2 such a bug is top-severity, must-fix-before-release; the release
> does not ship with one known-open. These two often pair: a flush holding a lock
> across a blocking send is a deadlock-class stall, and removing the stall without
> a cap exposes the leak — the same code, both faces.

**T19. Follow the codebase's existing naming conventions — never invent a synonym
for a concept that already has an established name.** Every new identifier
(variable, config key, struct field, function, state) is named the way the *same
kind of thing* is already named in the tree. Before naming, grep how the siblings
are named and match — don't coin a new word for an existing concept.
- **Canonical example — a recurring cadence is an `Interval`:** Go field
  `...Interval`, tags `...-interval` (mapstructure/toml) and `...Interval` (json).
  Do NOT introduce `Period` / `Every` / `Frequency` / `Freq` for the same thing.
  `Delay` is a *different* concept — a one-shot wait *before* an action (e.g.
  `provision-delay`, `sst-wait-retry-delay`) — keep it for that only, never as a
  synonym for a repeating interval.
- **The three struct-tag forms are the exact mechanical transform of the Go field
  name** (PascalCase → kebab-case for mapstructure/toml → camelCase for json).
  Keep them mechanically consistent; never hand-vary one form.

This is the naming face of T2: one concept, one name.

**T20. Before starting any work, sync from `develop` and check for in-flight work
— never duplicate.** Before branching or writing a line: (1) `git fetch` and
branch off / rebase onto the **latest `origin/develop`**, and (2) check existing
branches, open PRs, and the issue's own thread for anything already implementing
the fix. Building a parallel fix while a teammate's PR for the same issue is in
review wastes effort and creates conflicts. Sync first, check first, then work.
This is T2 (don't reimplement what already exists) at the repository level, and
it pairs with T9→T10 — the issue is where you discover the branch/PR that already
exists before you open a duplicate.

**T21. NEVER hand-edit the OpenSVC modulesets — they are generated exports and
the collector is the reference.** `share/opensvc/moduleset_*.json` are exports
of the OpenSVC collector's compliance rulesets: to change a config template,
update the ruleset variable ON the collector (UI or
`SetRulesetVariableValue`), then export and copy the JSON into `share/`. A code
change that needs a template change ships only the Go getter and the
`%%ENV:...%%` placeholder wiring (inert until the ruleset references it), plus a
PR section giving the exact ruleset text to apply on the collector — see PR
#1753 for the pattern. Instances pick updated modulesets up through the
enterprise-compliance plugin (`prov-auto-update-compliance`, preserved variables
never overwritten). A hand-edited share JSON diverges from the reference and is
overwritten at the next export. Additionally, a branch that carries a moduleset
export MUST first verify that no other active branch or open PR also modifies
that moduleset file (`git diff origin/develop...origin/<branch> -- share/opensvc/`
over the open branches): the file is replaced whole at each export, so two
concurrent branches touching it guarantee a conflict or a silently lost ruleset
change — coordinate the export order with the other branch's author (T20 at the
moduleset level). Ideally the moduleset export is committed on `develop` itself
— one lineage for the one reference, mirroring the collector — and a feature
branch that depends on it cherry-picks that export commit; carrying an export
inside a feature branch is the exception, and is what triggers the
concurrent-branch check above.

**T22. Infrastructure & environment questions are answered by the infrastructure
reference — never guessed.** Anything about the DEV or PRODUCTION infrastructure —
which host/OpenSVC service an instance is, dev vs prod, how to reach or read it
(bastions, SSH jump paths, `om`, `docker exec`), where a config value or secret
lives, which values are environment-specific vs portable, how one environment maps
to another — is answered by the single infra-access reference `INFRASTRUCTURE.md`
(maintained in the private back-office repo), the source of truth. **Consult it
FIRST**: do not answer from assumption, and do not declare a dead-end ("the VPN is
down, I can't reach it") before checking the reference — the access path, the exact
instance, and how to read its config are written there, so "I can't get it" is
almost always "I didn't open the reference." It is private (hosts, paths, access
methods): **point to it, never copy its specifics into this public repo** (T15) —
and keep the reference itself updated as you learn. This is the infrastructure face
of F5 (reconcile against reality, don't assume) and of T20 (check what already
exists before you work).

**T23. Every `dbhelper` SQL function answers for EVERY engine repman monitors —
PostgreSQL included.** When you write or change a `dbhelper` function, decide its
PostgreSQL side in the same change: the PostgreSQL equivalent behind the usual engine
branch (`myver.IsPostgreSQL()`), or, when the concept has none, a documented early
return (zero value or an explicit error) — never MySQL/MariaDB SQL sent to a
PostgreSQL server, never "PG later". Say which in the function's doc comment, and
cover both branches in its test. Before adding the function at all, look for the one
that already exists (T2): a new helper that duplicates an existing one is a bug,
whatever engines it covers. Example: the fresh-node check reads the install time from
`mysql.global_priv` CREATE_TIME on MariaDB, `mysql.user` on MySQL, and the initdb time
encoded in `pg_control_system().system_identifier` on PostgreSQL — one function, one
rule, every engine (the PostgreSQL read must work for the monitoring user: check its privilege first).

**T24. No AI attribution in commits or pull requests.** A commit message or a PR body
ends with its own content: never a `Co-Authored-By:` trailer, "Generated with" line or
any other attribution to an AI assistant or tool, whoever or whatever wrote the change.
The commit's author is the person who commits it and answers for it. This applies from
now on: commits already pushed are not rewritten to remove such a trailer (a history
rewrite and a force-push are not worth it).

**T25. A file the config git repo tracks is written only by the save callback —
never from anywhere else.** The config repo (`git-url`) stages each cluster's
`<cluster>/*.toml`, `agents.json` and `queryrules.json`, and the `event-changed.*.log`
event logs (`config.go` `PushConfigToGit`). Those files are written by
`cluster.SaveCallBack()` (and `repman.SaveCallBack()` for the global config), which the
single config-sync gate runs; every other piece of code changes the in-memory state and
calls `cluster.Save()`, the event, which does no I/O. Never an `os.WriteFile`, a
`toml.Marshal` to disk or a sed on one of these files from an API handler, a job, a
plugin or the monitor tick. A file written outside the gate races it: go-git can stage
it half-written and push it to every peer, the next save overwrites it from the
in-memory state (the change is lost), and it skips the key diff that feeds the event log,
so peers never replay it. One writer, one place to look when a pushed config is wrong.
A new file that must reach the config repo is added to `SaveCallBack` and to the staged
list together; anything else repman writes (backups, cookies, logs, caches) stays out
of the staged list. See `doc/implementation/config/CONFIG_SYNC.md`.

**T26. A security fix ends with the SBOM of the releases it concerns updated.** A
vulnerability in repman is a GitHub security advisory, kept a **draft** (private) until
the fixed release is out: no public issue, commit message or PR body describes the
attack before. Its vulnerable version range, patched version, CVSS and CWE are filled
exactly, because the release SBOM computes each release's status from them. Once the
fixed release is published, publish the advisory, then refresh the SBOM of the latest
release and of every release whose status changed: run `generate-sbom.yml` by hand with
the tag, and check the `vulnerabilities` entry of the downloaded `sbom-cyclonedx.json`
(affected release `exploitable`, fixed release `resolved`). A dependency vulnerability
that the SBOM marks `exploitable` (govulncheck finds our code calling it) is a security
fix too: bump the module or the Go toolchain, release, refresh. A draft advisory never
reaches the SBOM: the workflow lists published advisories only. Procedure for
contributors: docs.signal18.io, Contribute, Security Fixes and SBOM (#2086).

**T27. A replication link belongs to the client's topology: never delete one, and never
change one without a state that says so.** A channel (its master host, user, name,
position) is how the client wired their databases, often across clusters: a parent and
its child or staging cluster, an external source, a client's own multi-source. repman
monitors and repoints the links it manages; it does not own them (F1). So:
- No `RESET SLAVE ... ALL` / `RESET REPLICA ALL`, `DROP SUBSCRIPTION`, or any statement
  that removes a channel's definition, in any automatic path (reseed, rejoin, failover,
  provision, job). To clear positions, `RESET SLAVE '<channel>'` without `ALL` keeps the
  link. Removing a link is an explicit, user-requested action only.
- Moving a link to another source is the job of the failover, switchover and rejoin
  code, under their guards (election, positions, consistency checks), and nothing else's.
- Restoring a backup (a reseed, logical or physical) changes no link: no `CHANGE MASTER`,
  no other source, no definition. It may stop and start the managed channel around the
  load and restore the GTID state the backup carries (`gtid_slave_pos`); the link stays
  as the last failover, switchover or rejoin left it. A link that must be set or moved
  after a restore is the rejoin's job. Provision and jobs do not change a link either.
- STOP is not DROP. Stopping every channel (`STOP ALL SLAVES`) is right when the
  operation replaces the data all of them apply to (a restore): each channel stopped is
  started again at the end, or left stopped with a state saying why. Positioning or
  resetting acts on the channel repman manages (`replication-source-name`) only, by name:
  a statement without a channel name that removes or rewrites (`RESET SLAVE ALL`) is never
  justified by "there is only one channel".
- A path that may fail half-way leaves the link as it found it, stopped at worst: never
  "delete now, recreate at the end". The end may never come.
- Any change to a link (stop, repoint, reset of positions) opens a state naming the
  server, the channel, the reason and the action that did it, so the GUI shows why the
  link is down. A log line is not enough (T3/T4: states, not logs).
- An AI assistant changing a reseed, rejoin, failover or switchover path reads this law first and
  says, in the PR, which channels its change stops, repoints or resets, and why.
Example (PR #2020, v3.1.44): to fix older dumps and an idle MariaDB replica, the
mysqldump reseed added `STOP ALL SLAVES` then `RESET SLAVE '<name>' ALL` (a plain
`RESET SLAVE ALL` when no name is set), at request time and before the load, recreating
the link only after a successful load. A reseed launched from a parent on its child's
server deleted the child's link; the parent then dropped the server from its topology
and it vanished from the GUI; the reseed failed (a system account) and the link stayed
deleted; no state explained any of it.

## Debugging discipline — investigate FULLY before you unblock

**Never unblock a stuck issue before you have investigated it fully.** A restart, a
killed goroutine, a cleared flag or a truncated file *makes the problem disappear and
takes the evidence with it* — you lose the one chance to find the root cause and the bug
ships to the next demo. This is the live-investigation half of "never delete the trigger":
keep the investigation **read-only** until you understand *why*, then fix the cause, not
the symptom.

**The goroutine dump is the first tool for anything stuck / hung / deadlocked** (a
rejoin/reseed that never completes, a server pinned in maintenance, an op that "runs"
forever). repman registers `net/http/pprof` on the HTTP monitor (`server/http.go`,
`http-port` default 10001, `http-bind-address` = localhost), so from **inside the repman
container** (a read-only GET):

    curl -s "http://localhost:10001/debug/pprof/goroutine?debug=2"

`debug=2` prints every goroutine with its **wait state and blocked duration** —
`[sync.Mutex.Lock, 1615 minutes]`, `[chan receive, 13079 minutes]`. Read it as:
- **long durations are the smoking gun** — a goroutine blocked *minutes/hours/days* is
  stuck, not busy. Sort on the `N minutes]` in each state header.
- **a mutex deadlock** shows as ≥2 goroutines in `sync.Mutex.Lock` on the *same* mutex
  address; find the **holder** (the goroutine that took that lock and is now blocked
  *elsewhere* — a channel/network/another lock — while still holding it). The classic
  cause is **a lock held across a blocking wait**.
- `?debug=1` groups the counts — hundreds piling up in one stack = a **goroutine leak**.

Worked example (2026-09-11, belair/db2): an operator logical rejoin sat "running" for a
day. pprof showed `RejoinMaster → rejoinWithMethod → JobFlashbackLogicalBackup →
snapshotLogicalBackupMeta` blocked 27h on `server.backupMetaMutex`, held by a cron
physical backup parked in `waitForBackupSlot` while `MarkBackupPhysicalDone` (which frees
the slot) had waited 8.6 days on the same mutex — a circular wait. Restarting repman "to
unblock it" would have erased all of it, and the deadlock would have re-bitten the next
demo.

---

*Precedence: functional laws outrank technical laws; the perpetual-monitoring
invariant (F2 + F3 + F4) outranks everything. When two laws pull against each
other, the one that keeps us monitoring — perpetually, safely — wins.*
