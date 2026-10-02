# Database run-as user and data owner (`prov-db-run-as-uid`, `prov-db-volume-uid`; OpenSVC and Kubernetes)

## Purpose

A provisioned database involves two numeric identities: the user the container
process runs as, and the owner of its data volume, which the bootstrap chown
prepares before MySQL starts. If the process cannot use what the owner holds,
MySQL cannot read and write its data directory.

Historically OpenSVC hard-coded the volume owner and the chown to `999`,
matching the MariaDB and MySQL images, and gave `--user mysql` to images named
`mysql`; Kubernetes set none of them. Other images use a different numeric
account: Percona Server's images run MySQL as `1001:1001`, so on Percona the
volume was owned by 999 while the container ran as 1001 and the server could
not start.

Two settings expose the two identities, **independently**: choosing the user the
container runs as does not change who owns the volume, and the other way round.
Both empty changes nothing for MariaDB and MySQL images.

## Configuration

Both are **advanced settings**: leave them empty unless the image or the storage
requires a specific identity. The flag descriptions and the dashboard confirmation
text say so ("Advanced setting").

```toml
[clustername]
prov-db-run-as-uid = "1001"       # container process (OpenSVC --user, Kubernetes securityContext)
prov-db-volume-uid  = "1001:1001"  # data volume owner (OpenSVC volume + bootstrap chown, Kubernetes init chown)
```

Both are strings, default empty, with the same value format:

| Value | Meaning |
|---|---|
| empty (default) | the legacy rendering, unchanged (see below) |
| `"1001"` | UID 1001, the GID defaults to the UID (`1001:1001`) |
| `"1001:999"` | UID 1001, GID 999 |
| `"0"` / `"0:0"` | root, taken literally |

Root is taken literally by the volume owner, the bootstrap chown, the Kubernetes init chown and `--user`/`runAsUser`. The one
exception is the jobs script `db_owner` (see Jobs container): it falls back to the legacy owner for the few files it
writes when the datadir is owned by root, because with the official MariaDB and MySQL images a root-owned datadir is one
that is about to become 999's.

Empty means, per setting:

- `prov-db-run-as-uid` empty: no identity is rendered. OpenSVC gives `--user mysql` to
  images whose name contains `mysql` and nothing otherwise; Kubernetes sets no
  securityContext. The container runs as the image's own user.
- `prov-db-volume-uid` empty: the volume and the bootstrap chown stay `999:999`, and Kubernetes
  has no ownership step. **Except Percona Server images** (name contains `percona`): `1001:1001`,
  see below.

They are strings so that "not set" cannot be confused with root (an integer field
would need `0` as its unset value, and `0` is root) and so that they can carry
`UID:GID`. Operators can still write a bare TOML integer for a UID alone
(`prov-db-volume-uid = 1001`); Viper decodes it into the string field (covered by
`TestProvDBIdentityDecodesTOML`).

`ParseDBIdentity` (cluster/prov.go) accepts empty, `UID` or `UID:GID`, each id a
decimal number from `0` to `2147483647`; signs, spaces, hex, a missing side
(`1001:`, `:1001`) and more than two parts are refused. Names such as `mysql`
are refused: they would resolve through the image's passwd, and Kubernetes only
takes numbers. replication-manager does not infer an image's user from its tag
or mutate the image.

## Upgrade note

With both settings empty, MariaDB and MySQL images are rendered exactly as before. **Percona Server
images are the exception**: with no setting, the data volume owner is now `1001:1001` instead of
`999:999`, and the dbjobs containers run as root instead of the image's own user. This takes effect when
the database service is reprovisioned (a running service keeps its old definition), and the bootstrap then
chowns the existing datadir to 1001. Before, such a service had a 999-owned volume and a process running as
the image's 1001, which cannot write to it on a new volume.

The Percona Server image is recognized by its name: any image whose name contains `percona`, case
insensitive, whatever the registry, path or tag, so a custom or private image such as
`registry.example/percona-custom:8.4` matches too. For such an image that really runs as another account,
set `prov-db-volume-uid` explicitly to that account (for example `999`): a set value always wins over the
name-based default.

## Two independent settings

