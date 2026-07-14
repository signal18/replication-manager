# testSetMinorityWithMaster — full state-transition analysis (2026-07-14)

Run of `testSetMinorityWithMaster` on cluster **belair**, first run on the VSPLIT
state namespace build (commit `69e910b6a`, deployed on both nodes ~18:45 UTC).
Fired 19:01:50 UTC from node1's CLI; test result **PASS** at 19:03:08.

- **node1** = repman1, s18-fr-6, `dbaas-fr-2.signal18.io` — Active at test start, becomes the **minority** (master db1 colocated on its side).
- **DR** = repman2, s18-fr-4, `dbaas-fr-2-dr.signal18.io` — Standby at test start, becomes the **majority** and fails over.

The four cuts armed by the test (see `regtest/test_split_brain.go`):

| op | where | cut |
|----|-------|-----|
| 1/4 | LOCAL node1 | arbitrator link, ALL clusters (`SimulateArbitratorFailureAll`) |
| 2/4 | LOCAL node1 | inbound `/api/heartbeat` darkened |
| 3/4 | REMOTE DR | inbound `/api/heartbeat` darkened |
| 4/4 | REMOTE DR | master link cut, pinned to db1 (`SimulateMasterFailure`) |

## Verdict

The **split-brain window behaved exactly as designed**: the minority yielded
through the real fail-safe path, the majority won arbitration and failed over,
and every VSPLIT state opened and closed on the correct side, giving a readable
per-side timeline of the physical cuts interleaved with the cluster's reaction.

The **post-resolve recovery has a real bug**: at cut-clear the majority's
"last non slave" master inference promoted the *returning old master* db1 back
to Master and ran `SET GLOBAL read_only=0` on it while db2 was already the
promoted writable master → **dual writable master**. Details below.

## Full ordered state timeline (belair, both nodes)

Per-server `SEC*/WTAG*/GHENT*/WARN0306` blocks are topology attach/detach:
they RESOLV when a server leaves the confirmed topology (Suspect/Failed) and
re-OPEN when it returns.

