# Split-brain / arbitration test coverage (2026-07-15)

What the regression suite covers for the minority/majority model, and the gap.

## Covered — clear minority vs majority

A partition forms a **minority** when it loses BOTH the arbitrator (DC3) AND the
peer heartbeat, while the other side keeps the arbitrator. The regtests reproduce
this faithfully at the application level (`regtest/test_split_brain.go`), 6 ops:

```
op 1/6 LOCAL  : cut repman1 -> arbitrator link (all clusters — a real DC partition
                loses the arbitrator for every cluster at once)
op 2/6 LOCAL  : sever repman1 inbound heartbeat (repman2 -> repman1 times out)
op 3/6 REMOTE : sever repman2 inbound heartbeat (repman1 -> repman2 times out)
op 4/6 REMOTE : cut repman2 -> master link (master colocated on the minority)
op 5/6 LOCAL  : stop io_thread on every majority-side slave (starve the
                cross-partition replication channel — else the divergent tail
                never exists, capture is always empty)
op 6/6 LOCAL  : cut repman1 -> every majority-side database (so they go genuinely
                Failed on the minority, firing the real Failed->up rejoin at resolve)
```

Test cases:
- `testSetMinorityWithMaster` — master colocated on the minority side; the minority
  fences it and delegates failover to the majority; asserts no dual master, no
  failover on the minority.
- `testSetMinorityWithMasterSysbench` — same, with client DML driven into the split
  window so the divergent tail is real (row-based, flashback-able), exercising the
  lost-events capture / Last Divergence viewer.
- `testSetMinorityWithoutMaster` — master on the majority side; the minority holds
  only a slave, its master looks failed, it enters the failover path but must NOT
  promote (minority has no authority).

These validate: minority yields (Active->Standby), old master fenced read-only,
majority elects a new master, and on resolve the minority regains Active and
rejoins the old master (one-shot, disk-truth crash history, per the unified rejoin).

## NOT covered — equal partitions (the tie)

The **symmetric / equal-partition** case has **no test case yet**: when the split
is such that neither side is a clear minority — e.g. both repman are cut from each
other but BOTH can still reach the arbitrator, or any topology where the two
partitions have equal claim and neither loses the arbitrator.

This is exactly the case where the arbitrator's distinctive "loser/loser" power is
needed (see ARBITRATOR_ROLE.md): it is the only party that sees both sides, so it
can force both to stand down rather than let a contested single winner emerge.
Because that behavior is not fully built (the contest state is still an in-memory
latch, not DB-backed), and there is no regtest driving the reach-both tie, this
scenario is a **known coverage gap** to close alongside the arbitrator contest-in-DB
fix.

## Note

The faithful 6-op simulation and the disk-truth crash history behind these tests
were reworked on 2026-07-14/15 (unified rejoin). Several regressions were
introduced and fixed during that work (rejoin loops, an empty-delta
misclassification, a broken vite build) — the current suite reflects the
post-fix behavior; treat runs before 2026-07-15 evening as historical.