`dbRunAs` and `dbVolumeOwner` (cluster/prov.go) resolve each setting once and every
consumer uses that result: the container process takes `dbRunAs`; the OpenSVC
volume owner, the bootstrap chown and the Kubernetes init chown take `dbVolumeOwner`.
An invalid value (the setters refuse one, but a config file may carry it) is
logged at error level, once and not at every render of the templates, and handled as empty. Only the last
invalid value of each setting is remembered per cluster (two entries at most; a new invalid value replaces it and a
valid or empty value forgets it), so the state cannot grow (`TestInvalidDBIdentityLoggedOnce`).

Because they are independent, **the operator owns the match between them**.
repman does not force the owner to follow the user, nor the other way round:

- running as root (`prov-db-run-as-uid = 0`) with the legacy owner works for the official
  MariaDB and MySQL images, whose entrypoint starts as root, chowns the datadir to
  `mysql` itself and drops to 999:999 (checked: the `rootkeep` case of the Docker script);
  no `prov-db-volume-uid = 0` is needed, and none is applied;
- running as a UID that has no access to a volume owned by another UID (for example
  `prov-db-run-as-uid = 1234` with the legacy 999 owner) is the operator's choice, and mysqld
  then reports `Permission denied`; set `prov-db-volume-uid` to the same numbers to avoid it.
  replication-manager logs a warning at provisioning time in that case (a non-root run-as UID that
  differs from the volume owner, or, on Kubernetes, a non-root run-as UID with no managed owner), and does
  not correct it (`TestDBRunAsVolumeMismatch`). The check compares the UID only: for a process that is the
  owner of the files, the owner permission bits decide, whatever their group is.

An explicit `--user` (or `--user=`, `-u`) in `prov-db-docker-run-args` on OpenSVC
wins over `prov-db-run-as-uid` (it is also how an operator gives a user *name*, which
repman cannot turn into a number): nothing is appended after it, and the volume
keeps following `prov-db-volume-uid`. This is pinned by
`TestOpenSVCDBContainerExplicitUserWinsOverConfiguredPair`. Without
`prov-db-run-as-uid` the legacy rule applies unchanged (`--user mysql` appended for
images named `mysql`).

The init container (alpine, root) and the dbjobs containers are not configurable
and run as root: the init container chowns and writes the bootstrap files, and
dbjobs needs root (see below).

### Why Percona Server images own the volume 1001 when `prov-db-volume-uid` is empty

Percona Server images create `mysql` as `1001:1001` (8.0, 8.0.35 and 8.4, and
images derived from them); the MariaDB and MySQL images use `999:999`. A Percona
Server runs as 999 (datadir, socket and PID file are all volume-backed), but the
image is built for 1001: its entrypoint runs
`/usr/bin/telemetry-agent-supervisor.sh` (`0774`, `1001:0`), which fails with
`Permission denied` under 999, and mysqld's `percona_telemetry` component
cannot write to `/usr/local/percona/telemetry/ps` (`1001:1002`, `2775`) and logs
`[Warning] [MY-011071] ... Problem during telemetry file write: Permission
denied` on every scrape (verified with a 10 s scrape interval: 13 warnings in
two minutes as 999, none as 1001). With the legacy owner the volume was 999
while the image ran as 1001, which does not start. So an empty `prov-db-volume-uid`
means `1001:1001` for Percona Server; its process keeps running as the image's
own 1001 (`prov-db-run-as-uid` empty). The image is recognized by name, like the former
`mysql` rule for `--user`: a Percona Server image whose name does not contain
`percona` needs `prov-db-volume-uid = 1001` explicitly. Running Percona Server as
another UID works only with the telemetry agent denied (info in the Docker script).

`0` for `prov-db-run-as-uid` is meant for images whose entrypoint starts as root and
drops privileges by itself (the official MariaDB and MySQL entrypoints chown the
datadir and switch to `mysql` through `gosu`). Images whose entrypoint does not
drop privileges cannot use it: Percona Server's mysqld aborts as root with
`MY-010123 Fatal error: Please read "Security" section of the manual to find
out how to run mysqld as root!` (checked on Docker and on Kubernetes).

### What the official entrypoints do as root before dropping privileges

Read from the `mariadb` (10.6, 11.8, 12.3), `mysql:8.4` and `percona/percona-server:8.4`
entrypoints. MariaDB and MySQL, when started as root: check the configuration,
read the `*_FILE` secrets, create and chown the datadir and the socket
directory to `mysql`, and, for MariaDB 11.8 and later, chown the cgroup
`memory.pressure` file; then they re-exec as `mysql` through `gosu`. Percona
Server's entrypoint has no root phase. With `prov-db-run-as-uid` set to a non-root
user the container starts directly as that user, so these steps are skipped:

