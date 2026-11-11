# CI / Release pipeline — GitHub Actions

**Jenkins is retired.** The `Jenkinsfile` at the repo root is a leftover and is no longer
wired to any build server; everything it did (and more) now runs as GitHub Actions under
`.github/workflows/`. This doc is the map of what runs, when, and where artifacts land.

## Workflow map

| Workflow | Trigger | Produces |
|---|---|---|
| `build-packages.yml` | tag push `v*`, or manual dispatch with a `tag` input | DEB + RPM packages (amd64 + arm64), published to the apt/yum repos; **sole publisher of the signed log-plugin set** |
| `docker-build-push.yml` | push to `develop`, tag push `v*`, or manual dispatch | All Docker Hub images (release, nightly, dev, arb, slim + rootless variants) |
| `columnstore-image.yml` | pull request or push to `develop` touching `docker/columnstore/**`, or manual dispatch with a `mariadb_version` input | `signal18/mariadb-columnstore:<version>` and `:<major.minor>` (#2002): the official `mariadb` image + ColumnStore + CMAPI, smoke-tested (plugin ACTIVE); a pull request builds and tests only, never pushes |
| `release-binaries.yml` | GitHub **release published**, or manual dispatch | Standalone linux amd64/arm64 binaries attached to the release (darwin/windows currently disabled) |
| `generate-sbom.yml` | GitHub release published; manual with a tag | CycloneDX SBOM attached to the release, with its `vulnerabilities`: published advisories + grype findings, govulncheck reachability (`.github/scripts/sbom_vulns.py`, #2086). Re-run by hand with the tag to refresh a published release |
| `update-changelog.yml` → `update-changelog-claude.yml` | GitHub release published | Claude-authored `CHANGELOG.md` entry committed to `develop` |
| `claude.yml` / `claude-code-review.yml` | PR opened, or `@claude` PR comment | Automated PR review |

Note the trigger split: **pushing a tag** builds packages and docker images; the
**release-published** event (creating the GitHub release from that tag) drives binaries,
SBOM, and changelog. A tag without a GitHub release ships packages/images but no release
assets.

## Package build & repo publication (`build-packages.yml`)

Two jobs on two self-hosted runners:

**`build`** (self-hosted linux x64, the build host):

1. Checkout (full history + submodules). Manual dispatch checks out the given `tag`
   (defaults to the latest tag).
2. `package_linux.sh` runs **twice, sequentially, in the same job**: amd64 then
   `architecture=arm64`. Each run does `make`, then builds `deb`+`rpm` for the four nfpm
   configs (`client`, `osc`, `prov`, `arbitrator`) into `build/release/`, moved to
   `/builds/<tag>/` on the runner.
3. The packages are uploaded as the artifact `packages-<tag>` (kept 3 days).

**`publish`** (runner label `repo-publish`: a runner inside the package repository
service, so it writes into the tree the repository serves; it holds the signing key):

4. Downloads the artifact.
5. **Publish DEB**: `publish-repo <tag> <dir> deb` imports the `.deb` files with reprepro
   into every distribution, one component per minor version (`3.1`, `3.0`…); the first
   release of a new minor adds its component.
6. **Publish RPM**: `publish-repo <tag> <dir> rpm` rebuilds the yum trees from every
   build kept by the repository (each minor in its own directory; the newest minor and the
   last builds of the previous one at the top level), then `createrepo_c`.

`publish-repo` is provided by the runner's environment, not by this repository. One step
per format, so a failed deb or rpm publish shows as its own failed step.

**`notify`**: Mattermost notification (success/cancelled/failure) from the results of
both jobs.

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
