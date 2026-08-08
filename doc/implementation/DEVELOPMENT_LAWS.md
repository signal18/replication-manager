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
MySQL / Percona and versions. Never hand-roll SQL in `cluster/` or `server/` —
call or extend `dbhelper`. Manual paths delegate to the same auto path.

**T2. Don't duplicate code (DRY).** One implementation per concern — reuse the
existing helper, never reimplement what exists. If two pieces of code do the same
job, that's a bug: consolidate. Before writing a function, look for the one that
already does it.

**T3. Log — and track state — at the CORRECT level and in the CORRECT domain
state machine.** Two axes, always classify both:
- **Level:** *server* (one DB instance) vs *cluster* (the whole cluster).
- **Domain state machine:** HA (the main `StateMachine`), maintenance, schema
  (`SchemaStateMachine`), security (`SecurityStateMachine`), workload
  (`WorkloadStateMachine`).

Every state and every log line belongs to exactly one level + one domain.

**T4. Log everything, but present only what matters — never pollute a domain's
view, above all HA.** What you *log* (collect broadly, for debug) vs what you
*present* (only the signal that matters, per domain) are separate. The HA view is
sacred: never pollute it with other-domain noise that could bury the one signal
an operator needs.

**T5. Alert from the state machines — on critical states.** An alert fires when a
domain state machine transitions into a critical state — never from log scraping
or ad-hoc checks. Criticality is carried by the tracked state; the transition is
the trigger. Keep it flap-free (a %N-tick state must live in `pstatesN`).

**T6. NEVER implement an API call without a GUI interface.** Every API endpoint
ships with its GUI — no API-only features. Present a GUI whenever possible; don't
leave a capability CLI/API/config-only.

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

**T12. NEVER leave an issue without a target fix release (milestone) — on our
side.** Every issue *we* own is assigned to the release it will be fixed in.
Clients can file whatever they want, no milestone required from them; *we* triage,
tag, and milestone.

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

---

*Precedence: functional laws outrank technical laws; the perpetual-monitoring
invariant (F2 + F3 + F4) outranks everything. When two laws pull against each
other, the one that keeps us monitoring — perpetually, safely — wins.*