```
19:01:52 NODE1 OPENED VSPLIT0002                  ← op2: my inbound heartbeat darkened
19:01:59 DR    OPENED VSPLIT0001                  ← op4 landed: master cut live on DR
19:01:59 DR    OPENED VSPLIT0002                  ← op3: DR inbound heartbeat darkened
19:01:59 DR    OPENED VSPLIT0003                  ← db1 pinned-blocked, "this partition fails over"
19:01:59 DR    OPENED VSPLIT0005                  ← ERR00028 false-positive guard suspended
19:01:59 DR    OPENED WARN0079                    ← split-brain detected (majority side)
19:01:59 DR    OPENED WARN0083                    ← arbitration WINNER
19:02:05 DR    OPENED WARN0171                    ← db1 connection failed (the simulated cut)
19:02:05 DR    RESOLV ERR00105                    ← standby restrictions lifted → DR is Active
19:02:05 NODE1 OPENED ERR00022                    ← passive mode (minority yielded)
19:02:05 NODE1 OPENED VSPLIT0001                  ← arbitrator cut live (fleet-wide op1)
19:02:05 NODE1 OPENED VSPLIT0004                  ← minority side, must yield
19:02:05 NODE1 OPENED WARN0079                    ← split-brain detected (minority side)
19:02:05 NODE1 OPENED WARN0081 WARN0082           ← arbitrator report+arbitration errors (link cut)
19:02:05 NODE1 RESOLV GHENT716/1109/1204/1272/1464@db2
19:02:05 NODE1 RESOLV SEC0101/0103/0105/0108/0112/0114@db2
19:02:05 NODE1 RESOLV WARN0306@db2
19:02:05 NODE1 RESOLV WTAG0003/0008/0020/0021/0150/0200/0201/0210/0220/0240@db2
                                                  ← db2 held Suspect by fail-safe: states detach
19:02:15 NODE1 OPENED ERR00012                    ← no master in topology (pointer nil'd — correct)
19:02:15 NODE1 OPENED ERR00105                    ← standby restrictions on
19:02:15 NODE1 OPENED GHENT*/SEC*/WTAG*/WARN0306@db2  ← db2 states re-attach (still monitored)
19:02:15 NODE1 OPENED WARN0090                    ← arbitrator unreachable
19:02:30 DR    OPENED ERR00097                    ← failover gate check (all conditions true,
                                                    incl. NotOneSlaveHeartbeatIncreasing = the
                                                    VSPLIT0005 guard-disable working)
19:02:30 DR    OPENED WARN0023                    ← failover in progress
19:02:30 DR    RESOLV GHENT*/SEC*/WTAG*/WARN0306@db1  ← db1 unreachable on DR, states detach
19:02:35 DR    OPENED ERR00010 ERR00032 ERR00085  ← no slave left (db2 promoted, db1 dark)
19:02:35 DR    OPENED WARN0060                    ← no semisync on new master
19:02:35 DR    OPENED WARN0080                    ← cluster lost majority (1 of 2 db visible)
19:02:35 DR    RESOLV ERR00097 WARN0023           ← FAILOVER COMPLETE (db2 = RW master, proxies flipped)
19:02:35 NODE1 OPENED ERR00010                    ← no slave (db2 Suspect)
19:02:45 NODE1 OPENED ERR00021                    ← cluster state down (minority sees nothing usable)
─── test happy-path restore fires at 19:03:08 ───
19:03:08 DR    RESOLV VSPLIT0001 VSPLIT0003 VSPLIT0005
                                                  ← majority cuts cleared: db1 visible again AND
                                                    false-positive guard re-armed, same instant
19:03:13 NODE1 RESOLV ERR00022 ERR00105           ← node1 back Active
19:03:13 NODE1 RESOLV VSPLIT0001 VSPLIT0004       ← minority cuts cleared
19:03:13 NODE1 RESOLV WARN0079 WARN0081 WARN0082  ← split over, arbitrator reachable
19:03:14 DR    OPENED ERR00105                    ← DR back to Standby
19:03:14 DR    OPENED GHENT*/SEC*/WTAG*@db1       ← db1 re-attaches on DR
19:03:14 DR    RESOLV VSPLIT0002 WARN0079 WARN0083 WARN0171
19:03:19 NODE1 OPENED ERR00032 ERR00085 WARN0060  ← topology degraded post-split
19:03:19 NODE1 OPENED ERR00063                    ← ★ EXTRA MASTER detected — never resolves
19:03:19 NODE1 RESOLV ERR00012 VSPLIT0002 WARN0090
19:03:26 NODE1 RESOLV ERR00021                    ← cluster no longer "down"
19:03:31 DR    OPENED WARN0111 · RESOLV WARN0084@db1
19:05:24 NODE1 OPENED WARN0111 · RESOLV WARN0084
```

Resolution-ladder checkpoints all verified: minority timer/restore released
the arbitrator states (VSPLIT0004), the majority cut-clear released the frozen
host + ERR00028 guard + VSPLIT0003/0005 atomically, and the heartbeat
(VSPLIT0002) resolved last on each side.

## Bug 1 — dual RW master at resolve (CRITICAL)

DR raw log, 19:03:10–13 (right after cuts cleared):

```
19:03:10 State changed, init failed server db1 as unconnected
19:03:10 Setting Read Only on unconnected server db1 as a standby monitor   ← correct
19:03:10 db1 state transition Failed → StandAlone
19:03:10 Auto Rejoin is disabled          ← MISLEADING: autorejoin is ON (default true,
                                            no belair override). The else-branch printed
                                            this for ANY failed gate — the actual refusal
                                            was !IsSplitBrain (resolve propagated 19:03:14,
                                            a one-tick race), and the Failed->up trigger
                                            is one-shot so the rejoin was lost for good.
19:03:13 Server db1 was set master as last non slave        (cluster_topo.go:318)
19:03:13 db1 state transition StandAlone → Master
19:03:13 SET GLOBAL read_only=0   module=Rejoin server=db1  (cluster_topo.go:326)
19:03:13 Server db1 disable read only as last non slave
19:03:13 Server db2 was set master as last non slave        ← inference FLAPS to db2
```

