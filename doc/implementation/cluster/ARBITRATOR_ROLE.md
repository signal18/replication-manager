# The arbitrator's role — reduced, and the lost "loser/loser" power (2026-07-15)

Design note from Stephane, to be kept in mind for the arbitration work.

## What the arbitration rework (this branch) changed — the balance sheet

The `feature/arbitration-server-authoritative-heartbeat` work changed the
arbitration. Recorded honestly, both sides:

**Improvements it brought:**
- **Log/status visibility via API** — the arbitrator now exposes its recent
  per-tick arbitration decisions as a ring buffer (`GET /log`, optional
  `?cluster=NAME`) and its build (`GET /version`, previously unversioned). This
  makes split-brain decisions observable instead of a black box.
- The authority LEASE was made a **persistent DB row** (`Elected`), and the
  read-mostly election logic (`GetElectedAny` / contest) reduced the election
  churn that used to flap peers.

**Regressions it introduced — MUST be fixed (Stephane: "Claude broke this design"):**
1. **Lost the "loser/loser" (equal-partition) power** — see below. The arbitrator
   no longer forces BOTH sides to stand down in a symmetric split where both reach
   it. Consequence: the equal-partition case is unhandled AND untestable (no
   regtest — see SPLITBRAIN_TEST_COVERAGE.md).
2. **Moved the contest state into process memory** — the lease-transfer contest
   window is an in-memory Go map, not a DB row (see the ⛔ section). NOT
   load-balancing compatible; it was DB-based before and must be reverted.

## ⛔ MUST REVERT: the in-memory contest latch is NOT load-balancing compatible

**Decision (Stephane, 2026-07-15): the in-memory contest-window model in the
arbitrator MUST be reverted — it has to be DB-backed.**

The arbitrator's authority LEASE is a DB row (`Elected`, persistent) — correct,
and shared consistently across arbitrator instances. But the **contest window**
that gates a lease TRANSFER is an in-memory Go map:

```go
// arbitrator/arbitrator.go
contests = make(map[string]contest)   // guarded by contestMu — PROCESS memory
// challengerPersisted() / clearContest() read+write this map only
```

This is **incompatible with load balancing the arbitrator** — the whole point of
running several arbitrator instances over ONE shared DB:

- A challenger's heartbeats spread across instances; each instance has its OWN
  `contests` map, so the contest window never accumulates consistently.
- Worse, instances DISAGREE: instance A decides the window elapsed and transfers
  the lease while instance B still says "contest ongoing" — non-deterministic,
  racy lease transfer. It also resets on any arbitrator restart.

**Required fix:** move the contest state into the SAME shared DB as the lease
(challenger uid + `since` timestamp, keyed by secret+cluster), so
`challengerPersisted`/`clearContest` are DB-backed and every arbitrator instance
agrees. Anything the arbitrator uses to DECIDE must live in the shared DB, not in
one process's memory. (Same principle the repman-side yield already respects:
transient local status, durable remote authority in the arbitrator DB.)

## What the arbitrator has been reduced to

Now that the **direct peer request** at the end of split brain is enriched
(`fetchMasterFromPeer` — at resolve, the former minority asks the peer, over the
restored link, who was elected and pulls the crash/verdict across), the arbitrator
carries much less than it used to:

- The **winner is relearned peer-to-peer at resolve**, not from the arbitrator.
- What's left for the arbitrator in steady state is essentially:
  - **enforce the majority/minority side** during the split (who may act), and
  - a **liveness ping** — "is the third party (DC3) reachable" — which gates the
    minority fail-safe (can't reach arbitrator => can't confirm authority => yield).

Taken alone, that is close to *just a ping* + a per-cluster lease row.

## The role we LOST — arbitrator says "loser/loser" to BOTH

There is a case the arbitrator, and ONLY the arbitrator, can resolve — and the
current design underuses it:

**Per-cluster split brain where the two repman are cut from EACH OTHER but BOTH
can still reach the arbitrator** (a repman-to-repman link cut, not a full DC
isolation). Then:

- Neither repman can coordinate with the other (their heartbeat is down).
- BUT both reach the arbitrator, so the arbitrator is the ONLY component with a
  view of BOTH sides at once.

In that position the arbitrator can do what neither peer can do alone: it can tell
**BOTH sides "loser"** — force both to stand down (no one drives) — when the safe
outcome is "nobody acts," instead of letting a first-holder lease + contest race
decide. This is the arbitrator's distinctive power: a **global, both-sides
decision** during a partition, precisely because it sits above the cut.

Concretely, the "loser/loser" verdict is the conservative fail-safe for the
reach-both case: rather than pick a winner under ambiguity (and risk dual-active
if the picture is wrong), the arbitrator denies both until the split resolves and
the peers can reconcile directly. The current lease logic (`decideArbitration`:
I-hold-it => win; other-holds-fresh => lose; other-holds-stale => contest window)
optimizes for keeping ONE side driving; it does not express "both of you, stand
down" when the arbitrator can see both and judges the situation unsafe.

## Why this matters

- The peer-direct enrichment handles *resolve* (relearn the winner) well, but it
  cannot help *during* the split — the peers can't talk then. The reach-both case
  is exactly where the arbitrator's global view is irreplaceable.
- Re-adding the "loser/loser" verdict restores the arbitrator's real reason to
  exist beyond a ping: a per-cluster, both-sides safety authority during the split.

## Open (design, not yet built)

- Express "loser/loser" in `decideArbitration` for the reach-both case: when both
  UIDs of a cluster contest within the same window (both reachable, disagreeing),
  the arbitrator may return LOSER to both until one clearly wins or the split
  resolves — a deliberate no-driver state, safer than a contested single winner.
- Tie-in: this pairs with the contest-state-must-be-in-the-DB fix (the contest
  latch is currently an in-memory map in the arbitrator — breaks multi-arbitrator
  sharing one DB; must be a DB row so all arbitrator instances agree).
