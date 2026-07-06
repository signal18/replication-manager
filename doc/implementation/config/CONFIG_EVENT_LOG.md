# Config Event Log — peer-symmetric config change replication

Status: DESIGN APPROVED 2026-07-06 (svaroqui) — not yet implemented.

## Problem

Cluster config sync between peers is one-directional by design: the active
side is authoritative, standby peers pull and apply `<cluster>.toml` from the
shared config git repo (isolated `.config/` clone). Mutations born on a peer
that is *standby* for that cluster are stranded:

- A marketplace visitor signup (`r1mfd@powerscrews.com` on belair) was
  accepted by the main instance while belair was standby there. The user was
  saved into main's local `belair.toml`, but the active peer (DR) never pulls,
  so the user never reaches it — and the next active-side push will make the
  standby reload the active version and silently wipe the local user.
- Any API write (user add/drop, grant change, dynamic setting) accepted on
  the standby side of a cluster has the same fate.

The BO `inject.toml` channel (`.pull/<cluster>/inject.toml`, consumed by
`CheckInjectConfig`) is peer-symmetric but BO-authored, one-shot
(consume-and-truncate), and replace-not-merge. It is not suitable for
peer-originated mutations.

## Design

### Log file

Each instance appends locally-born config mutations to its own append-only
log in the shared config git repo:

```
event-changed.<RRBid>.log
```

`<RRBid>` is the instance's unique id (`arbitration-unique-id`). One log per
instance covers all clusters. The log rides the existing git push/pull —
no new transport.

### Entry schema

One line per event (JSON or TSV, decided at implementation):

| field       | content                                                      |
|-------------|--------------------------------------------------------------|
| ts          | event timestamp (RFC3339, authoring instance clock)          |
| cluster     | cluster name                                                 |
| author      | authoring instance RRBid                                     |
| action      | `set` / `add` / `drop`                                       |
| key         | variable name (e.g. `api-credentials-external`)              |
| value       | new value; **secrets always as `hash_` ciphertext**, never plaintext (peers share the encryption key via git; stable ciphertext — `GetStableEncryptedValue`, ed92556a2 — makes this deterministic) |

### Cursor — "apply the diff between two saves"

Each instance persists, as part of its own save, one cursor (byte offset /
line number) per known peer log. On every save cycle (the existing
`counter%60` SaveConfig ride-along in the main loop — no new scheduler):

1. Read each `event-changed.<id>.log` where `id != self`.
2. Apply, in order, only the events between the stored cursor and the head.
3. Advance and persist the cursor with the save.

Replay is idempotent by construction: the cursor never re-crosses an event.
A missing/blown cursor is not an error: the peer falls back to the existing
full-state standby pull, then sets its cursor to the current head.

## Approved decisions

1. **Echo suppression** — only *locally-originated* mutations end up in the
   log. Enforced by save-diff authoring with provenance subtraction (see
   "Authoring and echo prevention" below), NOT by hooking setters (rejected
   2026-07-06: touches dozens of call sites and every future setter must
   remember to log).
2. **Secrets** — log entries carry the `hash_` ciphertext only. Plaintext
   never enters the log.
3. **Conflicts** — same key changed on both peers between saves: apply in
   timestamp order, last-writer-wins. No coupling to arbitration status.
4. **Rotation** — logs are append-only in a git repo and grow forever.
   Policy: rotate once every known peer's cursor has passed an offset, or
   size-based rotation; a peer that is too far behind (cursor older than the
   rotated tail) falls back to the full config pull, which already exists as
   the standby sync baseline.
5. **Scope** — log every variable change, and peers **re-apply every event**
   (decided 2026-07-06: no apply-allowlist, no scope exclusion). This
   includes immutable `scope:"server"` settings: a runtime change to one is
   lost on restart anyway, so replaying it on a peer carries no more risk
   than making it locally — and peers converging on the same runtime value
   is the desired behaviour. LWW (decision 3) resolves concurrent writes.
   The log doubles as a config-change audit trail, valuable even without
   arbitration or peers.

## Authoring and echo prevention (approved 2026-07-06)

Events are DERIVED FROM THE SAVE, not captured in setters. Everything
happens in one place, the SaveConfig cycle:

1. **Replay first.** Read peer logs, apply events past the cursor (with the
   LWW check of decision 3). While applying, record each applied key in a
   provenance table: `(cluster, key) → (value, ts, author)`.
2. **Diff the save.** Before SaveConfig overwrites `<cluster>.toml`,
   key-level-diff the new content against the previous save's content.
   Every differing key is a candidate event.
3. **Subtract echoes.** Candidate whose current value came from a peer
   replay (provenance author ≠ self, value matches) is skipped — we just
   applied it, it is not ours. Everything left is by definition a
   locally-born change: append to `event-changed.<RRBid>.log` with
   author = self, ts = now, provenance updated to self.
4. **Persist together.** Cursors + provenance table are saved with the
   config. A lost table is recovered by the full-pull baseline + cursor
   reset, same as decision 4.

Why this cannot ping-pong: an applied peer event is subtracted from the
save diff by construction (step 3), so it is never re-emitted; cursors only
move forward, so it applies at most once per instance; per-key LWW with
author-id tie-break makes all peers pick the same winner, so a stale event
arriving late is skipped instead of causing divergence.

Accepted trade-offs of save-diff authoring:
- Timestamps are save-cycle-coarse (~2 min): "last writer" means "last
  save", resolved deterministically by author-id ties.
- Set-then-revert within one save window emits no event (net change nil);
  the audit trail loses the transient.
- Attribution is thinner than a setter hook (no acting username in the
  event); if the audit view later needs "changed by whom", take it from the
  security log rather than re-hooking setters.
- Secrets diff in their stable `hash_` ciphertext form (ed92556a2), which
  is deterministic across saves — a prerequisite this design relies on.

## Interaction with existing mechanisms

- **Standby full pull** (`.config/` clone, applies `<cluster>.toml` byte-diff
  to standby clusters) remains the baseline full-state sync and the recovery
  path when cursors are lost or logs rotated away.
- **inject.toml** (BO channel) is unchanged — BO-authored one-shot injection.
- **GitPush** stays ungated; the log is pushed with the regular config push.
- The authoritative active-side `<cluster>.toml` will eventually contain the
  merged result (e.g. the added user), making old events redundant — harmless
  because the cursor already passed them.
- **Audit/GUI (future)**: the log is the natural source for a per-cluster
  config-change history view, and the Config state machine can surface
  change counts (e.g. "N config changes since …").

## Non-goals

- Not a general CRDT: scalar keys rely on LWW; no vector clocks.
- No BO involvement; marketplace flows may later choose to write through
  this same path but that is out of scope here.
