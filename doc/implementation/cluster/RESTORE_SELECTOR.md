# Generic Restore Selector

A single declarative selector replaces every hand-rolled backup lookup in the
rejoin/reseed paths. A restore = *score/rank the known backups by all the preferences →
take the best available → obtain it (local/fetch/live) → restore → re-attach with the
full-param CHANGE MASTER (`GetChangeMasterBaseOptForSlave`)*.

**Some dimensions gate (hard filter), the rest rank.**

- **Hard gates** — a mismatch is excluded, never relaxed:
  - `type` and `tool` — the restore **mechanism**; you can't relax a physical request into a logical restore, or one tool into another.
  - `time` replayability (`notafterlastmasterbinlog` etc.) — **correctness**; a non-replayable / diverged backup would produce a broken restore.
  - `proven:true` — **assurance**; only test-restore-validated backups.
- **Ranking preferences** — resolver returns the best available and **relaxes** rather than fails: `origin`, `repo`, `safety`, `order` (e.g. `safety=preservenetwork` but only a remote copy exists → use it, ranked last).

## Parameters

| Dimension | Values | Picks… |
|---|---|---|
| `origin` | `any` · `master` · `mine` | whose backup |
| `repo` | `any` · `local` · `remote` · `live` | where to get it (local disk / restic remote / live dump) |
| `type` | `logical` · `physical` · `parallel` · `compress` · `encrypt` · `gtid` · `can-partial-restorable` | which kind/capability (**gate**; empty = none) |
| `tool` | `mysqldump` · `mariadump` · `mariadbbackup` · `xtrabackup` · `innodbhotbackup` · `mydumper` | which backup/restore tool (**gate**; empty = none) |
| `proven` | `true` · `false` | **boolean gate** — require a backup validated by an actual test-restore (proof of restore) |
| `time` | `any` · `notafterlastmasterbinlog` · `nobeforemasterbinlog` · `pitr` | position/time validity relative to the master's binlog range |
| `safety` | `preservedbdisk` · `preserverepmandisk` · `preservenetwork` · `preservemasterload` · `preservemasterlock` · `preservecpu` · `preservediskio` · `preservememory` | which resource(s) the restore must NOT stress (list; each ranks-not-gates; empty = any) |
| `order` | ranked list, e.g. `["last","local"]` / `["logical","last"]` | how to choose among matches |

> `can-partial-restorable` = **mydumper** + **splitdump** (per-table / per-chunk output → selective restore possible; a monolithic `mysqldump.sql.gz` or a physical archive is not partial-restorable).
>
> `proven` (own boolean **gate**) = the backup has been *validated by an actual test-restore* (and/or checksum), so it's known-restorable. Unlike the ranking preferences, `proven:true` **gates** (only proven backups — no relaxing). **NEW mechanism required:** no backup carries this today; a verify step (test-restore + stamp `Proven=true` on the catalog entry) must be built. Until then `proven:true` matches nothing.

### `time` — replayability window vs the master's binlog `[oldest … head]`

| Value | Keeps backups whose position is… | Why |
|---|---|---|
| `notafterlastmasterbinlog` | **not beyond** the master's head | not diverged / not from a future timeline |
| `nobeforemasterbinlog` | **not before** the master's oldest live binlog | master can still serve it → direct catch-up |
| `pitr` | toward a point-in-time target (restore + **binlog replay**) | feeds the existing PITR path; also covers "backup older than the live window → replay archived binlogs up into it" |
| `any` | (no constraint) | — |

A directly catch-up-able backup satisfies **both** `nobeforemasterbinlog` **and** `notafterlastmasterbinlog` (inside the live window). When a backup is older than the live window, `pitr` (restore + archived-binlog replay via `AutorejoinBackupBinlog`) reaches the master's head.

### `safety` — resource to protect

