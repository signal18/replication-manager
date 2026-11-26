# One build (4.0.1)

Until 3.x, `make` produced several replication-manager servers from the same source, each
one fixed at link time by `-X github.com/signal18/replication-manager/server.With...=ON|OFF`
linker flags: `osc`, `osc-cgo`, `tst`, `pro`, `emb`, and a `-basedir` twin of most of
them. In 4.0 there is **one server binary**: what an instance can do depends on the
plugins it receives and on its plan, never on how it was linked. This page is the
inventory of what the flavours were, and how the one build keeps every existing install
behaving exactly as before.

## 1. Inventory before the change

### 1.1 Build tags

| Tag | Selects | Kept |
|---|---|---|
| `server` | `main_server.go`, the server program | yes: one server binary |
| `clients` | `main_client.go`, the CLI (`replication-manager-cli`), and the reduced `share/embed_stub.go` | yes: separate program (see 4) |
| `arbitrator` | `main_arbitrator.go`, the arbitrator (`replication-manager-arb`) | yes: separate program (see 4) |
| `agent` | `agent.go` | unchanged |
| `cairo` | graphite PNG rendering through cairo (`graphite/expr/cairo.go`), never set by any target | unchanged |
| `integration`, `kindsmoke`, `ignore` | tests only | unchanged |
| `linux`, `darwin`, `windows`, `unix` | platform files | unchanged |

No build tag ever selected a server flavour: the flavours were linker flags only.

### 1.2 Linker variables of the server package (`server/server_cmd.go`)

| Variable | Default in the source | Set by | What `OFF` / `ON` switched |
|---|---|---|---|
| `WithProvisioning` | ON | `osc`, `osc-cgo`: OFF | OFF: none of the provisioning flags (`prov-db-*`, `prov-proxy-*`, `prov-sphinx-*`, `prov-net-*`, `prov-kube-*`, compliance, `cloud18-gateway-service`, ...) registered, so they kept the zero value; `measurement` default false (true otherwise); `binlog-copy-mode` default `ssh` (`mysqlbinlog` otherwise); `prov-db-config` default false (true otherwise) |
| `WithOpenSVC` | OFF | `pro`, `emb`, release `replication-manager-<os>-<arch>`: ON | ON: `opensvc` (true), `opensvc-host`, `opensvc-admin-user`, `opensvc-p12-certificate`, `kube-config`, `prov-timeout`, ... registered; `prov-orchestrator` default `opensvc` (else `onpremise`); `prov-orchestrator-enable` default `opensvc,kube,onpremise,local` (else `onpremise,local`); `prov-orchestrator-cluster` registered |
| `WithTarball` | empty | `*-basedir`: ON | default directories under `/usr/local/replication-manager` |
| `WithEmbed` | OFF | `emb`, `cli`, release `replication-manager-<os>-<arch>`: ON | default directories under the user's home, share files read from the embedded copy, `decrypt` allowed for a non-root user |
| `WithDeprecate` | ON | `tst`: OFF | nothing: read nowhere |
| `WithArbitration` | OFF | set at run time by the arbitrator program | the arbitrator needs no config backup directory |
| `WithArbitrationClient`, `WithProxysql`, `WithHaproxy`, `WithMaxscale`, `WithMariadbshardproxy`, `WithMonitoring`, `WithMail`, `WithEnforce`, `WithSphinx` | ON | never | registration of their flags; always ON in every build |
| `WithSpider`, `WithMySQLRouter` | empty | never | `spider` and `mysqlrouter-*` flags never registered (their fields stayed at the zero value) |
| `WithHttp`, `WithBackup`, `WithReact` | ON | never | nothing: read nowhere |

The `clients` package has its own copy of these variables (`clients/client_cmd.go`): only
`clients.WithEmbed`-style values are read by the CLI, and none of them depends on a server
flavour.

### 1.3 Makefile targets (server)

| Target | Binary | Flags |
|---|---|---|
| `osc` | `replication-manager-osc` | Provisioning OFF, static |
| `osc-basedir` | `replication-manager-osc-basedir` | + Tarball |
| `osc-cgo` (amd64) | `replication-manager-osc-cgo` | Provisioning OFF, CGO on |
| `osc-cgo-basedir` | `replication-manager-osc-cgo-basedir` | + Tarball |
| `tst` | `replication-manager-tst` | source defaults (Provisioning ON, OpenSVC OFF) |
| `tst-basedir` | `replication-manager-tst-basedir` | + Tarball |
| `pro` | `replication-manager-pro` | OpenSVC ON |
| `pro-basedir` | `replication-manager-pro-basedir` | + Tarball |
| `emb` | `replication-manager` | OpenSVC ON + Embed |
| `all` = `cli bin tar arb`; `bin` = `osc tst pro osc-cgo emb`; `tar` = the four `-basedir` | | |

