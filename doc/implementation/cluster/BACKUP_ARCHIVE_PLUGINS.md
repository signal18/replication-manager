# Backup archives as plugins (design)

Status: **design**, first step coding (2026-10-16): the archive list and its migration,
#2077 (branch `feat/archive-list`). Same model as the credential stores
(`doc/implementation/config/CREDENTIAL_STORES.md`): one interface, an internal plugin
for what every repman ships, external plugins for the rest. Related: #1987 (remove the
original once archived), #2023 (backup verification), BKU/BAU metering, T2, T25.

## Why

An **archive** is the copy of a backup artifact that leaves the local backup disk, its
versions, and the way back. Today the only archive is restic (`backup-archive-mode`:
`none`, `restic-local`, `restic-sftp`, `restic-aws`), and restic is not behind a boundary:

- **restic syntax in cluster code**: S3 resolution, env building, tags, path templates
  and purge filters (`cluster/cluster_bck.go` ~1433-2196) speak restic; backup metadata
  stores `ResticSnapshotID` / `ResticFilePath` (`cluster/srv_bck.go`);
- **the queue lives inside the backend**: callers reach into `ResticManager.Mutex`,
  `TaskQueue`, `Backups`, `BackupStat` (`cluster_bck.go`, `cluster_get.go`,
  `cluster_bku.go`), and the API exposes the restic queue (`/restic/task-queue/...`);
- **reseed knows restic's mount layout** (`cluster/srv_job_restic.go`, restore, dump,
  FUSE mount);
- **mode duality**: the legacy `BackupRestic` / `BackupResticAws` bools are still what the
  code reads, derived from the mode (`applyBackupArchiveModeFlags`), and the local/sftp
  split is a path prefix;
- **a bug it hides**: BAU reads the remote archive size only when `backup-restic-repository`
  is S3 or SFTP (`resticRepositoryIsRemote`, `cluster/cluster_bku.go:97`). An SFTP repo set
  in `backup-restic-local-repository`, or an S3 repo built from the bucket fields, likely
  meters 0 BAU (to confirm and file separately).

