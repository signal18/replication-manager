# xtrabackup injection for stock MySQL and Percona Server images (`prov-db-docker-xtrabackup-img`)

## Purpose

dbjobs runs `xtrabackup` and `socat` (and reads the stream with `xbstream`) in the database **jobs container**, which
always runs the database image (OpenSVC `{env.docker_image}`, Kubernetes `ProvDbImg`). The official `mysql:*` and
`percona/percona-server:*` images ship none of the three (`mariadb:*` ships `mariabackup`, `mariadb-backup`, `socat` and
`mbstream`), so physical backup, physical reseed, flashback and partial restore only worked with custom images that
bundle xtrabackup. `percona/percona-xtrabackup` cannot be the jobs image either: it has the tools but no `mysqld`, which
partial restore needs (temporary read-only server in the jobs container).

With this setting the database and jobs images stay the official ones, and an init container of the official xtrabackup
image fills a volume with the three tools ("the bundle"); the jobs container mounts it read-only. The word *bundle*
only names that directory; it is not an OpenSVC or Kubernetes concept.

## Setting

```toml
[clustername]
prov-db-docker-xtrabackup-img = "percona/percona-xtrabackup:8.4"
```

The injection is for the **official** database images only (`mysql`, `library/mysql`, `percona/percona-server`; see
"The tools on the PATH of the container"). With any other database image, whatever the value, nothing is rendered and the
setting is logged once as ignored.

An advanced setting (Go `ProvDbDockerXtrabackupImg`, JSON `provDbDockerXtrabackupImg`), OpenSVC and Kubernetes only.
It is written in the TOML configuration, or set from the GUI (Configs, Orchestrator images, "Xtrabackup") or the
settings API (`/api/clusters/{cluster}/settings/actions/set/prov-db-docker-xtrabackup-img/{value}`; `actions/clear/...`
turns it off). The settings API (`SetProvDbDockerXtrabackupImg`) validates the value and refuses a bad one, or an
orchestrator that does not render the injection, with an error; a TOML file or a flag is not checked when it is read, so
the render checks it again. The GUI offers Off, Auto and the tags of the `xtrabackup` repository of the image catalog.
A change takes effect at the next provisioning of the database service (the reprovision cookie is raised); it never
changes a running container.

| Value | Meaning |
|---|---|
| empty (default) | off. Nothing is rendered: the services are exactly what they were. The database image must ship the tools |
| `auto` | derived from the database image when the service is rendered (below) |
| an image reference | that image, as written. The operator picks the tag; repman never rewrites it |

The value is written into an OpenSVC configuration and a Kubernetes image field, and into a shell command, so only the
characters of a docker image reference are accepted (`ValidateXtrabackupImage`); spaces, quotes and shell or INI
metacharacters are refused. The check is made by the render (`xtrabackupBundleImage`), whatever way the value arrived
(a TOML file or a flag): an invalid one is logged once and leaves the injection off, exactly like an empty setting. This is a character-safety check, not a parser of docker references: a reference that is malformed in another way (`a@`),
that names an image that does not exist, or that cannot be pulled passes, and fails at pull time (see "A helper image that
cannot be pulled").

### `auto`

`xtrabackupAutoImage` answers only for what it can read without guessing: the repository must be an official one
(`mysql`, `percona/percona-server`) and the tag must start with a series (`8.0`, `8.4`). The xtrabackup image is
`percona/percona-xtrabackup:<series>`: the tag is the series itself, except `5.7`, which xtrabackup `2.4` backs up. A
MariaDB image needs nothing (the injection stays off). A digest, `latest`, a private name, or a series without a
published xtrabackup tag leave the injection off, logged once; the operator then sets the image explicitly. The
xtrabackup version must match the server series, not the exact release: `xtrabackup 8.4.0-7` backed up a `8.4.11`
server in the Docker check.

**The list of xtrabackup tags is the image catalog's.** `share/repo/repos.json` (built by `scripts/updaterepo.sh`, the
catalog the configurator and the GUI use for the images of MariaDB, MySQL, ProxySQL, HAProxy...) has an `xtrabackup`
repository (`percona/percona-xtrabackup`). A series is answered for when its tag is in that list, so a new series is
supported as soon as the catalog lists its xtrabackup tag, with no change of the code. The render reads the catalog the
way the configurator does (`GetDockerRepos`): the back-office file when one was pushed, the embedded one otherwise, and
keeps what it read for 5 minutes. A pushed catalog that does not list the `xtrabackup` repository yet falls back to the
built-in series `5.7`, `8.0` and `8.4`. `TestEmbeddedCatalogListsXtrabackup` keeps the embedded catalog and the built-in
series in step. The LTS list (`plugins/data/lts-versions.json`) is read by the security score plugin only and is not
used here: the series come from the database image tag.