Each of the five server targets compiled the whole tree again: `make` linked the server
nine times.

### 1.4 Packages (`package_linux.sh`, `nfpm/*.yaml`, `build-packages.yml`)

On a tag, `build-packages.yml` runs `package_linux.sh` for amd64 then arm64; it builds
`make` (`all`) and packs four nfpm configs into deb and rpm:

| Package | Content | systemd unit |
|---|---|---|
| `replication-manager-osc` | `/usr/bin/replication-manager-osc`, `/etc/replication-manager/config.toml` and the cluster sample (config files), `/usr/share/replication-manager` | `/etc/systemd/system/replication-manager.service` running `/usr/bin/replication-manager-osc --user repman monitor` |
| `replication-manager-pro` (`nfpm/prov.yaml`) | `/usr/bin/replication-manager-pro`, same config and share | same unit file, running `/usr/bin/replication-manager-pro --user repman monitor` |
| `replication-manager-client` | `/usr/bin/replication-manager-cli` | none; required `>=` the same version by osc and pro |
| `replication-manager-arbitrator` | `/usr/bin/replication-manager-arb` | `replication-manager-arb.service` |

`osc` and `pro` install the same paths (`/usr/share/replication-manager`, the unit file), so
they could never be installed together: installing one over the other failed on a file
conflict (the user doc tells the reader to remove the other one first). The `-basedir`
and `-cgo` binaries were built by `make` but shipped in no package.

**What on-premise package clients run today:** one of `replication-manager-osc` or
`replication-manager-pro`, with `replication-manager-client`; some also run
`replication-manager-arbitrator`. The package names, the binary paths and the unit file
are what an upgrade must keep. (Which client runs which flavour is tracked privately, not
in this public repository.)

### 1.5 Docker images (`docker-build-push.yml`)

| Image tag | Dockerfile | Server inside |
|---|---|---|
| `{tag}`, `latest` | `docker/Dockerfile` | osc, as `/usr/bin/replication-manager` |
| `{tag}-rootless`, `latest-rootless` | `docker/Dockerfile.rootless` | osc |
| `{tag}-pro` | `docker/Dockerfile.pro` | pro |
| `{tag}-pro-rootless` | `docker/Dockerfile.pro_rootless` | pro |
| `nightly` | `docker/Dockerfile.pro` | pro |
| `nightly-rootless` | `docker/Dockerfile.pro_rootless` | pro |
| `{tag}-slim`, `-slim-rootless` | `docker/Dockerfile.slim`, `.slim_rootless` | osc |
| `dev`, `dev-rootless` | `docker/Dockerfile.dev`, `.dev_rootless` | pro (dev tree, `./replication-manager-pro`) |
| `arb`, `nightly-arb` | `docker/Dockerfile.arb`, `.arb_rootless` | arbitrator |

Every image installs its server as `/usr/bin/replication-manager`, so the image, not the
program name, said which flavour it was.

### 1.6 Release binaries (`release-binaries.yml`)