- the datadir and the socket directory are prepared by the bootstrap chown (OpenSVC) or the
  init container (Kubernetes) from `prov-db-volume-uid` instead;
- `*_FILE` secrets must be readable by that UID (replication-manager itself passes
  `MYSQL_ROOT_PASSWORD` as a plain environment variable and uses no `*_FILE`);
- `memory.pressure` is chowned only when the entrypoint can write it, which it cannot in a
  default container (the cgroup is mounted read-only, checked on a Docker and an OpenSVC
  node with MariaDB 11.8: the entrypoint logs `memory.pressure not writable` as root, and
  the file stays `root:root` either way). On a host whose cgroup is writable
  (privileged container), `--user 0:0` in `prov-db-docker-run-args` restores the root phase;
- the other mounts rendered by replication-manager are unaffected: `/etc/mysql` and
  `/docker-entrypoint-initdb.d` are world-readable and `/var/lib/mysql-files` was already
  root-owned and not writable by the `mysql` process.

## OpenSVC

- `prov-db-run-as-uid` set: `--user UID:GID` appended to the database container's `run_args`
  (unless the operator already set one, see above). Empty: `--user mysql` for images
  named `mysql`, nothing otherwise, as before;
- data volume: `user`/`group` = `prov-db-volume-uid`, legacy `999:999` when empty (Percona
  Server images: `1001:1001`);