Adding a backend (native S3, rclone, a client's own storage) means touching all of these.

**In scope**: pushing local artifacts to an archive, listing, proving, fetching back,
streaming, retention, stats, check, mount where the backend has one.
**Out of scope**: what lands *on* the local disk (PostgreSQL WAL from the jobs sidecar,
remote dbjobs dumps), and backup streaming (`backup-streaming*`, a goofys S3 mount of the
whole backups directory: a filesystem, not an archive; see open questions).

## The interface

```go
// A place backup artifacts are archived. Repman archives only through it.
type ArchiveStore interface {
    Name() string
    Capabilities() ArchiveCaps

    Init(ctx context.Context) error
    // Push archives one artifact: its files (the dump or the physical backup, its
    // .hmac sidecar, its metadata) with tags; returns an opaque reference.
    Push(ctx context.Context, a Artifact, progress func(Progress)) (ArchiveRef, error)
    List(ctx context.Context, filter Tags) ([]ArchivedArtifact, error)
    // Contains proves the archive holds these paths for ref (remove-original gate).
    Contains(ctx context.Context, ref ArchiveRef, paths []string) (missing []string, err error)
    // Fetch restores paths of ref into dir (fetch-back, restore, reseed).
    Fetch(ctx context.Context, ref ArchiveRef, paths []string, dir string, progress func(Progress)) error
    // Stream reads one file of ref without restoring it (physical reseed, dump reseed).
    Stream(ctx context.Context, ref ArchiveRef, path string) (io.ReadCloser, error)
    // Forget applies a retention decision repman made; protected refs are never removed.
    Forget(ctx context.Context, remove []ArchiveRef, protected []ArchiveRef) error
    Stats(ctx context.Context) (ArchiveStats, error) // bytes held, remote or local disk
    Check(ctx context.Context) error
    Unlock(ctx context.Context, force bool) error
}

type ArchiveCaps struct {
    Remote     bool // leaves the cluster: metered in BAU, not in BKU
    Versioned  bool // keeps several versions of one artifact
    Encrypts   bool // encrypts at rest itself (on top of repman's backup-encryption)
    Mount      bool // optional Mount/Unmount (restic FUSE): reseed by mount
    Copy       bool // copies refs to another store of the same plugin
    ChangeKey  bool // rotates its own encryption key
}
```

- **`ArchiveRef` is opaque** and replaces `ResticSnapshotID` in the backup metadata (a
  restic ref is its snapshot id: existing metadata reads as-is).
- **Artifacts go by path**: the plugin reads the files from the local backup directory
  itself. Backup data never crosses the plugin pipe.
- **Optional operations** (`Mount`, `Copy`, `ChangeKey`, `Wipe`) sit behind their
  capability; repman greys the GUI action with the reason when a plugin lacks it.

## What stays in repman, what moves to the plugin

| Repman (generic, once) | Plugin (per backend) |
|---|---|
| the archive **queue**: order, pause, resume, cancel, move, one op at a time per store (moved out of `ResticManager`) | executing one operation |
| **retention decisions**: which refs to forget, from the purge strategy and disk pressure | `Forget` of the refs it is given |
| **remove original** (#1987): the gate, WARN0238, the `.archive-restore-*` work dir | `Contains` (the proof), `Fetch` |
| **protected refs**: a ref whose original was removed is passed as protected to every `Forget` (today restic retention can forget it) | honours the protected list |
| **verification** (#2023): proving a backup, now also its archived copy (`Fetch` to a work dir, then the usual integrity check) | `Fetch` |
| **metering**: BKU for local disk, BAU from `Stats` when `Remote` (fixes the remote check above) | `Stats` |
| **states**: the archive states keep their keys (WARN0093 fetch, WARN0094 purge, WARN0095 init/unlock/copy, WARN0134 locked, WARN0145/0146 config backup/restore); a plugin error carries a kind (locked, auth, unreachable, full, missing) that picks the state | error kinds |
| **local encryption** (`backup-encryption`, HMAC sidecar): the artifact and its sidecar are pushed together as one `Artifact` | its own at-rest encryption, if `Encrypts` |
| **credentials**: from the credential chain (`secret_kv`), handed to the plugin at start | uses them, never stores them |

## One to N archives per cluster

A cluster has **1 to N archives**, each one an `ArchiveStore` with its own plugin and
settings (for example a local restic repository and an S3 one, or the same plugin twice
with two backends). `none` is simply an empty list.

### Names and the connected archive

- An archive is a **name and a plugin type** plus the plugin's settings. The name is a
  label the user chooses (`archive-bso-s3`) and can rename: unique in the cluster,
  lowercase letters, digits, `-` and `_`. Repman names the archive the migration creates,
  and one created without a name, `archive-<type>-<n>`: `archive-local-1` is the archive of
  restic enabled today.
- Each plugin comes with its **typed config** (its manifest): every setting with its type
  (`string`, `int`, `bool`, `enum` with its values, `secret`) and its capabilities:
  `edit`, `remove` (the plugin can delete its content), `probe` (open an existing
  repository, or initialise a new one). The forms and checks are drawn from it.
- **Attach, detach, remove.** The cluster attaches one archive; the one it leaves is
  detached: it stays in the list with its content, still invoiced (some archives are
  billed on their content), shown with its size. **Remove always deletes the content**,
  then takes the archive out of the list, so its invoice stops: content left behind would
  be invoiced and no longer visible. Refused while attached, and while the archive holds
  the only copy of a backup (#1987). Exception: an archive attached read-only to another
  cluster's content (#2089) only leaves this cluster's list; the source is invoiced for it.
- Getting rid of an old, heavy archive: create `archive-local-2`, attach it
  (`archive-local-1` is detached), remove `archive-local-1`.
- The cluster is **connected to one archive**: `backup-archive-name` (a plain cluster
  setting, empty for none). The backups go to it and its snapshots are the ones listed.
  The user switches by changing that one setting; the archive left keeps its settings as
  they were run.
- The other archives of the list are filled from the connected one (`copy-from`), or wait
  to be connected.
- `backup-archive-mode` stays as a setter over the list: `none` disconnects; another mode
  changes the connected archive's plugin, or connects the first archive with that plugin,
  or a new `archive-<type>-1`.
- Migration (#2077): a cluster without a list gets `archive-local-1` (or `archive-sftp-1`,
  `archive-s3-1`) from its `backup-restic-*` settings and secrets, connected when `backup-archive-mode` is not none. With the mode none and
  customised settings, it is kept but not connected, so nothing is lost. With every
  setting at its default, there is no list. The legacy keys are not written any more (no
  downgrade copy, decided): after the upgrade, don't go back to 3.1.44, and upgrade an
  active/standby pair together.

- **Each archive has its own policy**: which backup kinds it takes (logical, physical,
  binlogs), and its retention. A backup is pushed to every archive whose policy takes it,
  one queued operation per archive.
- **Metadata keeps one ref per archive**: `ArchiveRefs map[archive]ArchiveRef` replaces the
  single snapshot id (an existing `ResticSnapshotID` reads as the ref of the first restic
  archive).
- **Order is the read preference**: restore, reseed, fetch-back and verification read from
  the first archive in the list that holds the backup, then the next one when it is
  unreachable or misses it.
- **Remove original** (#1987): the original is removed only when every archive that should
  hold the backup has proved it (`Contains`); the setting can lower that to "at least one"
  per archive policy. WARN0238 names the archive that refused.
- **Protected refs**: while the original is removed, the backup's ref is protected in every
  archive, so the last copy is never forgotten by one archive's retention.
- **Copy**: an archive can be filled from another (a new S3 archive seeded from the local
  one): the plugin's native `Copy` when both are the same plugin (`restic copy` today),
  else repman fetches from one and pushes to the other.
- **Metering**: BAU is the sum of `Stats` of the `Remote` archives, BKU counts the local
  ones on disk; the BAU series gets an archive label.
- **States** carry the archive name, so one failing archive is visible without hiding the
  others.

### Required: one local restic and x remote restic

A cluster must be able to run **one local restic archive and any number of remote restic
archives** (sftp, S3, each its own bucket or host) at the same time. So each restic plugin
is **instantiated once per archive entry**, never once per cluster as `ResticManager` is
today. Each instance has its own:

- repository, password (its own `secret_kv` key: `archive.<name>.password`) and backend
  credentials (S3 keys, sftp identity);
- environment (`RESTIC_REPOSITORY`, `RESTIC_PASSWORD`, `AWS_*` built per instance, never
  shared), cache directory and lock;
- mount point (only the local one is usually mounted for reseed by mount);
- queue (two archives push in parallel; one archive runs one operation at a time) and
  states (WARN0093 etc. name the archive).

Example:

```toml
backup-archive-name = "archive-local-1"   # the connected archive: the backups go to it

[[archive]]                 # 1: local, the read preference
name    = "archive-local-1"
plugin  = "archive-restic-local"   # <WorkingDir>/backups/archive/<cluster>
retention = "keep-daily 7"

[[archive]]                 # 2: remote, filled by copy from archive-local-1
name    = "archive-bso-s3"
plugin  = "archive-restic-s3"
bucket  = "backups-paris"
endpoint = "https://s3.fr-par.example"
copy-from = "archive-local-1"     # restic copy: no second read of the backup
retention = "keep-daily 30, keep-monthly 12"

[[archive]]                 # 3: remote, another provider
name    = "archive-dr-sftp"
plugin  = "archive-restic-sftp"
host    = "dr-backup.example"
path    = "/srv/restic/<cluster>"
copy-from = "archive-local-1"
retention = "keep-weekly 8"
```

- **Push or copy**: the connected archive takes the backup from the backup directory; a
  remote archive is filled from another archive (`copy-from`) with `restic copy`.
  Copy reads the local repository, not the backup again, so it can run after the original
  was removed. A remote created by repman with `copy-from` is initialised with the
  source's chunker parameters (`copy_chunker_params`, `utils/backupmgr/restic_copy.go`),
  so deduplication holds across the copy.
- **Disk pressure** (`backup-restic-purge-oldest-on-disk-space`) applies to the local
  archive only: forgetting in a remote one frees nothing on the cluster's disk.
- **Metering**: the local archive is BKU (local disk, walked), each remote is BAU through
  its `Stats`, labelled by archive name.
- **Remove original**: with the default rule, the original goes only when the local
  archive and every remote one that should hold it have proved it; a remote filled by copy
  proves it after its copy ran.
- **Migration**: today's single `backup-archive-mode` becomes archive 1 (`restic-local`,
  `restic-sftp` or `restic-aws`). Today's `restic copy` is a one-off action that copies
  *into* the cluster's repository from a source repository (`/restic/copy`); it stays as
  the archive's `Copy` operation, and a standing remote copy is a new archive entry.

## The plugins

| Plugin | Kind | Remote | Its own settings (from today's `backup-restic-*`) |
|---|---|---|---|
| `archive-restic-local` | **internal** | no (BKU) | `repository` (path, default `<WorkingDir>/backups/archive/<cluster>`), `dir-mode`, `file-mode`, `purge-oldest-on-disk-space`, `purge-oldest-on-disk-threshold`, the `mount-*` settings |
| `archive-restic-sftp` | **internal** | yes (BAU) | `host`, `port`, `user`, `path`, `identity` (a `secret_kv` key; today there is no sftp setting: it relies on the repman user's ssh config or `additional-env`), `known-hosts` |
| `archive-restic-s3` | **internal** | yes (BAU) | `bucket`, `endpoint`, `region`, `prefix`, `access-key-id`, `access-secret` (a `secret_kv` key), `s3-mode` (legacy URL / bucket fields), `create-bucket` |
| `archive-<name>` | **external**: signed executable in `<ShareDir>/plugins/` | as declared | as declared in its manifest |

- **One plugin per backend, because each has its own settings**: the GUI page, the
  validation and the error messages of each plugin only show what applies to it (no S3
  fields on an sftp archive, no disk-pressure purge on a remote one).
- **One restic engine under the three**: the restic code (`utils/backupmgr`: exec, JSON
  parsing, task execution, copy, check, unlock, key change) is a shared library; each
  plugin only builds its repository string, its environment and its settings, and
  declares its capabilities.
- **Settings common to the three** stay on each archive entry: `password` (a `secret_kv`
  key per archive), `tags`, `host`, `retention` (today's `purge-*`), `timeout`,
  `dump-timeout`, `auto-init`, `repo-append-cluster`, `additional-env` (reserved
  variables still refused).
- **Copy across the family**: `restic copy` works between any two restic repositories, so
  `Copy` is native between `archive-restic-local`, `-sftp` and `-s3` (same engine), not
  only within one plugin.

- **Config**: `backup-archive-mode` becomes the cluster's archive list, each entry a plugin
  name plus its settings (`plugin-config.<name>`, as the log plugins). `restic-local`,
  `restic-sftp` and `restic-aws` migrate to `archive-restic-local`, `archive-restic-sftp`
  and `archive-restic-s3`, their `backup-restic-*` settings moved onto the entry; the legacy
  `BackupRestic*` bools go away, so mode and flags can no longer disagree.
- **Manifest**: `<bin>.manifest.json` as for the log plugins (`description`, `tier`,
  `config_keys`, prerequisites), plus `capabilities`. The archive settings page is drawn
  from it; each restic plugin's page comes from its own manifest.
- **Wire** (external): the log plugins' one-shot 5 s request does not fit. An archive
  plugin is a long-lived child process speaking JSON lines on stdin/stdout: each request
  carries an id; the plugin answers with progress events and one result; repman can send
  `cancel <id>`. Timeouts per operation (a push of hundreds of GB is not a 5 s call), and
  a dead plugin fails its running operation and raises a state, never blocks the monitor
  (F2).
- **Signature**: an archive plugin reads every backup, so it is loaded only with a valid
  Ed25519 `.sig` embedded in the package or image (`plugin-signing-public-key`), even when
  signature checking is off for log plugins.

## API and GUI

- New generic routes `/api/clusters/{c}/archive/...` (refs, stats, fetch, forget, check,
  unlock, queue); the `/restic/...` routes stay as aliases for the first restic archive until the
  GUI and the MCP tools have moved.
- The reseed modal offers "from the archive" for any plugin (`Fetch`), and "by mount" only
  with the `Mount` capability.
- T6: every generic route has its GUI, swagger and ACL; the settings page reads the
  manifest.

## Goal: move a cluster's backups to another repman through an archive

The end-to-end case the design must serve:

1. **Add the migration archive from its connection infos**: Signal18's restic S3 (or any
   S3 or sftp repository) is an archive added to the list with its settings and secrets,
   whether the repository already exists or not. Add, edit, rename and remove are
   generic: the plugin declares its typed settings, secrets and capabilities. #2091
2. **Archive the backup to it**: connect it with `backup-archive-name` and push. Works
   with the archive list (#2077), once each archive has its own secrets. #2088
3. **Connect another repman instance to this archive**, **read-only**, with the source's
   full repository path: `repo-append-cluster` would otherwise point a cluster with another
   name at an empty repository, and the target's retention would forget the source's
   snapshots. #2089
4. **Import the backup**: list the snapshots the source wrote and register one as a backup
   of the target cluster, then restore or reseed from it (extends the discovery and
   register of #2073). #2090

| Step | Issue | Needs |
|---|---|---|
| Per-archive secrets (`archive.<name>.password`, through the credential store chain) | #2088 | #2077, #2075 |
| Read-only connection, full repository path | #2089 | #2088 |
| List and import another cluster's backups | #2090 | #2089 |
| Add, edit, rename, remove an archive from its connection infos (probe: open existing or init) | #2091 | #2088 |

Both repman instances use the same restic password: the target adds the archive with the
same connection infos, and the probe opens the existing repository (a wrong password is
refused, an existing repository is never re-initialised). Secrets go through the secret
store, never in clear. A read-only connection refuses push, forget,
prune, init, unlock and key change with a state, never silently.

## Plan

1. the interface and the three internal restic plugins (`archive-restic-local`, `-sftp`, `-s3`) over today's restic code as a shared engine; the queue,
   retention decisions and remove-original moved into repman; the archive list (one
   entry, migrated from `backup-archive-mode`) and `ArchiveRefs` in metadata; no behaviour
   change, the restic regtests must pass unchanged;
2. restic syntax out of cluster code (S3 resolution, env, tags, filters into the plugin);
   the mode migration; BAU from `Stats` (and the remote check bug, its own issue);
3. N archives: per-archive policy, push to each, read preference, remove-original over all
   archives, copy between archives; protected refs in retention, and verification of the
   archived copy;
4. the external wire, loader, manifest capabilities, signature, states;
5. the generic routes and GUI;
6. a first external plugin, to prove the boundary.

## Open questions

- **Tiers**: are the three restic plugins free (restic is today), and which external plugins are pro or
  enterprise?
- **Backup streaming** (`backup-streaming*`): keep it as a filesystem mount outside the
  family, or retire it once an S3 archive plugin exists?
- **Binlogs and WAL**: push them as their own artifacts (one ref per segment range)
  instead of whole-directory pushes with no metadata, as `JobBackupBinlog` does today?