On a GitHub release: `replication-manager-cli-<os>-<arch>`, `replication-manager-arb-<os>-<arch>`
and `replication-manager-<os>-<arch>`, the last one linked with Embed + OpenSVC (the
"embedded binary" of the user doc: runs from the user's home, no package).

### 1.7 Other references

- Dev instances (`docker/dev/run.sh`, `entrypoint.sh`, `start_licence_builder.sh`) run
  `./replication-manager-pro` or `./replication-manager-osc` from the tree, built with
  `make cli pro`, and their scripts find the monitor by that process name.
- `share/tests/*/run.sh` run `replication-manager-pro`.
- `etc/config.toml` comments name the flavours' default orchestrators.
- `.travis.yml` (`make osc`) and the root `Jenkinsfile` are dead (CI is GitHub Actions).
- User doc: installation overview (flavour table), repository install (`apt/yum install
  replication-manager-osc`), Docker, embedded binary, orchestrators.

## 2. Design of the one build

### 2.1 One binary

`make bin` (`server` target) links the server once:
`build/binaries/replication-manager`, static, `CGO_ENABLED=0`; the flavour names are
symlinks to it (2.4). Everything is compiled in
and every flag is registered in every instance: the provisioning and OpenSVC flags exist
on an osc install too (they can be set from the config file, the API and the GUI).

### 2.2 The install profile: defaults only, never a capability

Existing installs must behave as before after the upgrade. What differed between flavours
is only a set of **default values** (1.2), so the one binary picks those defaults from an
**install profile** resolved at start, before any flag is registered (the defaults feed the
default-flag map that the config save compares against, so they cannot be changed after
the config is read):

| Profile | Provisioning defaults | OpenSVC defaults | Same defaults as |
|---|---|---|---|
| `osc` | off: every provisioning key at its zero value, `measurement=false`, `binlog-copy-mode=ssh`, `prov-db-config=false` | off | the old `osc` |
| `tst` | on | off: `prov-orchestrator=onpremise` | the old `tst` and an unflagged build |
| `pro` | on | on: `opensvc=true`, `prov-orchestrator=opensvc`, ... | the old `pro` and `emb` |

and an **install layout** for the default directories: `package` (`/etc`, `/var/lib`,
`/usr/share`), `tarball` (`/usr/local/replication-manager`), `embed` (the user's home and the
embedded share).

"Off" registers the flags with their zero value, exactly the value an osc instance had
when they were not registered: an osc install does not start provisioning, measurement or
config deployment on its own, and its saved configuration does not change. Setting any of
these keys explicitly works on every profile; the profile is a default, not a gate.

Resolution (`server/server_install_profile.go`, `resolveInstallProfile`), first match wins:

1. `REPLICATION_MANAGER_INSTALL_PROFILE` = `osc|tst|pro` and
   `REPLICATION_MANAGER_INSTALL_LAYOUT` = `package|tarball|embed` in the environment (each
   one independently). The Docker images set them.
2. The program name `replication-manager-<word>[-...]`: `osc`, `tst`, `pro` select the
   profile (package layout), `emb` pro + embed, a later `basedir` the tarball layout; `cli`
   (the CLI, which embeds the server package) is `tst` + `embed` and `arb` (the
   arbitrator) is `tst` + `package`, as they were linked before. The flavour names are
   **symlinks** to the one binary (2.4): `os.Args[0]` is the name the process was started
   by, the symlink's, so the unit `ExecStart=/usr/bin/replication-manager-osc` resolves to
   the profile its package had.
3. A Go test binary (`*.test`): `tst` + `package`, the defaults of an unflagged build.
4. The plain name `replication-manager` beside exactly one flavour symlink resolving to the
   same file (`/usr/bin/replication-manager` + `/usr/bin/replication-manager-osc` on an osc
   package host): that flavour + `package`, so an operator typing `replication-manager
   version` or `decrypt` on a package host gets the package's profile and directories. With
   several flavour symlinks (a dev tree has them all) this rule does not apply.
5. Any other name (`replication-manager`, `replication-manager-linux-amd64`): `pro` +
   `embed`, the old `emb` binary and release download. The monitor logs this choice at
   WARN, naming the two variables, so a renamed binary does not change its defaults
   unnoticed.

Only the word right after `replication-manager-` names a flavour (`replication-manager-pro`,
`replication-manager-osc-basedir`); `basedir` counts only after a flavour word. A name with
an unrelated suffix (`replication-manager-host1-arb`, `repman-osc`) falls to rule 5.

An unknown value in the environment is ignored with a message on stderr and the next
source applies. The profile and layout are printed by `version` and logged at start, and
exposed in the config (`installProfile`, `installLayout`, read-only).

Neither variable is a TOML key: they decide where the TOML is read from, like `--config`,
and they are set by the package or the image, not by an operator. The two names are
excluded from the environment cluster discovery (`REPLICATION_MANAGER_<CLUSTER>_<KEY>`).

### 2.3 What goes away

- The `With*` linker variables that never changed (`WithMaxscale`, `WithHaproxy`, ...) and
  the unread ones (`WithHttp`, `WithBackup`, `WithReact`, `WithDeprecate`): their flags are
  always registered. `spider` is now registered with the default its field already had
  (false). The `mysqlrouter-*` flags stay unregistered: no build ever registered them, and
  registering them would put a default secret (`mysqlrouter-pass`) in every instance.
- `WithProvisioning`, `WithOpenSVC`, `WithTarball`, `WithEmbed` stay as package variables
  (many readers), set from the profile; a `-X` on them is no longer honoured.
- `osc-cgo` is dropped (shipped nowhere).

### 2.4 One real binary, the flavour names are symlinks

There is one real file, `replication-manager`; every flavour name is a relative symlink to
it in the same directory, so nothing that calls an old name breaks:

| Where | Real binary | Symlinks |
|---|---|---|
| Makefile | `build/binaries/replication-manager` (`make server`) | `replication-manager-osc`, `-tst`, `-pro`, `-emb`, `-osc-basedir`, `-tst-basedir`, `-pro-basedir` (`ln -sfn replication-manager ...`) |
| `replication-manager-osc` package | `/usr/bin/replication-manager` | `/usr/bin/replication-manager-osc` (nfpm `type: symlink`) |
| `replication-manager-pro` package | `/usr/bin/replication-manager` | `/usr/bin/replication-manager-pro` |
| osc images (`Dockerfile`, rootless, slim) | `/usr/bin/replication-manager` | `/usr/bin/replication-manager-osc` |
| pro images (`Dockerfile.pro`, rootless) | `/usr/bin/replication-manager` | `/usr/bin/replication-manager-pro` |
| dev images | `/usr/bin/replication-manager` (copied from the tree) | `/usr/bin/replication-manager-pro`; the tree keeps `./replication-manager-pro` -> `build/binaries/replication-manager-pro` -> `replication-manager` |

What a process started through a symlink carries:

- `os.Args[0]` is the symlink path: the profile (rule 2). Covered by
  `TestInstallProfileThroughSymlink`, which re-executes the test binary through
  `replication-manager-osc`, `-pro` and `-pro-basedir` symlinks (a symlink to a symlink, as
  in the tree).
- `ps aux` / `ps -ef` print the command line, so the symlink name: the dev scripts
  (`stop.sh`, `wait.sh`: `ps aux | grep replication-manager-pro | grep monitor`) still find
  the monitor. Checked on macOS: a program started through `x-osc -> x` shows `x-osc` in
  `ps -o command` and is found by `pgrep -x x-osc`; the kernel's own short name (`ucomm`) is
  the target's there.
- On Linux the kernel names the task after the basename of the path given to `execve`, the
  symlink's (the same reason a process re-executed through `/proc/self/exe` is named `exe`,
  which `utils/s3fuse/stuck_linux.go` relies on). Not checked by a run in this change (no
  Linux host reachable from the session that wrote it); nothing in the tree depends on it:
  the dev scripts read the command line, and `utils/s3fuse` matches the 15-character
  `replication-man`, the same for every name.
