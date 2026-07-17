# Split-Brain Orchestration Across Three Datacenters

How replication-manager behaves when a dual-repman / arbitrator topology is cut
in half, and how the **split-brain simulator** reproduces each case through the
real fail-safe paths.

## Topology

Three clouds, one per datacenter:

- **DC1** — master `db1` + the main repman (uid 1).
- **DC2** — slaves `db2`/`db3` + the DR repman (uid 2).
- **DC3** — the tie-breaker at signal18: a dedicated **arbitrator** (Support plan)
  or a third repman standing in as arbitrator (Free plan).

On a split, the isolated side becomes the **minority** and its master steps down;
the side that keeps the arbitrator is the **majority** and promotes a slave to
`NEW MASTER`. What differs between plans is the **recovery** — so the frames below
start from the Free plan (where we come from), then the Support plan that removes
the pain, then the symmetric split where repman must fence both sides.

---

## 1. Free plan — where we come from

No dedicated arbitrator; a third repman stands in for DC3, so tie-breaking is
weaker and the split lingers. Replication is cut, so the isolated old master keeps
writing binlogs no slave consumes — a growing pile of delta transactions that
**cannot be flashed back**. The only way home is a full **restore from backup**.

![Free plan orchestration](img/sbs-free.svg)

## 2. Support plan — the fix

A dedicated arbitrator in DC3 is the strong tie-breaker: the minority is detected
fast and yields cleanly (master read-only, `Active → Standby`). With
[`arbitration-minority-freeze`](#the-ftwrl-minority-fence) the old master is
**FTWRL-frozen** so the proxy cannot write to it. Its divergence is a single,
flashback-able delta — **rejoin in place, in seconds**, never a restore.

![Support plan orchestration](img/sbs-support.svg)

## 3. Lose–Lose — the symmetric split

Both DCs still reach the arbitrator, but not each other. Each side could naïvely
read a 2-of-3 majority and promote — a real split-brain. To prevent it, repman
does the safe thing and **grants neither**: both proxies are cut and both masters
are FTWRL-frozen. The price is a full write outage until an operator (or a
directional-failback policy) elects a survivor — but there is **zero divergence**,
so recovery is a clean unfreeze rather than a restore.

![Lose–Lose symmetric split](img/sbs-lose-lose.svg)

---

## The FTWRL minority fence

`arbitration-minority-freeze` (`config.Config.ArbitrationMinorityFreeze`, default
`false`):

> Hold `FLUSH TABLES WITH READ LOCK` on a minority master during split brain
> (blocks even `SUPER` writes; released when the split resolves) — true protection
> `read_only` cannot give on MariaDB.

This is the teal link cut on the minority side of the diagrams: repman actively
blocks writes to the old master, on top of the network partition, so no client can
diverge it while the majority promotes.

## The split-brain simulator

Each endpoint injects a **real** cut through the same code path a physical failure
would trigger — nothing is faked at the state-machine level. All are per-cluster
POSTs under:

```
/api/clusters/{clusterName}/test/split-brain-simulator/{action}
```

They require the `cluster-test` grant, are bounded by `?duration=<seconds>`
(default 120), and auto-restore when the timer expires.

| Action | Effect |
|--------|--------|
| `simulate-minority` | Marks **this** repman the minority side, instance-wide, on every cluster it drives. While armed the arbitrator link reads as severed, so this side cannot confirm authority and steps down through the real fail-safe path (master read-only, `Active → Standby` yield, peers `Suspect`). Aim it at the **active** instance — standby is the outcome you observe, never an input. |
| `simulate-arbitrator-failure` | Cuts this repman's link to the arbitrator (DC3): it stops reporting and bails out of the election so its arbitrator row expires, exactly as if the DC3 link were physically severed. |
| `simulate-heartbeat-failure` | Darkens this node's inbound `/api/heartbeat` handler so the peer's outbound heartbeat times out, producing a real peer-unreachable detection (`GWARN006`). Server-level and one-directional by design. |
| `simulate-master-failure` | Cuts this repman's DB link to the master only, leaving the slaves reachable so the majority side can still promote. Used when the master is colocated on the isolated (minority) side. Also resets the failover counters so a prior test cycle cannot veto this failover. |
| `restore` | Clears every simulated cut on this instance — the per-cluster arbitrator/database/master cuts and the server-level heartbeat. |

**Reproducing each frame**

- **Support / Free minority yield** — `simulate-minority` on the active (DC1)
  instance; observe it go read-only and yield `Active → Standby` while the DR side
  promotes.
- **Master-colocated failover** — `simulate-master-failure` on the DC1 instance;
  the majority (DC2) promotes a `NEW MASTER`.
- **Lose–Lose** — arm the minority/heartbeat cut on **both** sides while leaving
  each arbitrator link healthy: neither can prove a majority, and (with the freeze
  enabled) both masters are held read-only.

Always finish with `restore` (or let the `duration` expire) to release the cuts.

---

*Diagrams: `doc/implementation/cluster/img/sbs-{free,support,lose-lose}.svg`
— self-contained, theme-aware SVG.*
