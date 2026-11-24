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
command = /bin/sh -c 'eval "$APP_RELOAD_COMMAND"'
```

om3's `task.host` `container` keyword runs the command through the container's runtime (an
exec into the sandbox) with the task's environment read at run time, the keys of that
moment: nothing runs on the node, nothing outside the sandbox. Checked on a node (om3
v3.0.0-rc56): the PHP reload re-renders the sizing files and restarts the php-fpm pool
inside the gVisor app. The keyword needs an om3 agent that has it (rc56 has it); off the
sandbox the docker task is unchanged.

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

A node without it fails to start the app (`unknown or invalid runtime name`): the
orchestrator reports the start failure, nothing is silently started without the sandbox.

## What the client's values can no longer reach

The sandbox is only worth the definition around it. `validateAppDeploymentForService` runs
before anything is rendered (and before the orchestrator is asked for its pools) and refuses,
with an error naming the value:

- an image that is not one image reference (`ValidateDockerImage`, the character set the
  xtrabackup image already used, no leading `-`): the image is the argument after the run
  options, one starting with `-` would be read as an option;
- app cores that are not a whole number: they are written as `--cpus=` into `run_args`
  (also refused by the app setting `prov-app-cpu-cores`);
- a volume whose name is not an object name (`ValidateVolumeName`), a volume directory with a
  `..`, a space or a `:`, a volume user, group or permission that is not one;
- a path mapping whose source or container path carries a space, a `:` or a `..`, or whose
  container path is not absolute (`ValidateMountPath`, also enforced in
  `PathMapping.SafeDockerMapping`): a space would start a second mount (any node path), a `:`
  would add mount options;
- a git clone whose volume is not a volume or whose directory leaves the quotes of the clone
  command; the branch and repository, read from the environment, are now quoted;
- an S3 mount uid/gid that is not numeric (they are arguments of the privileged mount
  sidecar), and dynamic loader variables (`LD_*`) in an app with an S3 mount, whose
  privileged sidecar shares the app's environment.

`validateAppServiceSections` then refuses a rendered key or value with a line break, which
would start a new keyword in the orchestrator's INI configuration. The app setting
`prov-app-docker-cmd` refuses one too.

The command stays free: it is the container's arguments, after the image.

## Known limits

- Under the sandbox the reload task needs the om3 `task.host` `container` keyword (present
  in v3.0.0-rc56); an agent without it refuses the keyword.
- The jobs sidecar reads the service's cgroup slice through a read-only bind mount; reading
  cgroup files through the gVisor gofer is to be checked on a node (engine apps only).
- The live resize of a docker-capped app (`prov-db-docker-run-args-limit`) is a reprovision,
  unchanged; the om3 slice cap (`pg_*`) applies to the sandbox like to a runc container.

## Tests

`cluster/app_sandbox_test.go`: resolution of the setting, the runtime on the client-image
sections only (app, jobs sidecar) and absent from the pause, sensor and init containers,
the reload task run in `container#app` under the sandbox and a docker task in the app's PID
namespace when off, absent when off, an invalid value refused, and each injection path above refused
while any image and command stay accepted.