## Bundle layout

Built by `share/scripts/xtrabackup_bundle.sh` (embedded in the binary, carried to the init container base64-encoded so
it needs no network, no file in the image and no quoting) running as root in the xtrabackup image:

```
current/bin/<tool>      wrappers: the only directory put on the PATH of the jobs container
current/libexec/<tool>  the real xtrabackup, xbstream, socat
current/lib/            their libraries and the dynamic loader (about 75 MB in all)
current/manifest        image=<reference>, xtrabackup version, loader, size, build date
status                  "ok <image>" or "failed <reason>"
```

A wrapper runs the tool **through the bundled loader**: `exec lib/ld-linux... --library-path lib libexec/<tool> "$@"`.
The bundle therefore does not depend on the glibc of the image that runs the jobs (the Docker check ran the same bundle on
Ubuntu 20.04, Debian 12, `mysql:8.4` and Alpine), and `LD_LIBRARY_PATH` is never set, so the image's own `mysql` client
and `mysqld` are not disturbed. x86_64 was tested; another architecture needs the helper image of that architecture
(the loader name is read from the library list, not assumed).

### Lifecycle and bounds

- The build is **idempotent**: if `current/manifest` names the same image (a literal comparison: the dots of a reference are
  not wildcards), nothing is copied. A swap interrupted between `current -> .old` and `stage -> current` is recovered at the
  next start (`.old` is put back before anything is removed).
- **A failed build leaves no bundle.** The bundle directory is on the PATH of the jobs container, so after an image
  change whose build failed the previous bundle would still be run, possibly for another series. The failing script
  removes `current` (and `.old`) and writes `status` as `failed ...`: the tools are then missing, and reported as missing.
- It builds in `.stage`, checks that every tool runs from there (`--version`, `-V`), then swaps it into `current` (the
  previous one is removed). A failed or interrupted build leaves no `current` half written; stale `.stage` and `.old`
  directories are removed at the next start.
