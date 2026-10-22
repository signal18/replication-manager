# archiveplugin-plakar

replication-manager's plakar archive plugin (#2091). It embeds the plakar backup engine
([kloset](https://github.com/PlakarKorp/kloset)) and keeps a cluster's backups in an
encrypted, deduplicated plakar archive on:

- `archive-plakar-local`: a local folder;
- `archive-plakar-s3`: an S3 bucket (capability `s3`: replication-manager adds the
  `s3-provider` setting and hands the provider's endpoint, region and keys).

It is a separate Go module and a separate program, run once per operation as the
contract in `cluster/archiveplugin/wire` says: the request as one JSON document on stdin,
the response as one JSON document on stdout, progress lines on stderr, secrets in the
environment. `archiveplugin-plakar --manifest` prints the JSON array of its two manifests.

## Build

Go 1.25:

```sh
cd cluster/archiveplugin/plugins/plakar
go build -o archiveplugin-plakar .
```

## Settings

| setting       | plugin | type   | group    | meaning |
|---------------|--------|--------|----------|---------|
| `location`    | local  | string |          | the archive's folder |
| `bucket`      | s3     | string |          | the bucket (created if missing when the keys allow it) |
| `prefix`      | s3     | string |          | the folder in the bucket |
| `use-tls`     | s3     | bool   | advanced | https to the endpoint (default on, off for an `http://` endpoint) |
| `password`    | both   | secret |          | the archive's passphrase |
| `compression` | both   | bool   | advanced | compression of a new archive (default on) |
| `cache-dir`   | both   | string | advanced | the engine's local state cache (default `plakar-<hash>` under the user cache dir, else `$TMPDIR`) |

The cache only speeds things up: each run rebuilds the local state from the archive. A
run that finds the cache locked by another run on the same archive uses a temporary one.

## Environment

| variable | content |
|----------|---------|
| `REPMAN_ARCHIVEPLUGIN_SECRET_PASSWORD` | the archive's passphrase |
| `REPMAN_ARCHIVEPLUGIN_S3_ACCESS_KEY`, `..._S3_SECRET_KEY`, `..._S3_SESSION_TOKEN` | the S3 keys, the session token for short-lived STS keys; no keys = the instance's IAM role |
| `REPMAN_ARCHIVEPLUGIN_SECRET_TARGET_PASSWORD` | sync: the target archive's passphrase |
| `REPMAN_ARCHIVEPLUGIN_SECRET_TARGET_S3_ACCESS_KEY`, `..._TARGET_S3_SECRET_KEY`, `..._TARGET_S3_SESSION_TOKEN` | sync: an S3 target's keys, the source's keys when unset |

The secrets are never logged nor written in a file.

## Operations

| op | does | answers |
|----|------|---------|
| `probe` | opens the archive (a wrong passphrase fails on the canary); with `create`, creates it when missing, encrypted, as `plakar create` | `created`, `location` |
| `push` | backs up the folder `path` with `tags` and `host` (kept as a `host:<host>` tag) | `snapshot` (an error with it when files could not be read) |
| `list` | every snapshot, newest first | `snapshots` |
| `restore` | restores `snapshot` (id or unique prefix) into `target`, or only its path `include`; the backed-up folder's layout is kept under `target` | `snapshot` |
| `forget` | deletes `snapshot` | `removed` |
| `purge` | applies `policy` (`dryRun` only lists), then reclaims space | `removed` |
| `size` | the bytes the archive takes | `size` |
| `remove` | deletes the archive: only plakar's objects (`CONFIG` last), refused when there is no plakar `CONFIG` or the passphrase does not open it | `location` |
| `sync` | copies `snapshots` (all when empty) that `syncTo` lacks; with `create`, creates the target when missing | `snapshots` (the copied ones) |

### Purge

The reference time is the newest snapshot (as restic), so an archive whose backups stopped
keeps its last ones. `keepLast` keeps the N newest, `keepWithin` every snapshot of the
window (`7d`, `2y5m7d3h`: years, months, weeks, days, hours). The hourly, daily, weekly,
monthly and yearly counts are kloset `locate` periods keeping the newest snapshot of each of
the last N calendar buckets; a `keepWithin<Period>` window is the same period over the
buckets of that window. Unlike restic, a calendar bucket without a snapshot still counts.

Deleting a snapshot only marks it deleted. The space is reclaimed by plakar's maintenance,
ported here: a purge marks the packfiles nothing uses any more, and removes those marked
more than 7 days earlier (a backup running at the same time keeps the packfiles it uses).
So a purge frees space about a week later, on a later purge. The reclaim needs the
archive's exclusive lock: while a backup runs, it is skipped (logged on stderr) and the
next purge does it.

## Credits and license

The plakar engine, its fs integration and the code adapted here (`s3store.go` and
`s3retry.go` from PlakarKorp/integrations/s3, `maintenance.go` from PlakarKorp/plakar
subcommands/maintenance) are by PlakarKorp, under the ISC license: see
`LICENSE.plakar-integrations`. The rest of the plugin is part of replication-manager,
GPLv3.