- bootstrap container (database services only), when the owner is managed (`prov-db-volume-uid`
  set, or a Percona Server image): `REPLICATION_MANAGER_DB_VOLUME_UID/GID` in its
  environment; `share/dashboard/static/configurator/opensvc/bootstrap` chowns
  `/bootstrap/data` with them (falling back to `999` if they are missing, which is also the
  proxies' case).

With both settings empty on a MariaDB or MySQL image the rendered service is the
one from before these settings existed (`TestOpenSVCDatabaseIdentity` pins both forms).

The init container is shared with the proxy services. The owner is added only
to the database one (`OpenSVCGetDBInitContainerSection`); the proxies' init
container carries no `REPLICATION_MANAGER_DB_VOLUME_*`, so the bootstrap keeps
chowning their data to its legacy `999` default. That is the account of the
ProxySQL image (`999:999`) and of the MariaDB-based ShardProxy; giving them the
database owner would lock them out of their own data on a Percona cluster or
after a custom owner.

A running service keeps its old definition until it is reprovisioned.

The bootstrap chown is recursive over `/bootstrap/data`, as it was before this change
(`chown -R 999:999`), whereas the Kubernetes init command only touches entries that differ. On a very
large datadir the OpenSVC bootstrap can therefore take long; aligning it with the Kubernetes approach is
possible but was left out to keep this change minimal.

## Kubernetes

`k8sDBIdentity` (cluster/prov_k8s_db.go):

- `prov-db-run-as-uid` set: the database container gets `securityContext.runAsUser`/`runAsGroup`
  and mounts an `emptyDir` at `/run/mysqld` (PID file, socket). Some images ship that
  directory as `0775` owned by their own `mysql` account (Percona Server: `1001`); under
  any other UID mysqld then aborts with `Can't start server: can't create PID file:
  Permission denied`. A plain emptyDir is created world-writable (mode `0777`), so the pod's
  UID needs no `fsGroup` and no init step to use it; this was checked on a Kubernetes
  cluster with pods running as `1001:1001`, `1234:1234`, `999:1001` and `0:0`, each creating
  a PID file and a socket in one. Like the image directory it replaces, it lives as long as
  the pod. OpenSVC gets the same from its `{name}/run/mysqld` volume mount. MariaDB and
  MySQL images ship `/run/mysqld` as `1777`. Empty: no securityContext and no emptyDir;
- `prov-db-volume-uid` set (or a Percona Server image): the init container (alpine, root) ends with
  `find /var/lib/mysql \( ! -user UID -o ! -group GID \) -exec chown -h UID:GID {} +`:
  only entries that differ are changed (a matching volume is not rewritten on
  every pod restart) and symlinks are not followed. If it fails (read-only
  volume, squashed NFS owner, ...), `WARNING: could not set the owner of
  /var/lib/mysql to UID:GID` is written to the init container log
  (`kubectl logs <pod> -c <name>-init`) and the pod still starts, so a volume
  the process can use anyway is not blocked; if it cannot, mysqld reports
  `Permission denied`. It runs after the `.system` directories are created and the
  configuration is applied, and does not decide the init container's exit code, which stays
  `MKDIR_STATUS`. Empty on other images: no ownership step, as before;
- no pod `fsGroup`: the init chown already gives the volume its owner, and
  `fsGroup` would also make the kubelet change the volume's group on mounts;
- with `prov-db-run-as-uid = 0`, `runAsUser: 0` is explicit; a namespace enforcing the
  `restricted` Pod Security Standard refuses such a pod (the init container and the dbjobs
  sidecar run as root regardless, as before for the init container). With `prov-db-volume-uid = 0`
  as well, on every pod start the init container chowns the datadir to `0:0` and the image
  entrypoint chowns it back to `mysql` before dropping privileges; the MySQL entrypoint
  changes the owner only, so files end up `mysql:root`, which mysqld can use. On a large
  datadir this double chown lengthens each start. Leave `prov-db-volume-uid` empty to avoid it.

With both settings empty on a MariaDB or MySQL image the pod has no securityContext, no
ownership step and no `/run/mysqld` emptyDir, as before (`TestK8SDatabaseDeployment_Identity`).

The local (`localhost`) provisioner is not affected: it starts `mysqld` on the
host as the replication-manager process user. On-premise provisioning targets
existing hosts and is not affected either.

## Jobs container

When any identity is managed (`prov-db-run-as-uid` or `prov-db-volume-uid` set, or a Percona
Server image), the OpenSVC `jobs` container and the Kubernetes dbjobs sidecar
(`dbjobs_new`) run explicitly as root: `--user 0:0` first in the jobs `run_args` (so a
`--user` in `prov-db-jobs-docker-run-args` still wins) and `runAsUser`/`runAsGroup: 0` on
the sidecar. They ran as the image's default user before, which is root for the MariaDB
and MySQL images but `mysql` (1001) for Percona Server's. With any other UID that sidecar
could not read the datadir (`0750` directories): the server ran, but backups and other
jobs failed with `Permission denied`. dbjobs needs root for more than reading: running it
as 1001 on Percona fails (its self-upgrade writes to the init directory, owned by the
replication-manager UID from the archive, and the configuration print creates
`/bootstrap/dummy`). There is no setting for the jobs user: a non-root value breaks the
jobs, and on OpenSVC a `--user` in `prov-db-jobs-docker-run-args` already overrides it; Kubernetes has no
equivalent override.

Trust boundary: the jobs container runs the scripts it fetches from the replication-manager server as
root, inside a container that is not privileged and only sees the volumes of its own service (datadir,
configuration, init, run directory and the credentials secret). For MariaDB and MySQL images this is not a
widening, since their default user is root; for Percona Server images (default user 1001) it is. The scripts
already run with the database credentials, so the trust placed in the replication-manager server as their
source is unchanged; root only adds the ability to read and chown the whole datadir, which the jobs need.
With both settings empty on a MariaDB or MySQL image nothing is added (the image's own
user is root).

The jobs containers run from the database image and write into the data volume
(configuration extraction, partial restore). They no longer use `999:999` or
the image's `mysql` account for those files: `db_owner` reads the numeric owner
of the datadir, which the orchestrator set from `prov-db-volume-uid`, and falls back
to the previous owner when the datadir is missing or owned by root. That keeps
the files readable by the database process even when the configured UID has no
passwd entry in the image.

Root (`0`) is the one case where the owner of the datadir does not tell what the
database process will be. The MariaDB and MySQL entrypoints start as root and
drop to `mysql` after chowning the datadir, so it ends up owned by 999 (verified,
see the matrix), and an early look at a root-owned datadir is a datadir that is
about to become 999's: `db_owner` then uses the previous owner (`999:999`, or
`mysql:mysql`), which is what the database will own. Keeping files root-owned
instead would leave `mysqld` (999) unable to read what a partial restore
creates. The residual case is a custom image whose mysqld really runs as root:
its files get the previous owner, which root can still read. Percona Server
refuses to run as root, so it never gets there.

The `mysql_uid`/`mysql_gid` variables of the legacy OpenSVC collector moduleset
(`share/opensvc/moduleset_mariadb.svc.mrm.*.json`) stay at `999`; they belong to
the collector compliance path, not to the v3 bootstrap path described here.

## Dynamic configuration and dashboard