- The size is bounded **while copying**, not after: before anything is copied the free space of the destination must be at
  least the bound (256 MiB) plus a 128 MiB floor that the build leaves free (on OpenSVC the destination is the
  database's own data volume, so the build never takes the volume down to zero free), then the running total (tools and libraries, measured before each file is copied) must stay
  within the bound minus a 1 MiB reserve for what is written afterwards (the three wrappers, the manifest and their
  filesystem blocks). Every write of the build is checked (a failed write stops it before anything is installed; the
  final `status` write only warns, the manifest being what marks a bundle as installed), the manifest must be there and
  non-empty, and the size of the whole stage, manifest included, is checked once more before
  it replaces `current`. On OpenSVC the destination is the database's own data volume, so a build never starts on a
  volume that cannot hold it with the bound to spare, and an installed bundle never exceeds the bound. A real bundle is
  about 70 MB (tools and the libraries `ldd` lists for them, not the helper image).
- It **never fails the init container**: any error *of the script* is logged and written to `status` and the script exits
  0, so the database starts without the tools and monitoring is not affected. The job that needs the tools reports why.

### A helper image that cannot be pulled

The script cannot report a failure that happens before it runs, and on Kubernetes an init container cannot be made
optional: a helper image that cannot be pulled leaves the pod in `Init:ImagePullBackOff` and the database never starts.
So the render asks the registry first (`xtrabackupHelperPullable`, `cluster/prov_xtrabackup_helper_check.go`, a manifest
`HEAD` request bounded to 10 s, made by the provisioning renders and never by the monitoring loop) and renders nothing
unless the registry hands the image out: no init container, volume, mount or PATH, on OpenSVC and on Kubernetes alike.
The database starts without the injection and repman logs an error naming the image and the registry's answer.

| Registry's answer | Rendered? |
|---|---|
| hands the image out | yes |
| 401, 403 or 404 (a typo, a tag or repository that is gone, one that needs credentials; Docker Hub reports a missing repository as 401) or a reference that cannot be parsed | no |
| cannot be reached (timeout, DNS, connection refused, a 5xx) | only if **this cluster** confirmed the image earlier and the registry has not refused it since: a cluster that runs with the injection keeps it through an outage of the registry. Otherwise no, so that a helper that cannot be pulled never keeps the database from starting |

Every answer is kept for 5 minutes, so a render (which asks several times) costs one request, and a tag that disappears,
a registry that revokes access or one that comes back is noticed at the next provisioning after that. Requests for one
image are shared (one in flight) and no lock is held while the network is used, so a registry that stalls for one image
holds nothing for the others. The memory of the check is bounded: one entry per cluster and image, dropped when its answer and its confirmation have both expired, and never more than 256 (the least recently used go first). The requests in flight are bounded too: at most 8 reach the registry at once, through 8 slots. A cluster that finds them all taken waits for its turn, no longer than a request takes (10 s), and no more than 32 wait, counting both those waiting for a slot and those waiting for the answer of a request already out for the same cluster and image: a caller that finds the queue full is not queued, and none waits longer than 10 s. When no turn comes, in time or at all, the registry could not be asked for it, and it is answered as when the registry cannot be reached (rendered only if that cluster confirmed the image earlier), without keeping the answer, so it asks again at its next render. A confirmation lasts 24 hours after the last time the registry handed the image out. What was confirmed, and every answer kept, belongs to one cluster and one image: another cluster may reach another registry, so nothing is inherited from a cluster that happened to be confirmed. After a restart of repman nothing is confirmed: with the registry down at that moment the
injection is not rendered until it answers.

**Where the check runs.** Only in the provisioning renders, never in the monitoring loop or the state machine: the
render is reached from `ProvisionServices` and `InitDatabaseService` (which start `OpenSVCProvisionDatabaseService` or
`K8SProvisionDatabaseService` in a goroutine and wait for its result), from `UpgradeDatabaseDeploymentOnStart` (the
rolling restart and upgrade) and from `OpenSVCUpdateDatabaseTemplate` (the API handler); their callers are the API
handlers, the cron scheduler functions, the security remediation (`go cluster.RollingRestart()`) and the regtest, and no
monitoring file calls them. The locks held across the render are `provisioningMutex`, which only the provision and
unprovision operations take (it serialises their `errorChan`), and `rollingReprovMutex`, which only `RollingReprov`
takes against itself; neither is taken by the monitor, and the lock of the check itself is never held while the network
is used. A render with a cold cache and a registry that does not answer costs one request (10 s) and at most one wait
for a turn (10 s) per cluster and image, kept for 5 minutes; the provisioning in progress lasts that much longer.
A check that panics ends its request: the flight is removed and the callers waiting on it are released.

A helper image that repman cannot read anonymously is refused too: mirror it into a registry repman can reach without
credentials, or leave the setting empty and use an image that ships the tools. What the check cannot see is a node that
cannot pull an image the registry hands out (a network rule between the node and the registry, for instance): that case
still ends in `Init:ImagePullBackOff` on Kubernetes.

## OpenSVC

When the setting resolves to an image:

- a blocking, optional init container `container#03` (between the bootstrap `container#02` and `container#db`) runs the
  helper image as `--user 0:0` (those images default to a non-root user) with `{name}/xtrabackup` mounted read-write at
  `/bundle`, `start_timeout = 120s`, `image_pull_policy = always` when `opensvc-image-force-pull` is set;
- the data volume gets the extra directory `xtrabackup`;
- `container#jobs` mounts `{name}/xtrabackup:/opt/xtrabackup:ro`. `container#db` is untouched.

## Kubernetes

- an `emptyDir` `<server>-xtrabackup` with a 256 MiB size limit;
- an init container `<server>-xtrabackup` (the helper image, root) appended after the existing bootstrap init container,
  mounting the emptyDir read-write at `/bundle`;
- the dbjobs sidecar mounts it read-only at `/opt/xtrabackup`; the database container does not mount it.

The emptyDir lives as long as the pod, so the bundle is copied at every pod start (a few seconds); the init container
exits 0 even when the copy failed, as above.

## The tools on the PATH of the container

The injection is **Docker level**: it is complete when the service is started or provisioned, and nothing in it depends
on `dbjobs_new.sh`. That script cannot run without `socat` (it reaches repman, dispatches its jobs and receives its own
upgrade through it), so a jobs container whose tools are only found by the script could never get that script.

The PATH of the **jobs container itself** gets the bundle, rendered by repman (`xtrabackupBundlePath`), the same way on
OpenSVC and Kubernetes:

- OpenSVC: `PATH=<default>:/opt/xtrabackup/current/bin` in the `environment` of the jobs container
  (`OpenSVCGetJobsContainerSection`), after `MYSQL_INITDB_SKIP_TZINFO=yes`;
- Kubernetes: the same `PATH` as an `Env` entry of the `dbjobs` container (`k8sAddXtrabackupBundle`).

The bundle goes **last**, so a tool the image ships itself wins. It is set only when the injection is on **and** the
database image is one of the two official ones (`IsStockMySQLImage`: Docker Hub's `mysql` or `library/mysql` and
`percona/percona-server`, with an optional Docker Hub host, a tag or a digest): the environment of a container replaces
the PATH of its image as a whole, so the default has to be known, and both have the same one
(`/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`).

**A custom image is never touched.** An image built or mirrored under another name or registry (`myco/mysql:8.0-custom`,
`registry.example/mirror/mysql:8.4`) may have a PATH of its own: whatever the setting (`auto` or an explicit image),
nothing at all is rendered for it, neither helper, volume, mount nor PATH, and the setting is logged once as ignored. A
bundle that is copied but not on the PATH of the jobs container would be a half-done injection. With the setting empty
(the default) nothing is rendered either. Once the helper image is pulled, its script exits 0 even when a check fails, so
a failed bundle does not keep the database from starting; a helper image that cannot be pulled is handled before the render, see
"A helper image that cannot be pulled".

The setting reaches a container when the service is provisioned again (a change of the environment is a change of the
service definition); a jobs container that is already running keeps the PATH it was started with.

`dbjobs_new.sh` has one change: `XTRABACKUP` is `${CLIENT_BASEDIR}/xtrabackup` when that file exists, else the
`xtrabackup` found on the PATH.

## Scope

Three things, checked when the service starts or is provisioned: `socat` (the requirement of all: without it the jobs
script does not run), `xtrabackup` and `xbstream` are **available** on the PATH of the jobs container, can be
**executed**, and xtrabackup is the **right version** for the server. Whether a backup, a transfer or a restore that use
them succeed is not part of it.

The init container does the checking, in the helper image, before the bundle replaces the previous one: `socat` first
(`socat -V`), then `xtrabackup --version` and `xbstream --version`, each run through the bundled loader, so a tool that
is present but cannot start (a missing library, another architecture) is refused. The series (major.minor) of the MySQL
server xtrabackup is "based on" is compared with the series of the database image tag (`XB_SERIES`, passed by repman for
a stock image whose tag names one: `mysql:8.0.35` is 8.0); xtrabackup 8.0 does not back up an 8.4 server, nor the
reverse. With a tag that names no series (`latest`, a digest) the series check is skipped. A refusal is written to the
init container's log and to `status` (`failed <reason>`); the previous bundle is removed (see the lifecycle), and the init
container still exits 0 (once its image is pulled) so the database starts.

`share/scripts/tests/xtrabackup_bundle/docker_check.sh` is the check from outside, at Docker level, on a running jobs
container (see Tests).

Not part of this change (kept out on purpose, to be done separately): a check in the monitor that raises an alert when
`status` is `failed`, and the success of the backup and restore jobs themselves.

## Trust

The bundle's binaries come from the image the operator names and run as root in the jobs container (not privileged; it
only sees the volumes of its own service). The helper itself runs as root (`--user 0:0` / `runAsUser: 0`) to fill the
volume and to run each tool once from the staging directory. Only a **digest** is immutable: a tag, even a pinned-looking
one, can be re-pointed. Use a digest from a registry you trust, or mirror the image.

