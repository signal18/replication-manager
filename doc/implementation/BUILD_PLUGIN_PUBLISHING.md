# Log-plugin publishing — single owner, opt-in

Log-plugins are built by `make` (the `plugins` target, a dependency of `osc`/`pro`) and
distributed to servers at runtime through the signed-plugin store. This note defines **who
publishes to that store, and when** — the rule that keeps the store consistent with what
servers actually run.

## The rule

> **Publishing is opt-in and single-owner.** A build publishes plugins **only** when it is
> invoked with `PLUGIN_PUSH=ON`. Exactly one build sets it: the **tag-triggered package build**.
> Every other build (docker images, nightly, dev, osc, slim, …) leaves `PLUGIN_PUSH` unset and
> therefore **skips** publishing.

## Why the package build is the publisher

Plugins are platform-specific — one set per OS/arch, grouped by the plugin **wire protocol
version** (`WireVersion` in `cluster/logplugin/plugins/wire/wire.go`). The publisher must
therefore cover **every architecture we ship**, and must do so without two publishers racing.

The **package build** (`.github/workflows/build-packages.yml` → `package_linux.sh`) is the only
place that already satisfies both:
- it runs on **release tags**,
- it builds **every arch** — `amd64` then `arm64` — **sequentially, in one job on one runner**,
- it already holds the store credentials.

Because the arches build sequentially in a single job, each publishes its own arch one after the
other — **all arches covered, no concurrent push, no ref race.** The docker image builds are the
wrong place: the release image is single-arch, and making it multi-arch would run two publishers
concurrently.

## The gate in `make` (opt-in only)

The Makefile `plugins` target reduces to:

```
if PLUGIN_PUSH = ON  -> publish
else                 -> skip   (unset / anything-but-ON)
```

This is deliberately two-state. An earlier three-state version auto-published whenever signer
credentials happened to be present, which meant several unrelated build variants published
independently and inconsistently — the source of both the "who published what" confusion and the
concurrent-push races. Opt-in makes publishing an explicit, owned action.

## How it breaks (and the guard against it)

Publishing a plugin set is coupled to a value in the source tree: **`WireVersion`**. Bumping it
(a normal feature change) silently obsoletes every already-distributed plugin set, because
servers on the new wire version can't use the old sets. If the next release doesn't publish a set
for the new wire version, servers get **no plugins** — and it surfaces far downstream, not at the
commit.

**Guard:** a release-time check that fails the build when, for the current `WireVersion`, the
store has no published set for the shipped arches. That turns "silent, discovered weeks later in
the field" into "red release build." (Recommended follow-up; see the `plugins`/`plugin-push`
targets for the paths involved.)

## Adding an arch

Publishing follows wherever the package build compiles. To publish plugins for a new arch, add it
to the package build's per-arch sequence (`build-packages.yml`) — do **not** add `PLUGIN_PUSH=ON`
to any other workflow.
