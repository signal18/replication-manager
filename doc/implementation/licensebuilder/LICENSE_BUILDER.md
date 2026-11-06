# licence-builder (#2309)

`replication-manager-pro license-builder` builds, for one seat of a client under support, the
licence package its replication-manager loads with `license-pull` (see
`doc/implementation/server/LICENSE_PULL.md`), and tests it on the monitor it runs beside before
it is handed over. It runs on the dev image, beside a regular monitor.

## The package of a seat

- The plugins of the client's replication-manager release: `make plugins` in a clone of that
  release (`git clone --depth 1 --branch <version>`, kept per version), signed by the Makefile with
  the Signal18 plugin key (`plugin-keys` fetches it from the signer repo with
  `PLUGIN_SIGNER_USER` / `PLUGIN_SIGNER_TOKEN`).
- The seat's licence plugin, `plugin-licence`: `make plugin-licence LICENCE_URI=<uri>
  [LICENCE_EXPIRY=<end>]` in the builder's own source, set at build time so the signature covers it.
  Two forms: an **air-gapped** seat is `<company>.<expiry YYYYMM>.<seat>` (expiry in the URI); a
  **registered (online)** instance is its full identity URI `<domain>.<subdomain>.<zone>` with the
  expiry — the contract end — carried separately in `LICENCE_EXPIRY` (`YYYY-MM-DD` or `YYYYMM`),
  since the full URI has no `YYYYMM` slot. It must speak the release's log plugin wire, or the build
  fails.
- The manifest: company, URI, wire, platform, build time (two builds of one seat are two packages
  for `license-pull`'s checksum).
- No public key: every release ships `share/plugins/plugin-signing.pub`, so a package cannot bring
  a key of its own.

## One request

1. Clone the release (once per version), `make plugins` for the platform.
2. `make plugin-licence` with the seat's URI; the wire of both trees must match.
3. Write `<uri>.tar.gz` to the packages dir.
4. Remove every plugin built, in both trees, and what a previous test left on the monitor
   (`<datadir>/.pull/plugins`, `<datadir>/plugins`): the test loads the package's plugins only.
5. Copy the package into the monitor's datadir and call `license-pull` on the monitor's API, as the
   GUI button does. Every plugin of the package must be loaded; an `unchanged` answer is a failure.

One request at a time; queue bounded to 64, history to the last 50 results (F4).

## Air-gapped vs online

`airGapped` is really "the client has no registered URI" (repman present, not internet-connected).
The modes differ in identity, expiry and delivery:

- **Air-gapped** (`airGapped:true`): URI `<company>.<YYYYMM>.<seat>`, expiry in the URI. Steps 1–5
  above build the full `<uri>.tar.gz` (release plugins + licence plugin), test it with `license-pull`,
  and `GET /packages/<uri>.tar.gz` hands it to the admin to install by hand.
- **Online** (`airGapped:false`): URI is the instance's full `<domain>.<subdomain>.<zone>`, expiry
  passed separately (`LICENCE_EXPIRY`, the contract end). Only the `plugin-licence` is built (no
  release plugins, no `license-pull` self-test — that exercises the air-gapped install path); the
  signed binary **and its `.sig`** are pushed into the instance's `-pull` repo
  `<domain>/<subdomain>-<zone>-pull` under `plugins/`, the same directory the enterprise plugins
  live in. repman's next pull loads and verifies it against the embedded public key like any other
  plugin (`VerifyPluginSignature` reads `<name>.sig`), so a version upgrade that changes the wire
  gets a freshly-built, matching licence — the registered client never loses the licence on upgrade.
  The git token rides an `Authorization` header, never the remote URL or any error (F10); unchanged
  content (same wire/expiry already there) is a no-op.

## API

Every call needs a Cloud18 realm token (signature, issuer, expiry) from an allowed client (`azp`,
default `cloud18-crm`) of a signal18-admin (`groups` `/signal18-admins` or realm role
`signal18-admin`), the rule the CRM applies.

- `POST /queue` `{company, uri, seat, expiry, airGapped, version, platform}`: 202, 400 when invalid
  or the queue is full.
- `GET /queue`: `{pending, current, results}`, each result with its state (`queued`, `building`,
  `testing`, `done`, `failed`), error, package and the plugins loaded.
- `GET /packages/<uri>.tar.gz`: the built package, for the admin to hand to the client.

## Environment

| Variable | Default |
|---|---|
| `LICENSE_BUILDER_ADDR` | `:10010` |
| `LICENSE_BUILDER_REPO_URL` | `https://github.com/signal18/replication-manager.git` |
| `LICENSE_BUILDER_SOURCE_DIR` | `/go/src/github.com/signal18/replication-manager` |
| `LICENSE_BUILDER_WORK_DIR` / `_OUT_DIR` | `/var/lib/license-builder/work` / `/packages` |
| `LICENSE_BUILDER_DATADIR` | `/var/lib/replication-manager` |
| `LICENSE_BUILDER_MONITOR_URL` | `https://127.0.0.1:10005` (loopback only) |
| `LICENSE_BUILDER_MONITOR_USER` / `_PASSWORD` | `admin` / required |
| `PLUGIN_SIGNER_USER` / `PLUGIN_SIGNER_TOKEN` | required |
| `LICENSE_BUILDER_GIT_BASE_URL` | `https://gitlab.signal18.io` (the `-pull` repos' GitLab) |
| `LICENSE_BUILDER_GIT_USER` / `_GIT_TOKEN` | `oauth2` / required for online `-pull` delivery |
| `LICENSE_BUILDER_ISSUER_URL` | the Cloud18 realm |
| `LICENSE_BUILDER_ALLOWED_CLIENTS` | `cloud18-crm` |

The monitor beside it must not pull a `-pull` repo (`git-url-pull` empty): `license-pull` refuses
then.
