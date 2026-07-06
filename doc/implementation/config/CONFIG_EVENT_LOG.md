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

1. **Echo suppression** — only *locally-originated* mutations are logged
   (API set, user add/drop, GUI switch). Applying a peer's event bypasses
   the normal "mutate + log" path and must not append to our own log,
   otherwise two peers ping-pong forever.
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
