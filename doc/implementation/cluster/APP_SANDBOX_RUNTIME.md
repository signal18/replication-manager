# Sandbox runtime of the client app containers (`prov-app-docker-runtime`)

## What it is for

A client app (OpenSVC orchestrator, docker or podman) runs an image and a command the client
chooses (`prov-app-docker-img`, `prov-app-docker-cmd`). That freedom stays: the isolation from
the node does not come from restricting the image, it comes from the **runtime** of the
containers that run it, set by the instance admin only.

## The setting

| | |
|---|---|
| Key | `prov-app-docker-runtime` (`Config.ProvAppDockerRuntime`, json `provAppDockerRuntime`) |
| Scope | `scope:"server"`: global, admin only (global settings API and GUI, Cloud18 > Marketplace > Apps > App Containers) |
| Default | `auto` |
| Values | `auto`: `runsc-hostnet` when the instance is a Cloud18 one (`cloud18 = true`), the node's default runtime otherwise. `none` or empty: the node's default runtime (the off-switch, T14). Any other value: a docker runtime name (`[A-Za-z0-9][A-Za-z0-9._-]{0,63}`) |
| Resolver | `config.(*Config).GetAppDockerRuntime()`; validation `config.ValidateAppDockerRuntime` |

`auto` is a value of its own so that "unset" (follow the instance kind) and "explicitly off"
(`none`, or empty) are distinguishable, the same idiom as `compress-backups-logical` or
`backup-restic-reseed-strategy`.

An invalid value (only possible from a configuration file: the API refuses it) is an
**error** at rendering, never a silent fallback to the default runtime: the provisioning of
the app stops with the message.

Being `scope:"server"`, a cluster user never changes it: the cluster settings routes hand a
server key to the global handler (admin check), the MCP refuses it, an app setting
(`App.SetSetting`) has no such key, a template unmarshals into `AppConfig`, which does not
carry it. The `set-cron` route, which used to pass any key name to the cluster setter,
now takes `*-cron` keys that are not server scope only.

A change applies at the next provisioning (or definition refresh) of each app: the running
containers keep their runtime until then.

## The sandbox turned off: WARN0328 (security domain)

On a Cloud18 instance (`cloud18 = true`) the sandbox is the norm. When the setting resolves
to the node's default runtime there (`none` or empty) and the cluster has client apps,
`CheckAppSandboxRuntime` (each tick, `cluster/app_sandbox.go`) opens **WARN0328** in the
cluster's `SecurityStateMachine` (WARNING, `ErrFrom` SECURITY): "the client apps of this
cluster run without the sandbox". Turning the sandbox off stays possible (T14: the setting is
the off-switch), but it is no longer silent: the state is in the security pill and the
security log, and alerts like any state. It closes on the next tick after the setting is
back to `auto` or a runtime name. Nothing is reported off Cloud18 (the node's runtime is the
norm there), on a cluster without apps, or for an invalid value (the provisioning already
stops with APPERR012). The state says what the setting is, not what each running container
runs: a change applies at the next provisioning of each app (see above).

## Trusted apps: `prov-app-docker-trusted`

An app of the instance itself (its own code, not exposed to the clients) can be too slow under
gVisor. The instance admin can **trust** it: its containers run under the node's default
runtime, every other app keeps the sandbox.

| | |
|---|---|
| Key | `prov-app-docker-trusted` (`Config.ProvAppDockerTrusted`, json `provAppDockerTrusted`) |
| Scope | `scope:"server"`: global, admin only, like the runtime |
| Default | empty (no app trusted) |
| Value | a comma list of app ids (`ap<n>`, `App.SetID`: `ap` + the crc64 of cluster and app name), normalized (trimmed, each once) and validated (`config.ValidateAppDockerTrusted`) |
| Resolver | `config.(*Config).IsAppDockerTrusted(appID)`, used by `cluster.(*Cluster).appDockerRuntime(app)` |

