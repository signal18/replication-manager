# Bootstrap fallback to the DR replication-manager (#1942)

## Problem
Every bootstrap fetches its script and its configuration from one replication-manager URL: the OpenSVC init container of a database or proxy, the on-premise SSH provisioning and its `bootstrap` scripts, and the Kubernetes init container. The URL (`REPLICATION_MANAGER_URL`) is the instance that provisioned the service, and only provisioning rewrites it. While that instance is down, during a DR takeover, a restart cannot fetch anything.

## Design (only with `arbitration-external = true`)
- **The peer's URL comes from the heartbeat.** `/api/heartbeat` answers `apiUrl` = `conf.MonitorAPIURL()` (`https://<monitoring-address>:<api-port>`), the same helper that builds `REPLICATION_MANAGER_URL`. Each instance keeps its peer's URL (`recordPeerAPIURL`) under these conditions:
  - it is a plain `https://host[:port]`, with no path, user or query, and no character a shell loop could split or expand;
  - its host is the host of the configured `arbitration-peer-hosts` entry that answered. Anything else answering the heartbeat cannot redirect the bootstraps.

  An answer from the instance itself (the default peer is 127.0.0.1) or without a URL (an older peer) changes nothing. The last known URL is kept while the peer is down, because that is when it matters.
- **Both URLs of the pair are offered as `REPLICATION_MANAGER_URL_DR`**, the instance's own first, then its peer's. Both are needed because `REPLICATION_MANAGER_URL` may point at the dead instance, whichever of the two provisioned.
- **Every bootstrap tries `REPLICATION_MANAGER_URL`, then each URL of the list not tried yet**, and keeps the one that answered for every later call.

| Orchestrator | Where the DR URLs go | Fallback |
|---|---|---|
| OpenSVC | Namespace config `env`, key `REPLICATION_MANAGER_URL_DR`. Written once per value, and mapped into the init container only after a successful write, because a mapped key that does not exist fails the start. | The init command (`bootstrapInitCommand`) and the `opensvc/bootstrap` login loop |
| On-premise | The SSH environment of databases, proxies and apps (`GetSshEnv`) and the proxy provisioning environment | `onPremiseBootstrapCommand` and the login loop of the 7 on-premise `bootstrap` scripts |
| Kubernetes | Baked into the init container command. The command is byte-identical without DR URLs, so no pod restart. | Picks the first URL answering the public `/api/version`, then uses it (`$B`) for need-config-fetch, the config and the CLI |

## Behaviour and limits
- **Timing:** busybox `wget -T 10` bounds each read. A dead main URL costs about 10 s per step: the script fetch, then the login. A blackholed address or a slow DNS failure can take longer within the init container's 30 s start timeout.
- **Disabling active/standby:** the key is no longer mapped. An existing `REPLICATION_MANAGER_URL_DR` key stays in the namespace config, unused.
- **After a replication-manager restart:** the key is written again once, because which value was written is kept in memory only.
- **No GUI setting:** the feature follows `arbitration-external`, and the peer's URL is learned, not configured. It shows up as the `REPLICATION_MANAGER_URL_DR` key in each namespace config, and the `apiUrl` field of `/api/heartbeat`.

## Tests
- `cluster/bootstrap_dr_test.go` covers the state transitions, the Kubernetes command, and the on-premise command and environment.
- `cluster/bootstrap_dr_shell_test.go` runs the generated commands and the script's login loop with `sh` against a fake `wget` (main down, DR up). It checks that DR is reached and each dead URL is tried once.
- `server/server_bootstrap_dr_test.go` covers URL validation, the recorded peer URL, the heartbeat answer and the pair.
