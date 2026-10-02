# App database auto-create (#1870)

An application deployed on a cluster asks the cluster for its own database. The
cluster is the MariaDB/MySQL DBaaS, so nothing external is involved: the schema,
the user and its grants are created on the primary at app provision, through
`dbhelper`, every statement in the SQL log.

## Template contract

Three substitution keys, resolved like every other `{{...}}` key by
`cluster.ParseAppTemplate` on the JSON from `GetAppsSubstitutionJSon`:

| key | value |
|---|---|
| `{{app.db.schema}}` | the schema (`app-db-schema`) |
| `{{app.db.user}}` | the user (`app-db-user`) |
| `{{app.db.password}}` | the password (`app-db-pass`), **stored form** (encrypted `hash_…`) |

The `db` object is present on the app only when it asked for a database, so a
template that references the keys on a plain app is refused at add with
`missing keys`. It is also present on every sibling, which is how dependent
processes share one database: `{{apps.#(name==erp-backend).db.user}}`. gjson
selectors take bare values, `name==erp-backend`, because the raw template is
TOML-parsed before the substitution runs (quotes would break the first parse).

The password is handed out in its encrypted form: a `type = "secret"` variable
decrypts it at render time (`GetDecryptedPassword`, the same path as any
secret), so the container gets the clear value in its environment. A
`prov-app-docker-cmd` must read it from that environment variable, never from
the placeholder.

## Settings

| setting | meaning |
|---|---|
| `app-db-auto-create` | the request (bool); set by the template keys, by `app-db-auto-create = true` in the template, or by the GUI/API |
| `app-db-schema`, `app-db-user` | default `appDbIdentifier(app name)`: lower-case, `[a-z0-9_]`, 32 chars max |
| `app-db-pass` | default generated (24 alphanumeric, DSN and shell safe), stored encrypted; setting it on an owned database rotates the user password at once (`RotateAppDatabasePassword`) |
| `app-db-owned` | the ownership mark, written by the provision that created the objects |

`ApplyAppDbDefaults` runs in `AddSeededApp` before the substitution (so the
keys resolve) and again after the template unmarshal (a template may set
`app-db-auto-create` itself), and in the `app-db-auto-create` setter.

## Provisioning and the security rule

`ProvisionAppDatabase` runs in `OpenSVCProvisionAppService` once the agent is
found, before the service is pushed; a failure aborts the provision through the
error channel. Steps, all on the primary:

1. observe: `GetSchemas`, `GetUsers`;
2. decide (`appDbProvisionDecision`, pure, unit-tested): **without the
   ownership mark an existing schema or user refuses the provision**, nothing
   is altered (Stéphane: never override an existing user). With the mark the
   provision is idempotent;
3. `CreateDatabaseIfNotExists` (the schema is marked owned from this point, so
   a later failure never turns the retry into a foreign-schema refusal),
   `CreateUser` for `%` (the configurator sets `skip_name_resolve=ON`, so a
   hostname-bound account can never log in, as the security monitor flags;
   the database is reachable from the cluster network only), `SetUserGrants`
   `ALL PRIVILEGES ON schema.*`; on an owned account that already exists the
   stored password is re-applied (a drop and re-add of the app generates a new
   one while the account keeps the old, seen live on curepipe);
4. mark `app-db-owned`, clear `App.DbProvisionError`.

A refusal is tracked, not logged only: `App.DbProvisionError` is turned into the
app state `APPERR008` every tick by `GetMonitoringStatus` (app_chk.go), the same
way as the gateway conflict, and resolves on the next successful provision.

Dropping the app never drops the schema or the user (monitoring first
principles: we never destroy client data).

## Surfaces

* API: the app settings route, `set/app-db-auto-create|app-db-schema|app-db-user|app-db-pass|app-db-owned`, `switch/app-db-auto-create|app-db-owned`; the app view carries `appDbAutoCreate`, `appDbSchema`, `appDbUser`, `appDbOwned` (`appDbPass` masked) and `dbProvisionError`.
* GUI: app Overview, rows Database (auto-create), Schema, User, Password, Owned.
* MCP: `app-add` and `list-cluster-apps` answer `db: {schema, user, owned, error}`.
* Templates (cloud18-templates): forgejo and the ERPNext set use the keys.

## Related: S3 mount owner

`S3Mount.Uid` / `Gid` (`uid`, `gid` in `s3-mounts`) set the `--uid/--gid` of
the mount sidecar, default 33 (www-data, the historical value); ERPNext runs
as 1000. The S3 provider credential lookup (`s3ProviderAppCredentials`,
server/api_app.go) accepts the MinIO, RustFS and AWS variable names.

Tests: `cluster/cluster_app_db_test.go`.