**Why a global list, not an `AppConfig` field.** The point is that a cluster user with the
app-config grant cannot exempt an app from the sandbox. An `AppConfig` field is written by
many paths a cluster user drives: `App.SetSetting`, the app creation body, reset-from-template,
save-as-template (which would carry the exemption to every app created from the template), the
cluster's TOML. A server key is written by one path, the global settings handler, which
`isGlobalSettingsAdmin` gates (the local admin only); the cluster routes hand a server key to
that handler, the MCP refuses it (`SetClusterSetting`), the app settings route does not know it
(`unknown setting`). No new endpoint: the existing global settings route, its ACL rule and its
Swagger entry carry it.

**Why ids, not names.** The value of a server key is copied into every cluster's `Conf`, which a
cluster user reads in the cluster's configuration (JSON). A list of `cluster/app` names would show
every client the names of other clients' clusters and apps; an id is a hash that names nothing.

**Residual.** The id is derived from the cluster and app names: an app deleted and created again
with the same name in the same cluster is the same id, so it is trusted again. Only the users of
that cluster can create it, and WARN0329 names it as soon as it exists. Remove an app from the
list before deleting it.

`appDockerRuntime(app)` returns `""` for a trusted app; `applyAppDockerRuntime(svcsection, app)`
then adds no `--runtime`, and `openSVCAddAppReloadSection` renders the docker reload task (the
app's PID namespace), as off the sandbox. An invalid `prov-app-docker-runtime` is still an error
for a trusted app: the setting is wrong whatever the app.

### WARN0329 (security domain)

`CheckAppDockerTrusted` (each tick, `cluster/app_sandbox.go`) opens **WARN0329** in the cluster's
`SecurityStateMachine` (WARNING, `ErrFrom` SECURITY) while the sandbox is on
(`GetAppDockerRuntime` resolves to a runtime) and apps of this cluster are trusted, naming them.
Nothing is reported when the sandbox is off (WARN0328's case), when no app of the cluster is in
the list, or for an invalid runtime. Like WARN0328 it states the settings, not what each running
container runs: a change applies at the next definition refresh or provisioning of the app.

### GUI

- App settings (Overview, General): **Trusted (no sandbox runtime)** switch. Shown to the local
  `admin` session or a person in admin mode (`adminMode.isActive()`), and to everyone while the
  app is trusted (read-only for them). It writes the whole list through `setGlobalSetting`; a
  refusal for want of the local admin brings up the admin login (`adminMode.js`).
- Global settings, Cloud18 > Marketplace > Apps: **Trusted Apps (no sandbox)**, the list as text.

## Where it goes

`OpenSVCGetAppTemplateSectionMap` renders the whole service, then `applyAppDockerRuntime`
appends `--runtime=<runtime>` at the **end** of `run_args` of every docker/podman section whose
image is `{env.app_img}`, the client's image:

| Section | Image | Runtime |
|---|---|---|
| `container#app` | client | yes |
| `container#jobs` (jobs sidecar of an engine app, e.g. PostgreSQL) | client | yes |
| `task#reload` (live resize reload command) | client | runs **in** `container#app` (see below) |
| `container#01` (pause, holds the network namespace) | ours | no |
| `container#sensor` | ours | no |
| `container#NNinit<git>` (git clone), `container#NNinitjobs` (client download) | ours | no |
| `container#NNinit<s3>` (S3 FUSE mount, privileged) | ours | no |
| database and proxy services | n/a | not touched |

Keyed on the image rather than on section names, a future section of the client's image
gets the runtime without a change here. Last on the line, the runtime is the one docker keeps
whatever precedes it.

The OpenSVC `container.docker` driver has no runtime keyword: om starts the container with
the docker CLI and passes `run_args` through, so the runtime is a run option.

### The reload task under the sandbox

Off the sandbox, `task#reload` is a container of the client's image that joins the app's
PID namespace (`pidns = container#app`) and signals its processes through `/proc/1`. Under
gVisor the app's processes live in the sandbox's own kernel: no other container can join
that PID namespace. A gVisor reload container sees only itself (`cat: /proc/1/root/run/php/
php8.4-fpm.pid: No such file or directory`, observed on a node), and a runc one would run
the client's image outside the sandbox. So when a runtime is set, `openSVCAddAppReloadSection`
renders a **host task that runs the command in `container#app`**:

```ini
[task#reload]
type = host
container = container#app
configs_environment = <app>/*
command = /bin/sh -c 'read -r k </proc/version;case "$k" in *gvisor*) eval "$APP_RELOAD_COMMAND";;*) echo "reload refused: not in the app sandbox" >&2;exit 1;;esac'
```

om3's `task.host` `container` keyword runs the command through the container's runtime (an
exec into the sandbox). The environment is computed by the agent at each run
(`EncapEnv` over `configs_environment`, read in the om3 v3.0.0-rc56 source), so the reload
reads the keys of that moment, as the docker task did. Checked on a node (om3
v3.0.0-rc56): the PHP reload re-renders the sizing files and restarts the php-fpm pool
inside the gVisor app. Off the sandbox the docker task is unchanged. Only the `env` keys
are passed (`configs_environment`), in both forms: a reload command that needs a secret key
does not get it.

**Fail-closed guard.** An om3 agent that does not know the `container` keyword only *warns*
on it (`unknown keyword` is a warning alert in `core/xconfig/validate.go`, the object loads):
the section would then be a plain host task and the command would run on the node, as the
agent's user. The command therefore checks where it runs before it evaluates anything
(`appReloadSandboxCommand`):

- a `runsc*` runtime: the kernel the shell reads must be gVisor's (`/proc/version` is
  `Linux version 4.19.0-gvisor ...` inside a `runsc-hostnet` container, the node's kernel
  otherwise; checked on a node);
- any other runtime: the container files docker (`/.dockerenv`, present under
  `runsc-hostnet` too) or podman (`/run/.containerenv`) create, which a node does not have.

Residual: on the non-`runsc` form the guard only proves "inside some container". An agent
that itself runs in a docker container has `/.dockerenv` at its root, so an agent that
ignored the `container` keyword *and* runs containerized would pass it, with a non-gVisor
runtime only. The `runsc*` form has no such gap (the gVisor kernel is not the agent's).
Accepted: om3 agents are installed on the nodes, not in containers, and the agent that
ignores the keyword is an older om3, which also only warns.

Outside, it prints `reload refused: not in the app sandbox` and exits 1: the task fails, the
live resize reports it (APPERR011), nothing ran on the node. The text is fixed (the runtime
name only picks one of the two forms) and carries no ` ;` nor ` #`, which om3's INI reader
would take as the start of a comment; a command with a `;` is run by om3 through
`/bin/sh -c`, which the guard is written for.

## Why `runsc-hostnet`, not `runsc`

OpenSVC's model: `container#01` (pause, runc) holds the network namespace and the service
address; `container#app` joins it (`netns = container#01`). Under plain `runsc`, gVisor runs
its own network stack in the sandbox and takes the address of the namespace it joins: the pause
container and every sidecar sharing the namespace lose it, and the app has no network after a
restart. With `--network=host`, gVisor uses the namespace it is given (the pause's), which is
what the model needs. Tested on a node: two start/stop rounds with the pause model.

## Node prerequisite

Every node that can run an app (the app's agents, the failover targets) must register the
runtime in `/etc/docker/daemon.json`, then reload docker:

```json
{
  "runtimes": {
    "runsc-hostnet": {
      "path": "/usr/local/bin/runsc",
      "runtimeArgs": ["--network=host"]
    }
  }
}
```

A node without it fails to start the app: docker refuses the run with `unknown or invalid
runtime name: runsc-hostnet`, which the orchestrator reports as the start failure of
`container#app` (the service ends `warn`/`down` on that node, its log carries the docker
message); nothing is silently started without the sandbox. There is no repman pre-flight
check: repman does not read the nodes' docker configuration. The node module
`compliance/node/gvisor` (`check` mode, read only) tells whether a node has the runtime;
run it on every node that can host an app before turning the setting on (or before the
`auto` default applies on a Cloud18 instance).

## What the client's values can no longer reach

The sandbox is only worth the definition around it. `validateAppDeploymentForService` runs
before anything is rendered (and before the orchestrator is asked for its pools) and refuses,
with an error naming the value:

- an image that is not one image reference (`ValidateDockerImage`, the character set the
  xtrabackup image already used, no leading `-`): the image is the argument after the run
  options, one starting with `-` would be read as an option;
- app memory or cores that are not a number: they are written as `--memory=` / `--cpus=`
  into `run_args` (`appRunArgsLimits`). Cores are whole or decimal, as docker `--cpus` takes
  them (`2`, `0.5`, `1.5`; a whole number is still written `2.0`), on a strict numeric
  pattern; the app setting `prov-app-cpu-cores` refuses the same values. `container#app`
  itself fails to render on a bad value: it is never rendered uncapped;
- a volume whose name is not an object name (`ValidateVolumeName`), a volume directory with a
  `..`, a space or a `:`, a volume user, group or permission that is not one;
- a path mapping whose source or container path carries a space, a `:` or a `..`, or whose
  container path is not absolute (`ValidateMountPath`, also enforced in
  `PathMapping.SafeDockerMapping`): a space would start a second mount (any node path), a `:`
  would add mount options;
- a git clone whose name is not an identifier (`[A-Za-z0-9][A-Za-z0-9._-]{0,62}`: it names
  a section and the prefix of its environment keys), whose branch is not a git ref name on a
  strict character set (`ValidateGitBranch`: no `..`, `//`, trailing `/`, `.` or `.lock`),
  whose repository is not an https host and path (`ValidateGitRepository`: with or without
  `https://` or `http://`, stripped from the environment value by
  `config.GitRepositoryWithoutScheme` since the script adds `https://` itself; ssh, `git@host:path`, `file://` and `ext::` are refused, the clone runs over
  https and would not clone them), whose volume is not a volume or whose directory is not a
  plain path;
- an S3 mount whose name is not an identifier, whose uid/gid is not numeric, or whose
  endpoint, bucket, region or mount directory is not one word (the mount sidecar's
  entrypoint word-splits them into its mount command).

**The git clone command** is a fixed script; no client value is part of its text:

```ini
command = -c 'rm -rf "$1" && mkdir "$1" && git clone -b "$GIT_<NAME>_BRANCH" -- "https://$GIT_<NAME>_REPO" "$1"' sh /bootstrap/<dir>
```

The branch, repository and credentials are environment values expanded inside double
quotes: the shell never evaluates a value (`$(...)`, backticks, `"`, `;` stay characters of
it); `--` ends git's options before the URL; the directory is the script's first argument.
om3 splits the `command` keyword of a container with shlex (`converters.Shlex`), which keeps
the single-quoted script one argument. A test runs the rendered command in a real shell
with hostile values in the environment and checks git's arguments.

**The S3 mount sidecar** (privileged, our image) gets only its own mount's keys, under the
names its entrypoint reads (`ENDPOINT`, `BUCKET`, `REGION`, `MOUNT_DIR`, `AWS_ACCESS_KEY`,
`AWS_SECRET_KEY`, mapped from `<app>/<MOUNT>_<KEY>` by `S3Mount.GetVariableKeys`), never the
app's environment: no client variable (`LD_PRELOAD`, `BASH_ENV`, `PATH`...) reaches it, so
no denylist is needed. Its entrypoint (`/root/run.sh` in `signal18/nfsmixr`) reads those
unprefixed names, which the app's wildcard environment (`<app>/*`, prefixed keys) never
provided.

`validateAppServiceSections` then refuses a rendered key or value with a line break, which
would start a new keyword in the orchestrator's INI configuration. The app setting
`prov-app-docker-cmd` refuses one too.

The command stays free: it is the container's arguments, after the image.

## Upgrade: stored values the validators refuse

The validators apply to every rendering, so an app stored before this change with a value
they refuse no longer renders: its next provisioning or definition refresh stops before
anything is sent to the orchestrator, and **the running containers are unchanged** (the
service keeps its previous definition). The refusal is:

- an **ERROR state on the app**, `APPERR012` "Application <app> service definition refused,
  nothing was sent to the orchestrator: <the value and why>", shown with the app's other
  states until a rendering succeeds (`App.ServiceDefinitionError`);
- an error line in the app log, and the error returned to the API call or job that asked.

Every rendering goes through `OpenSVCGetAppTemplateSectionMap`, which sets or clears the
state: the provisioning (v2 and v3), the v3 definition refresh (`OpenSVCUpdateAppTemplate`),
the v2 environment refresh (`openSVCUpdateAppEnvironmentV2`, which renders the whole
definition and takes `container#app` from it) and the template preview of the API. The state
is read from the app each monitoring tick (`App.GetMonitoringStatus`), so a deleted app takes
it with it. A value fixed without a new rendering keeps the state until the next provisioning
or refresh, which is what the message asks for.

The operator fixes the value named (app settings or the template) and provisions again.
What can be met in stored apps:

| Value | Before | Now |
|---|---|---|
| Decimal cores (`0.5`, `1.5`) | rendered `--cpus=1.5.0` (refused by docker) | accepted, `--cpus=1.5` |
| Cores or memory with anything but a number | reached `run_args` | refused (APPERR012) |
| Path, volume directory or mapping with a space or `:` | reached the mounts | refused |
| Image with a space or a leading `-` | reached the run line | refused |
| Git branch or repository with shell or git syntax, ssh repository | reached the clone script / never cloned | refused |
| S3 endpoint, bucket or region with a space | word-split by the sidecar | refused |

## Known limits

- Under the sandbox the reload task needs the om3 `task.host` `container` keyword (present
  in v3.0.0-rc56). An agent without it would only warn and run the command on the node,
  where the guard refuses it: the reload fails, nothing runs.
- The jobs sidecar reads the service's cgroup slice through a read-only bind mount; reading
  cgroup files through the gVisor gofer is to be checked on a node (engine apps only, not
  tested yet).
- The S3 mount sidecar's environment change is unit-tested, not run on a node.
- The live resize of a docker-capped app (`prov-db-docker-run-args-limit`) is a reprovision,
  unchanged; the om3 slice cap (`pg_*`) applies to the sandbox like to a runc container.

## Tests

`cluster/app_sandbox_test.go`: resolution of the setting, the runtime on the client-image
sections only (app, jobs sidecar) and absent from the pause, sensor and init containers,
the reload task run in `container#app` under the sandbox (shared on a failover app) and a
docker task in the app's PID namespace when off, the guard run by a real shell outside a
sandbox (refused, the reload command not run), an invalid value refused (with and without a
reload task), decimal cores, each injection path above refused while any image and command
stay accepted, the git clone values refused (`$(id)`, backticks, `"`, `;`, a line break,
`--upload-pack=`, other schemes) and the clone command run by a real shell with hostile
values (git gets them as literal arguments, nothing else runs), the S3 sidecar's own keys,
and APPERR012 set on a refusal and cleared by the next rendering.
`cluster/app_sandbox_state_test.go`: WARN0328 open on a Cloud18 instance with apps and
`none` or empty, closed for `auto`, the sandbox or another runtime, without apps, off Cloud18,
and for an invalid value.

On a node (dev instance, om3 v3.0.0-rc56, three nodes with `runsc-hostnet`): a PHP app
provisioned under the sandbox on three nodes, a live resize reload inside the sandbox, two
restart rounds, the refused injection values, and the off-switch (`none`) giving the node's
default runtime back.