- `os.Executable()` is the real file (`/proc/self/exe` resolves the link): rule 4 uses it to
  find the sibling flavour symlink.
- Re-executed children (the backups folder mount server `utils/s3fuse`, the /etc writer
  `utils/etcwriter`) start from `selfexec.Path()` (`/proc/self/exe`) under a plain name, so
  their own program name says nothing. The server hands them its resolved profile in the
  environment (`selfexec.ProfileEnv`, appended by `selfexec.ChildEnv`; the s3fuse allowlist
  gets the two variables, no secret), so a child always resolves its parent's defaults and
  directories (otherwise, in a dev tree or as `exe`, it would take pro + embed and the
  embedded layout's home directory).

Upgrade from 3.x: `/usr/bin/replication-manager-osc|pro` was a regular file owned by the
package and becomes a symlink owned by the same package; dpkg and rpm replace a file by a
symlink on upgrade. `/usr/bin/replication-manager` is new in the packages: a file of that
name installed by hand (the release download) is overwritten by the package.

## 3. What a package client sees on upgrade

- `apt upgrade` / `yum update` of `replication-manager-osc` or `replication-manager-pro`:
  same package name, same `/usr/bin/replication-manager-osc|pro` (now a symlink to the new
  `/usr/bin/replication-manager`), same unit file, same config files (left untouched, they
  are config files). The unit starts the monitor through the flavour symlink, whose name
  selects the profile the package had, so every default is the one it had. Nothing to do.
- New: each server package declares a conflict with the other (deb `Conflicts` +
  `Replaces`, rpm `Conflicts`), so switching from osc to pro (or back) is one
  `apt install replication-manager-pro` (rpm: `dnf swap replication-manager-osc
  replication-manager-pro`) instead of a file conflict. An `apt upgrade` / `yum update`
  never switches: it only upgrades the installed package. No rpm `Obsoletes`, which would
  make an update replace one flavour by the other. The config files stay.
- `replication-manager-client` and `replication-manager-arbitrator`: unchanged.
- Docker `latest` / `{tag}` keep osc defaults, `-pro` and `nightly` keep pro defaults
  (environment set in the image). A container that overrides the environment can choose
  another profile.

## 4. Not in this step

- The CLI and the arbitrator stay separate programs (`clients`, `arbitrator` tags): the CLI
  is copied into the database job containers and must stay small; the arbitrator runs on a
  third site without the server. Folding them into the server binary as subcommands is a
  decision for the maintainer.
- Gating by plan or by plugin (the end state): the profile only reproduces the old
  defaults. The next step moves each default to the plan and the plugins an instance
  receives, after which the profile can go.
- `docker/Dockerfile.pro*` and `Dockerfile` differ only by the profile now; merging them
  into one Dockerfile with a build argument is left for a later change so external builds
  referencing the file names do not break.