The "last non slave" inference fired with **two** non-slaves present — db2
(promoted master, not a slave) and db1 (returning old master, StandAlone) —
picked db1, made it Master and writable, undoing both the minority fail-safe's
read-only and DR's own standby-monitor read-only set 3 seconds earlier. Since
then DR infers db1 per tick, node1 knows db2 ("Peer designates real master db2
after split-brain"), and **both DBs are writable** (verified live:
db1 Master ro=OFF, db2 Master ro=OFF).

Fix direction: the last-non-slave inference must not fire when more than one
non-slave exists (that ambiguity IS post-split dual-master — ERR00063's case),
and a returning server must never be un-read-onlied by inference — only by an
explicit rejoin/failback decision. This is the "directional failback" gap from
the arbitration ship plan.

## Bug 2 — crash re-materialization loop

node1, every tick since 19:07:55:

```
Materialized peer crash for db1 (elected master db2) from peer failoverHistory
Inter cluster multi-source check drop unlinked server db1 ...
```

The materialized crash is purged at the top of every discovery pass
(cluster_topo.go HasAllDbUp purge: in dual-master both DBs look "up" and
healthy, so hasUnresolvedCrash sees nothing unresolved), then the extra-master
branch re-fetches and re-materializes it — a symptom of the stalled rejoin, not
an independent dedup bug.

## Confirmed in passing — heartbeat scope-leak is NOT cosmetic

During the window DR's recovery ran `SET GLOBAL read_only=0` (module=Rejoin) on
db1 of **flacq (19:02:01), crm (19:02:10, :21), curepipe (19:02:16)** — bystander
clusters dragged into the leaked server-level split-brain. Harmless this time
(those were already the RW masters), but it proves the leak drives real
write-state actions on production-adjacent clusters. The known fix (scope the
heartbeat cut to the armed cluster) moves from nice-to-have to must-do.

## Aftermath / cleanup

belair left untouched for inspection: dual-RW (db1+db2), ERR00063 open on
node1, no client traffic. Cleanup = set db1 read-only, rejoin it as slave of
db2.

## Root cause (settled after review with Stephane)

The SIMULATOR was unfaithful: it cut the control plane (arbitrator, both
heartbeats, majority->master) but NOT the minority's data plane to the
majority side. In a real partition node1 also loses db2, so db2 is genuinely
Failed on node1 during the split — and at resolve the Failed->up transitions
fire on each side exactly where the (correct, morning-gated) rejoin design
expects them. Fix = **op 5/5**: `SimulateServerFailure` per-host cut, armed by
`setMinorityWithMaster` on every non-master server (implemented, VSPLIT0006).

History of the rejoin CHANGE MASTER for this scenario:
- pre-f4d783cac (until 2026-07-14 morning): fired MID-split (no split gate) —
  wrong time: empty capture, collision with db1's diverging tail, SlaveErr.
- f4d783cac (morning): `!IsSplitBrain` gate added — correct delay, but with the
  unfaithful sim the one-shot trigger was consumed inside the race window on DR
  and never existed on node1 → fired NEVER → this run's dual-master.
- op-5 (evening): faithful cuts put the Failed->up transitions where a real
  partition puts them; the morning design then carries the rejoin unmodified.
  Autorejoin stays exactly as it always was (default ON — single-repman rejoins
  automatically on equal GTID; arbitration only DELAYS the rejoin past the
  both-sides-wrote window; diverged handling stays behind its own knobs).

## Follow-ups

1. Rerun testSetMinorityWithMaster with op-5 and verify db1 demotes + rejoins
   at resolve (watch cluster_topo.go:318/:326 last-non-slave + read_only=0 —
   if it still fires with two non-slaves present, revisit the inference gate).
2. Scope the server-level heartbeat sim/split to the armed cluster (leak:
   during this run DR ran read_only=0 via module=Rejoin on db1 of flacq, crm,
   curepipe — bystanders dragged into the leaked split-brain).
3. `testSetMinorityWithMaster` still composes the minority manually (op1+op2);
   consider switching it to the new one-call `SimulateMinority()` +
   heartbeat cut, so the test exercises the same entry point operators use.
