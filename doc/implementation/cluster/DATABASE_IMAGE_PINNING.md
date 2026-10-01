# Database image pinning (#1862, 2026-10-01)

## The rule

- The **configuration** holds what the operator asked for: a pin (`mariadb:11.8.9`), a line
  (`mariadb:11.8`) or a pointer (`mariadb:latest`, `mariadb:lts`). Setting: `prov-db-image`
  (flag `prov-db-docker-img`).
- The **service definition** (OpenSVC `env.docker_image`, Kubernetes container image) always
  carries the **explicit release** that request resolved to at provision or at the last
  rolling upgrade, never a pointer. Record: `prov-db-docker-img-resolved` =
  `"<declared>=<explicit>"` (`mariadb:latest=mariadb:13.0.2`), written by replication-manager,
  persisted in the cluster dynamic toml, dropped when the declaration changes
  (`SetProvDBImage`).
- A **restart** recreates the container from that release (`rm = true`, om3 pull policy
  `once`: the node cache is used, a missing tag is pulled once). The release cannot move.
- An **upgrade** is the only path that moves it: `RollingUpgrade` resolves the request again
  (`ResolveDatabaseImage(force=true)`, stops when the registry cannot be reached), the pull
  phase pins the result in the definition (`env.docker_image` patched in place next to
  `image_pull_policy = always`, Kubernetes image in the Deployment patch), pulls, restarts.
  With an explicit pin in the configuration there is nothing to resolve: the upgrade is a
  restart on the same release.

## Why

Facts established on preprod (om3 rc40) and with a throwaway service on s18-fr-6:

- A floating tag resolves per node (`mariadb:11.8` was 11.8.9 on s18-fr-4 and 11.8.8 on
  s18-fr-6) and a pull by one service moves the node's tag for every service on it.
- A rewritten image name followed by a plain restart changes the image with no pull policy
  (local tag from the cache, absent tag pulled once). curepipe: the `update-opensvc-template`
  action of 2026-09-14 rendered the flag default `latest` into a service whose containers ran
  11.8.8; the 2026-10-01 rolling restart recreated them from the node cache's 19-month-old
  11.7.2 (#1861).

## Resolution (`utils/releases/registry.go`)

`ResolveTag(repo, tag)`: an explicit tag (`x.y.z`, `x.y.z-suffix`, a digest reference)
resolves to itself without a request. A pointer is resolved on Docker Hub: manifest digest of
the tag (`HEAD /v2/<repo>/manifests/<tag>`), then the Hub tag listing
(`/v2/repositories/<repo>/tags?ordering=last_updated`, five pages at most) gives the tags
sharing that digest; the plain `x.y.z` wins, else the shortest explicit one, else the tag
pinned by digest (`tag@sha256:…`). Another registry is not queried (`checked=false`): the
declared name is rendered as is, with a warning.

## Where it plugs in

| path | call |
|---|---|
| provision (OpenSVC, Kubernetes) | `ResolveDatabaseImage(false)` before the render; a valid record is kept |
| `update-opensvc-template` action (API) | `ResolveDatabaseImage(false)` before the render |
| `RollingUpgrade` | `ResolveDatabaseImage(true)`, abort on failure |
| MCP `cluster-rolling-upgrade` confirm | `SetProvDBImage` (refused when pinned immutable), `ResolveDatabaseImage(true)`, per-node push, `RollingUpgrade`; the plan reports `targetRelease` and `currentRelease` |
| every render | `cluster.deployImage()` = the record's explicit release, else the declared name |

Rolling restart: unchanged, it keeps the image the service runs (`DeployImageOverride`).

GUI: Configs > Orchestrator images shows "Service definition image".
