# CI / Release pipeline — GitHub Actions

**Jenkins is retired.** The `Jenkinsfile` at the repo root is a leftover and is no longer
wired to any build server; everything it did (and more) now runs as GitHub Actions under
`.github/workflows/`. This doc is the map of what runs, when, and where artifacts land.

## Workflow map

| Workflow | Trigger | Produces |
|---|---|---|
| `build-packages.yml` | tag push `v*`, or manual dispatch with a `tag` input | DEB + RPM packages (amd64 + arm64), published to the apt/yum repos; **sole publisher of the signed log-plugin set** |
| `docker-build-push.yml` | push to `develop`, tag push `v*`, or manual dispatch | All Docker Hub images (release, nightly, dev, arb, slim + rootless variants) |
| `release-binaries.yml` | GitHub **release published**, or manual dispatch | Standalone linux amd64/arm64 binaries attached to the release (darwin/windows currently disabled) |
| `generate-sbom.yml` | GitHub release published | CycloneDX SBOM attached to the release |
| `update-changelog.yml` → `update-changelog-claude.yml` | GitHub release published | Claude-authored `CHANGELOG.md` entry committed to `develop` |
| `claude.yml` / `claude-code-review.yml` | PR opened, or `@claude` PR comment | Automated PR review |

Note the trigger split: **pushing a tag** builds packages and docker images; the
**release-published** event (creating the GitHub release from that tag) drives binaries,
SBOM, and changelog. A tag without a GitHub release ships packages/images but no release
assets.

## Package build & repo publication (`build-packages.yml`)

Runs on a **self-hosted linux x64 runner** (the packaging host — it holds the GPG key and
the repo trees):

1. Checkout (full history + submodules). Manual dispatch checks out the given `tag`
   (defaults to the latest tag).
2. `package_linux.sh` runs **twice, sequentially, in the same job**: amd64 then
   `architecture=arm64`. Each run does `make`, then builds `deb`+`rpm` for the four nfpm
   configs (`client`, `osc`, `prov`, `arbitrator`) into `build/release/`, moved to
   `/builds/<tag>/` on the runner.
3. **DEB repo publish**: `signal18/reprepro:latest` container, mounting `/root/.gnupg`,
   `/home/repo` (the served repo tree) and `/builds`, invoked with the tag **stripped of
   its leading `v`** (`${tag#v}`).
4. **RPM repo publish**: `/usr/local/bin/rpmrepo.sh` on the runner host.
5. Mattermost notification (success/cancelled/failure).

Both arch builds run with `PLUGIN_PUSH=ON` — this workflow is the **single owner** of
signed-plugin publication. Do not set `PLUGIN_PUSH` anywhere else; rationale and the
wire-version coupling are in [BUILD_PLUGIN_PUBLISHING.md](BUILD_PLUGIN_PUBLISHING.md).

### Version derivation — tags must be `v<digit>…`

`package_linux.sh` derives the package version as:

```sh
version=$(git describe --tag --abbrev=4 | sed 's/^v//')
```

The `sed` strips only a **lowercase** `v`. Debian versions must start with a digit, so a
mis-cased tag flows straight through nfpm into an invalid `.deb`:

> **Incident 2026-07-20:** tag `V3.1.35` (capital V) produced
> `replication-manager-osc_V3.1.35-1_amd64.deb`; dpkg rejected it on every client
> (`'Version' field value 'V3.1.35-1': version number does not start with digit`) and the
> published apt repo was poisoned until the bad packages were removed.

Rules:
- Release tags are always `v` + digits (`v3.1.35`). Never capital `V`, never a bare
  version.
- Recovery from a bad tag: delete/replace the tag with the correct lowercase form, remove
  the bad packages from the repo tree on the packaging host (reprepro remove + clean
  `/builds/<badtag>/`), then re-run `build-packages.yml` via manual dispatch with the
  corrected tag.

## Docker images (`docker-build-push.yml`)

One job per image; concurrency-grouped per ref, never cancelled. A `paths-filter` gate
(`detect-changes`) skips the branch-triggered (nightly/dev) jobs when no relevant files
changed; the arbitrator nightly has its own narrower filter.

**Tag-only jobs** (run on `v*` tags, or forced via dispatch):

| Image tags | Dockerfile |
|---|---|
| `{tag}`, `latest` | `docker/Dockerfile` (OSC) |
| `{tag}-rootless`, `latest-rootless` | `docker/Dockerfile.rootless` |
| `{tag}-pro` | `docker/Dockerfile.pro` |
| `{tag}-pro-rootless` | `docker/Dockerfile.pro_rootless` |
| `{tag}-slim` / `{tag}-slim-rootless` | slim variants |
| `{tag}-arb`, `arb` / `{tag}-arb-rootless`, `arb-rootless` | arbitrator |

**Branch jobs** (push to `develop`): `nightly`, `nightly-rootless`, `nightly-arb`, `dev`,
`dev-rootless` (dev images also get `{tag}-dev*` on tag builds).

Docker builds do **not** publish plugins (`PLUGIN_PUSH` unset) — nightly images therefore
depend on the last tag build having published a plugin set for the current wire version
(see the nightly-plugins gap discussion in BUILD_PLUGIN_PUBLISHING.md).

## Operational notes

- The packaging host state lives outside CI: `/home/repo` (served repo tree),
  `/builds/<tag>/` (raw artifacts), `/root/.gnupg` (signing key). Repo surgery
  (removing a bad package) happens there, not in a workflow.
- `build-packages.yml` uses a non-cancelling concurrency group — re-dispatching while a
  build runs queues, matching the old Jenkins `concurrentBuild: false`.
- The root `Jenkinsfile` and its Mattermost plumbing are dead code and can be removed once
  nobody references the old Jenkins job.