## Not covered

- The legacy OpenSVC collector-API path (`opensvc-use-collector-api`): not audited, unchanged. Per T21 the collector
  rulesets and `share/opensvc/moduleset_*.json` are not edited for this feature.
- Other architectures than x86_64.
- Kubernetes: unit tested, and run on a kind cluster (two pods on two workers, `mysql:8.0.35`, `auto`) with
  `docker_check.sh`. Not run on other distributions.
- A node that cannot pull a helper image the registry hands out (see "A helper image that cannot be pulled"): the
  database does not start on Kubernetes in that case.

## Tests

- Go (`cluster/prov_xtrabackup_bundle_test.go`, `config/config_xtrabackup_img_test.go`): `auto` derivation, image
  validation, the helper command with `XB_SERIES`, the series read from the image tag, the OpenSVC and Kubernetes renders
  (empty setting renders what it rendered before; with an image: helper, read-only jobs mount, the PATH on the jobs
  container only, database image unchanged), and a value that arrives from a TOML file or a flag
  (`TestXtrabackupBundleImageFromConfig`): spaces, `;`, quotes, `$(...)`, a newline and an over-long value render no
  helper and leave the injection off.
- The init script against real xtrabackup images: the matching series gives `ok`, a mismatch both ways gives
  `failed xtrabackup is for the MySQL series ...`.
