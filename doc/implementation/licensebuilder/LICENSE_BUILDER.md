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
- The seat's licence plugin, `plugin-licence`: `make plugin-licence LICENCE_URI=<uri>` in the
  builder's own source, the URI `<company>.<expiry YYYYMM>.<seat>` set at build time so the
  signature covers it. It must speak the release's log plugin wire, or the build fails.
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

One request at a time; queue bounded to 64, history to the last 50 results (F4). Only air-gapped
requests are taken for now: the delivery to a registered client's `-pull` repo is not built.

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
| `LICENSE_BUILDER_ISSUER_URL` | the Cloud18 realm |
| `LICENSE_BUILDER_ALLOWED_CLIENTS` | `cloud18-crm` |

The monitor beside it must not pull a `-pull` repo (`git-url-pull` empty): `license-pull` refuses
then.
