# archiveplugin-pbs

replication-manager's Proxmox Backup Server archive plugin (`archive-pbs`): a cluster's
backups kept in a namespace of a PBS datastore. It drives the official
`proxmox-backup-client`, a **prerequisite of the node** (the Debian package
`proxmox-backup-client`, Linux only): PBS speaks its own protocol (chunks, indexes) and
has no Go library, so the engine is not embedded, unlike `archiveplugin-plakar`.

It is a separate Go module and a separate program, run once per operation as the
contract in `cluster/archiveplugin/wire` says: the request as one JSON document on stdin,
the response as one JSON document on stdout, the client's output and progress on stderr,
secrets in the environment. `archiveplugin-pbs --manifest` prints its manifest.

## Build

```sh
cd cluster/archiveplugin/plugins/pbs
go build -o archiveplugin-pbs .
```

`make` builds it with the other archive plugins (every `cluster/archiveplugin/plugins/<name>`
with a `go.mod`).

## Settings

| setting       | type   | group    | meaning |
|---------------|--------|----------|---------|
| `server`      | string |          | the PBS host |
| `datastore`   | string |          | the datastore, created on the PBS by its admin |
| `namespace`   | string |          | the cluster's namespace (`a/b/c`), created by a probe with create; `remove` needs one |
| `auth-id`     | string |          | `user@realm` or an API token `user@realm!token` (default `root@pam`) |
| `fingerprint` | string |          | the server certificate's SHA-256 fingerprint (a self-signed PBS) |
| `password`    | secret |          | the auth id's password or the API token's secret |
| `encryption-key` | secret |       | the client encryption key, its JSON (`proxmox-backup-client key create`); none: not encrypted |
| `encryption-key-password` | secret | | the key's passphrase, when it has one |
| `port`        | int    | advanced | the API port (default 8007) |
| `client-path` | string | advanced | `proxmox-backup-client` (default: from `PATH`) |
| `change-mode` | enum   | advanced | `legacy`, `data`, `metadata`: the client's change detection |

## Environment and secrets

The plugin reads `REPMAN_ARCHIVEPLUGIN_SECRET_PASSWORD`, `..._ENCRYPTION_KEY` and
`..._ENCRYPTION_KEY_PASSWORD`, and gives the client `PBS_REPOSITORY`
(`<auth-id>@<server>[:port]:<datastore>`), `PBS_PASSWORD`, `PBS_FINGERPRINT` and
`PBS_ENCRYPTION_PASSWORD`. The encryption key goes through a pipe (`--keyfd 3`): no secret
on a command line, none in a file. replication-manager's own variables are not passed on.

## Operations

| op | does | answers |
|----|------|---------|
| `probe` | lists the namespace's snapshots (server, credentials, namespace checked); with `create`, creates the missing namespace levels | `created`, `location` (`pbs://<repository>/<namespace>`) |
| `push` | `backup backup.pxar:<path>` as a `host` snapshot of the group `host/<backup-id>` (the request's host, characters PBS refuses as `_`), then writes the notes | `snapshot` (`host/<id>/<time>`) |
| `list` | the namespace's snapshots, newest first | `snapshots` |
| `restore` | `restore <snapshot> backup.pxar <target>`: the pushed folder's content under `target`, or only `include` (`--pattern`), kept at its place | `snapshot` |
| `forget` | `snapshot forget` | `removed` |
| `purge` | `prune` of each group with the policy's counts (`--dry-run` with `dryRun`) | `removed` |
| `size` | the sum of the namespace's snapshot sizes (logical) | `size` |
| `remove` | `namespace delete --delete-groups`; refused without a namespace | `location` |
| `sync` | not supported: a copy between PBS datastores is a sync job of the PBS | error |

### Tags

PBS has no tags. The snapshot's host, pushed path and tags are written on the first line of
its notes (`repman host=… path=… tags=…`, URL-escaped), the line `snapshot list` returns as
its comment. A snapshot without that line lists with no tags.

### Purge

PBS prunes by counts: `keep-last`, `-hourly`, `-daily`, `-weekly`, `-monthly`, `-yearly`, as
the cluster's `backup-keep-*`. A `backup-keep-within*` window has no PBS equivalent: the
purge is refused with an error, never run with less than the policy. The space of pruned
snapshots comes back with the datastore's garbage collection, a job of the PBS, not of
the plugin.

## To check on a real PBS (the tests use a stand-in client)

- the JSON fields of `snapshot list`, `prune` and `namespace list` (`backup-type`,
  `backup-id`, `backup-time`, `comment`, `size`, `keep`, `ns`): not in the client's
  documentation;
- bare boolean flags (`--allow-existing-dirs`, `--delete-groups`, `--dry-run`);
- a restore with `--pattern <path>` and `<path>/**`: one file, one folder;
- the API token's privileges: `Datastore.Backup` for push and list, `Datastore.Prune` or
  `Datastore.Modify` for forget and purge, `Datastore.Modify` on the parent for namespaces;
- deduplication: a gzip dump deduplicates poorly from one backup to the next.