- `share/scripts/tests/xtrabackup_bundle/docker_check.sh <server version> -- <exec prefix>` on a provisioned jobs
  container, `docker exec` on OpenSVC and `kubectl exec` on Kubernetes: `socat` first, then `xtrabackup` and `xbstream`
  on the PATH, executable, and xtrabackup of the server's series. It prints `RESULT PASS` or `RESULT FAIL: n check(s)`.
- `share/scripts/tests/xtrabackup_bundle/docker_matrix.sh`: the automated check, no repman or orchestrator needed. For
  each official database image (`mysql:8.0.35`, `mysql:8.4`, `percona/percona-server:8.0`, `:8.4`) it runs the init
  script in the matching xtrabackup image on an empty volume, starts the database image as the jobs container with the
  volume read-only and the PATH of the render (read from `xtrabackupStockPath`), and runs `docker_check.sh`. It checks
  the failures too: no injection must fail the check, and a series mismatch must be refused and leave no bundle. It
  also covers the MariaDB images (see below). One `RESULT <case> PASS|FAIL` line per check; the exit status is 0 only
  when all pass.
- The helper image check (`prov_xtrabackup_helper_check_test.go`, also run with `-race`): 401, 403, 404 and a bad
  reference leave the whole injection off on both orchestrators; a network error renders only an image confirmed
  earlier; answers expire (a tag that is gone turns the injection off at the next provisioning, a refusal is not undone
  by a later outage, a corrected registry is noticed; the confirmation of one cluster is not inherited by another, a confirmation expires, and the memory, the requests in flight and the queue stay bounded however many clusters provision at once, each waiting for its turn); requests for one image are shared and a slow image blocks no
  other. Also tried once against Docker Hub: the real image passes, a wrong tag and a wrong repository are refused, an
  unknown host is a network error.
- MariaDB, the third product: `TestXtrabackupInertOnMariaDB` and `TestXtrabackupInertOnMariaDBKubernetes` (the setting
  renders nothing, `auto` or an explicit image: on OpenSVC no section and no environment, on Kubernetes the pod spec is
  equal to the one of the empty setting; the same for a database image that is not an official one; the registry is not
  asked), and the matrix checks that `mariadb:10.11` and
  `mariadb:11.8` ship `socat`, `mariabackup` and `mbstream` themselves.