The React Configurator shows **Database Run As** and **Database Data Owner** in
**Configs → Orchestrator Disks** when the orchestrator is OpenSVC or Kubernetes, as
text inputs that accept `UID` or `UID:GID` (empty = default). Saving an
empty value goes through the existing clear-setting endpoint, which calls the
same dispatcher with `""`. Both use the existing `cluster-settings` ACL; there
is no new endpoint or ACL exception.

The settings exist for the OpenSVC and Kubernetes provisioners only. For any other
orchestrator (local, on-premise, SlapOS) the dispatcher refuses them, the way the
restart action refuses an unsupported orchestrator, instead of accepting a value that
would do nothing and raising a reprovision cookie for it (this also covers API and CLI
callers; the dashboard only shows the inputs for the two supported ones).

A change only marks database services for reprovisioning. It does not alter a
running container or chown an existing volume; the later unprovision/provision
remains explicit operator consent. To apply a new value, unprovision the
affected database service after confirming its data can be discarded or has
been backed up, then provision it again. This prevents a mixed-owner data
directory.

The OpenSVC secret tmpfs uses a separate ownership setting and is not changed
by these options.

## Repeatable tests

- Go unit tests (`cluster/prov_opensvc_db_volume_test.go`, `cluster/prov_k8s_test.go`,
  `server/api_cluster_test.go`, `config/config_db_runtime_id_test.go`): what is rendered
  for OpenSVC and Kubernetes with each setting alone and together, the legacy rendering
  when both are empty, the Percona Server owner, root, the value parser, the settings API
  (and its refusal for the other orchestrators), TOML integers decoded into the string
  fields, proxies keeping the legacy owner, an operator `--user` kept (also against a
  configured owner), the jobs containers as root only when managed.
- `cluster/prov_k8s_init_docker_test.go`, `TestK8SDatabaseInitOwnershipCommandInAlpine`:
  runs the ownership command the Kubernetes init container ends with, as generated for the
  owner, in the init container's own image (Docker needed; skipped otherwise and with
  `-short`): ids with no passwd entry, a mixed UID/GID, root, symlinks chowned and their
  targets left alone, a read-only path reported without failing the init container, and an
  already correct volume left untouched (ctime unchanged).
- `share/scripts/tests/db_runtime_uid_gid/docker_check.sh` (real Docker, T13): the images
  really run and restart under what is rendered. For every LTS line of
  `cluster/logplugin/plugins/plugin-score-lts/lts-versions.json` (MariaDB, MySQL, Percona
  Server) and each identity (`default` = both settings empty, `custom` 1234, `mixed`,
  `root` = both 0, `rootkeep` = run-as 0 with the owner left empty) it creates data and run
  volumes owned by the owner, starts the server with the run-as user, checks the mysqld
  process identity, that every datadir file belongs to exactly the expected UID:GID pair
  (only the UID with root: the MySQL entrypoint leaves the group of part of the tree at 0)
  and that the PID file in `/run/mysqld` belongs to the process, writes a row, stops and
  starts the same container again and checks all of it again. Expected results: the
  settings are honored; with root, MariaDB and MySQL drop to 999:999 themselves (also when
  the owner is left legacy) and Percona Server refuses (`MY-010123`); Percona Server under a
  UID other than 1001 runs, with the entrypoint's telemetry agent denied (reported as info).
  It needs `docker` and `python3` only, is sequential (about a minute per image and
  identity), and `CONTROL_VOLUME_OWNER=999:999 IDS=default ... percona/percona-server:8.4`
  is the control: a 1001 process on a 999-owned datadir, the state before Percona Server's
  own 1001 became the default owner, must fail (`data directory exists and is not writable`).
  Two details of the script come from what it caught while it was written, and matter if it
  is changed: the volumes need a hidden marker file, because Docker copies the image
  directory's owner into a volume that is empty when it is mounted, which silently undoes
  the chown (the control then passes when it must fail); and readiness has to wait for
  `@@skip_networking = 0`, because the entrypoint's temporary initialization server answers
  `select 1` first and is shut down seconds later with whatever was sent to it. The
  MariaDB 11.x entrypoint also aborts now and then while it initializes (`TLS/SSL error:
  certificate is not yet valid`, on its own self-signed certificate; 5 of 36 cases over three
  runs, any identity, in bursts, also with the legacy `999:999` layout): the script retries
  such a start once and prints a note, any other failure is final. The same race also hit the
  test client on a query after a restart, so the script's MariaDB client runs with
  `--skip-ssl` (the queries probe the identity, not TLS). Plain images started repeatedly
  without the script (60 runs of MariaDB 11.4 / 11.8) did not show it, so its trigger is
  not established.