| Value | Constrains the strategy to avoid… | Favours |
|---|---|---|
| `preservedbdisk` | extra DB-host disk **space** (no full staged copy on the DB node) | streaming restore, physical SST direct-apply |
| `preserverepmandisk` | staging a copy on the repman host | in-memory pipe (e.g. `JobRejoinMysqldumpFromSource`), stream-through |
| `preservenetwork` | cross-node transfer / bandwidth | `repo=local` backup, never live-dump / remote-fetch |
| `preservemasterload` | the master's CPU/IO | a stored backup — never a live mysqldump **from** the master |
| `preservemasterlock` | locking the master (`FTWRL` / long read locks) | tools/positions that don't lock the source |
| `preservecpu` | CPU on the repman/DB host | avoid heavy compress-decompress, high mydumper parallelism |
| `preservediskio` | DB-host disk **throughput** (not space) | a restore that won't saturate the data volume's IO |
| `preservememory` | RAM pressure | small in-memory footprint, stream-through |

*(empty `safety` = no constraint = fastest available.)*

## Struct

```go
type RestoreSelector struct {
    // bool
    Proven bool     // GATE: require a backup validated by test-restore (NEW: needs a verify mechanism)

    // []string — GATE whitelists: empty = none (that's what makes them gate)
    Type   []string // logical | physical | parallel | compress | encrypt | gtid | can-partial-restorable
    Tool   []string // mysqldump | mariadump | mariadbbackup | xtrabackup | innodbhotbackup | mydumper

    // []string — ranking preferences: empty = any (no preference)
    Safety []string // preserve{dbdisk,repmandisk,network,masterload,masterlock,cpu,diskio,memory}  (combinable)
    Order  []string // e.g. ["last","local"] or ["logical","last"]

    // string
    Origin    string // any | master | mine
    Repo      string // any | local | remote | live
    Time      string // any | notafterlastmasterbinlog | nobeforemasterbinlog | pitr
    StartGtid string // GTID boundary — PITR target AND backup filtering (not pitr-only)

    // time.Time
    StartTime time.Time // time boundary — PITR target AND backup filtering (not pitr-only)
}
```

## Method presets

| Method | Selector |
|---|---|
| rejoin logical ("any backup") | `{Origin:"any", Repo:"any", Type:["logical"], Time:"notafterlastmasterbinlog", Safety:["any"], Order:["last","local"]}` |
| reseed from master | `{Origin:"master", Repo:"live", Safety:["any"]}` |
| reseed from master, spare the master | `{Origin:"master", Repo:"live", Safety:["preservenetwork"]}` → forced to a stored backup instead |
| restic remote fetch | `{Repo:"remote"}` |
| physical rejoin | `{Type:["physical"], Order:["last","local"]}` |

## Re-attach (shared, final step of every restore)

- **Anchor** = backup metadata (`BinLogGtid` / `BinLogFileName`+`Pos`), or `Crash.FailoverIOGtid` for flashback.
- **Params** = `GetChangeMasterBaseOptForSlave` (SSL / delay / channel / multi-source).
- **Apply** = one `CHANGE MASTER` from *anchor + full params*, as the last step.
- Dumps use `--master-data=2` so mysqldump's minimal embed is commented out and never overrides.

## Existing paths this replaces

| Current path | file:line | Becomes a preset |
|---|---|---|
| `JobFlashbackLogicalBackup` inline lookup | srv_job_backup.go:1388 | logical rejoin preset |
| `JobReseedLogicalBackupPrepare` inline lookup | srv_job_backup.go:600 | per-server reseed preset |
| `RejoinDirectDump` source pick | srv_rejoin.go:468 | `Repo:"live"` preset |
| `handlerMuxServerReseedRestic` | server/api_database.go | `Repo:"remote"` preset |
| shared restore core (kept) | `reseedMysqldumpWithMetadata` srv_job_backup.go:1013 | unchanged — the executor |

## Unified backup catalog (the selector's source of truth)

The selector filters/ranks over **one unified backup list** that indexes **any type of
backup we take**, across **both `local` and `repo`**. No per-method / per-server ad-hoc
lookups — every restore path queries this same catalog.

One entry per backup, self-describing so `origin` / `repo` / `type` / `time` / `order`
can all be evaluated against it:

```go
type BackupCatalogEntry struct {
    Server    string   // which node it was taken from  -> origin
    Location  string   // local | repo(restic/remote)   -> repo
    Kind      string   // logical | physical            -> type
    Tool      string   // mysqldump | mydumper | splitdump | mariabackup | ...
    Caps      []string // parallel, compress, encrypt, gtid, can-partial-restorable
    Proven    bool     // validated by a test-restore (NEW: needs a verify mechanism to set it)
    Timestamp int64    // -> order "last"
    Gtid      string   // captured position (Ahmad's metadata) -> time filters
    BinFile   string
    BinPos    string
    Path      string   // local path or repo/snapshot id
}
```

Built by merging what already exists into one list: per-server `LastBackupMeta`
(local) **+** Restic snapshots (`ResticManager`, repo) **+** on-disk backup dirs — de-duped,
one row per backup, any type. That merged list is what `RestoreSelector` runs against.

## Implementation notes — ranking / query

**Ranking: stdlib, no library.** Go 1.25 (`slices` already imported). Dynamic multi-key
ranking from the `order` list via `slices.SortFunc` + a comparator fold:

```go
cmps := map[string]func(a, b Backup) int{
    "last":    func(a, b Backup) int { return b.Ts - a.Ts },        // newest first
    "local":   func(a, b Backup) int { return boolCmp(a.Local, b.Local) },
    "logical": func(a, b Backup) int { return typeRank(a) - typeRank(b) },
}
slices.SortFunc(cands, func(a, b Backup) int {
    for _, k := range sel.Order {
        if c := cmps[k](a, b); c != 0 {
            return c
        }
    }
    return 0
})
```

**Full-text search library: not needed (overkill).** Pure-Go options exist — **Bleve**
(`blevesearch/bleve`) and **Bluge** (`blugelabs/bluge`) — but they're built to index large
*text corpora*. The catalog here is a handful of *structured records*
(`{origin, repo, type, timestamp, caps}`); a **filter + multi-key sort over a slice** is
simpler, dependency-free, and faster than standing up an index. If we later want a
query-DSL feel to the selector, add a small grammar over the structured fields — still no
search engine.

## Config & the manual → auto loop

`autorejoin-backup-selector` (config, JSON `RestoreSelector`) declares WHICH backup an
**automatic** rejoin restores from. Empty = default (`{Type:["logical"], Order:["last","local"]}`).
Parsed by `getAutorejoinBackupSelector()`; the restore dispatch forces `Tool = [backtype]`.

The loop this closes:
1. Operator picks/validates a restore **manually** via the selector (tries origin/type/order/safety).
2. When it works, they save that selector JSON into `autorejoin-backup-selector`.
3. The **automatic** rejoin now uses the human-validated choice — manual is how the auto path *earns trust*.

**Future — `valid_selector.json`:** a **per-client** list of restore methods/selectors that have been
**tested and confirmed working at that client**. A successful manual restore is recorded here; the
operator (and the auto path) picks from this validated set — so a client only ever auto-rejoins with a
method already **proven on their own setup**.

## Open questions (decide before building the resolver)

1. ~~**Catalog** — what source of truth?~~ **RESOLVED: a unified backup catalog**
   (any type, local + repo) — see the section above. Remaining sub-task: merge
   `LastBackupMeta` + Restic snapshots + on-disk dirs into that one list.
2. ~~**`order` semantics** — hard filter or sort?~~ **RESOLVED: sort/preference** —
   the model ranks, it doesn't gate. `["logical","last"]` = "prefer logical, then newest",
   falls through to non-logical if none.
3. ~~**`can-partial-restorable`** — what qualifies?~~ **RESOLVED: mydumper + splitdump.**
4. ~~**`safety` conflicts** — fail or relax?~~ **RESOLVED: relax** — safety is a preference,
   not a gate; rank the disfavoured option last and use it rather than fail.

**All open questions resolved.** Model: **hard gates** = `type` · `tool` · `time` · `proven`
(mismatch excluded, never relaxed); **ranking preferences** = `origin` · `repo` · `safety` · `order`
(relax to best-available).