The Docker script covers the images; the Kubernetes pod spec (securityContext, init
container ownership command, `/run/mysqld` emptyDir) and the OpenSVC services are checked
by provisioning through replication-manager (see below).

## Lab validation

Three things are validated here and must not be mixed: the feature (the rendered identity is the
one the database runs and owns its data with), the behavior of the official images (what their
entrypoints do with it), and a timing flake of the MariaDB 11.x images that is unrelated to the
identity (see the Docker script above).

### OpenSVC, final settings

Through replication-manager provisioning on an OpenSVC test cluster, one case at a time, for every
LTS line of `cluster/logplugin/plugins/plugin-score-lts/lts-versions.json` (official images:
MariaDB 10.6, 10.11, 11.4, 11.8, 12.3; MySQL 8.0, 8.4; Percona Server 8.0, 8.4) and six identities:
**9 images x 6 identities = 54 cases**. One database is provisioned per case through the per-server
provision API; a second server entry of the test cluster stays unprovisioned. Each case checks the
rendered `--user`, volume `user`/`group` and bootstrap owner, the mysqld process identity, the owner of
every datadir file, that the jobs container runs as `0:0` and reads the whole datadir, that repman
reaches the server (`StandAlone`), and that nothing is left after unprovision.

| Identity | `prov-db-run-as-uid` | `prov-db-volume-uid` |
|---|---|---|
| default | empty | empty |
| both1001 | 1001 | 1001 |
| root | 0 | 0 |
| both1234 (no passwd entry) | 1234 | 1234 |
| mixed | 999:1001 | 999:1001 |
| rootkeep | 0 | empty |

Result: **52 PASS, 2 FAIL as literally reported by the evaluator.** The two FAIL are `mysql:8.0` and
`mysql:8.4` with the `root` identity, and they are an evaluator mismatch, not a feature defect: the
server started, mysqld ran as `999:999` (the entrypoint dropped root), the jobs container read the
whole datadir, the log was clean and nothing was left behind. The datadir owners (raw log) were
`999:999(188) 999:0(10) 0:0(8)` on 8.0 and `999:999(194) 999:0(13) 0:0(5)` on 8.4. The `999:0` entries
are the known behavior of the MySQL entrypoint, which changes the owner of the datadir and leaves
the group as it was (here 0): the files are `mysql:root`, usable by mysqld. The totals equal those of
the default case (206 and 212 entries), and the remaining `0:0` entries are the root-owned `.system/jobs`
directory used by the jobs container. The evaluator rejected `999:0`, which the Docker script already
accepts for root. The two results are kept as reported; they are not turned into PASS.

What the 54 cases show, by behavior:

- default: MariaDB and MySQL render as before this feature (volume `999:999`, no `--user` for MariaDB,
  `--user mysql` for MySQL) and Percona Server renders `1001:1001`; all 9 pass.
- 1001, 1234, 999:1001: the process, the volume and the bootstrap owner are exactly the values set,
  for all 9 images (27 cases).
- root and rootkeep: the MariaDB and MySQL entrypoints drop root to `999:999`, also when the volume
  owner is left at its legacy value (`rootkeep`). Percona Server refuses to run as root
  (`MY-010123`), which is the expected result for its 4 root cases; for one of them
  (`percona-server:8.0`, `root`) the rendered configuration was not captured because the wait was cut
  short, so that verdict rests on the refusal in the database log and the clean cleanup.
- Percona Server images under a UID other than 1001 start; their entrypoint logs
  `telemetry-agent-supervisor.sh: Permission denied` and the server is healthy.

The settle step of the test harness matters when the test is repeated: unprovision is asynchronous
on the node (logical volume, DRBD resource, volume object), and a provision started before it ends
loses its volume (`volume u1 does not exist`). That was a harness race, not the identity, and not
a permission error (no container had been created).

### Kubernetes, final settings (sampled)

A sample of **15 cases** (not the full 54) was run on a local kind cluster through replication-manager
provisioning, one database per case, with the final settings. The rendering of the securityContext, of the
init container ownership command and of the `/run/mysqld` emptyDir does not depend on the image (it is
covered by `TestK8SDatabaseDeployment_Identity` and `TestK8SDatabaseInitOwnershipCommandInAlpine`); what
depends on the image is the entrypoint, so the sample covers each distinct entrypoint behavior:

| Image | Identities run | Cases |
|---|---|---|
| `mariadb:11.8` | default, 1001, 1234, mixed (999:1001), root, rootkeep | 6 |
| `mysql:8.4` | default, 1234, root, rootkeep | 4 |
| `percona/percona-server:8.4` | default, 1234, root | 3 |
| `mariadb:10.6` (oldest MariaDB line) | 1234 | 1 |
| `mysql:8.0` (oldest MySQL line) | root | 1 |

Not run: the other identities on `10.11`, `11.4` and `12.3`, on `percona-server:8.0`, and on `mysql:8.0`
except root. Each case checks the database container's securityContext (none when both settings are
empty), the dbjobs sidecar as `0:0` whenever an identity is managed, the init container ownership command,
the mysqld process UID:GID, that every datadir file belongs to the expected pair, the owner of the PID file
in `/run/mysqld`, a successful query, repman reaching the server (`StandAlone`: the test cluster has two
server entries and only the first is provisioned, through the per-server provision API), that the row and the
process identity survive a replaced pod, and that no pod or volume claim is left after unprovision.

Results as reported, in the order they were run:

- `mariadb:11.8` default, 1001 and 1234: 3 PASS (first round, cluster-level provisioning).
- A second round of 12 cases with per-server provisioning: **8 PASS and 4 FAIL as literally reported.**
  The four were failures of the test harness or of its judge, not of the feature, and were rerun with a
  corrected harness: `mariadb:11.8` root (the post-restart check addressed the replaced pod), `mysql:8.4`
  default (the judge accepted the `999:0` files only for an explicit root; the MySQL entrypoint also leaves
  group 0 on entries when the container starts as root without any setting, as before this feature),
  `percona-server:8.4` root (the wait saw the pod `2/2` for a moment before the container exited; the
  refusal `MY-010123` was in the log) and `mysql:8.0` root (the first query ran before the database had
  finished initializing, and the later checks followed from it). The rerun gave **4 of 4 PASS**; the
  first-pass results are kept above and are not replaced by it.
- Percona Server with root is refused by mysqld (`MY-010123`, the container restarts) and MariaDB/MySQL drop
  root to `999:999`, as in the OpenSVC matrix. MySQL leaves `999:0` entries when it starts as root.

Provisioning one server through `GET /api/clusters/{cluster}/servers/{id}/actions/provision` also avoids a
wait seen on the cluster-level unprovision ("Waiting for cluster shutdown", which ended in
`Cluster shutdown timeout` after 120 s in about 4 of 10 cycles on this single-server test cluster). That wait
is not related to the identity and was not investigated.

### Kubernetes, earlier matrix (former pair of settings)

The Kubernetes matrix below was run when the identity was still one pair of
settings (a UID setting and a GID setting, each empty meaning the image's account). The rendered
securityContext, init chown and `/run/mysqld` emptyDir for an explicit value are unchanged by the split into
the two final settings.

Kubernetes was validated through replication-manager provisioning on a local kind
cluster, one database per test cluster, for the same nine images with the same five
identities. In all 45 cases the rendered securityContext, the mysqld process and the
datadir owner matched, the dbjobs sidecar ran as `0:0` and could read the whole datadir,
and a query succeeded, except Percona with `0:0`, which mysqld refuses (expected). With
`0:0`, MariaDB and MySQL entrypoints drop to `999:999`.

Further Kubernetes cases: a pod restart keeps data and identity (including root
mode); changing the identity on an existing volume and reprovisioning re-owns the
datadir and keeps the data (999:1001 → 1234:1234 on MariaDB, 1001 → 999 on Percona);
invalid values (`mysql`, `-1`, `2147483648`, `abc`) are refused by the API and leave the
setting unchanged. Percona needs `prov-db-memory` of at least 1G on Kubernetes (512M is
OOM-killed during initialization, independent of the identity).

A Docker matrix emulating the OpenSVC rendering (volume chowned to the pair,
`--user UID:GID`, `{name}/run/mysqld` mounted) passed for mariadb 10.11/11.8,
mysql 8.0.35/8.4 and percona-server 8.0.35/8.4 with 999:999, 1001:1001,
1234:1234 and 999:1001, including a second boot on the same datadir; `0:0`
passed for MariaDB/MySQL and failed for Percona as above.
