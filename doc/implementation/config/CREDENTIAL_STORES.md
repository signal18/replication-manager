# Credential stores: one interface for config, local store and Vault (design)

Status: step 1 (`secret_kv-config`, the chain) implemented in `config/credstore`; versioned
stores (#2227) designed and closed 2026-10-28: rules 1-12, 15, 16 implemented with the API,
the GUI and the Cloud18 vault offer; rule 14 (the vault's policies) and the offer's catalogue
file are the back-office's, to do; rule 13 is out of #2227 (branch
`feat/vault-secrets-copy-2227`, not merged):
see "Implementation" below. Issues: #2017 (cause), #2041 (local store wins at start), #2057
(Vault settings and setter), #2227 (versioned stores). Related laws: T2 (one way to do a
thing), T25 (tracked files written only by the save callback).

## Why

**#2017.** A credential set at runtime is reverted by the next database or proxy tag
added or dropped, then saved, then loaded at the next restart (preprod belair-mcs:
ErrorAuth after the restart). Reproduced on develop by
`TestTagChangeKeepsRuntimeCredentials` (branch `fix/2017-tag-reverts-credentials`).

**The cause is not the tag functions.** Rereading the config must be safe from anywhere,
at any time. The cause is the writers:

- every setter (API settings, global settings, register, rotation, event replay, secret
  store restore: about 40 `Conf.Secrets.Set` calls) writes the new value into
  `Conf.Secrets`, the in-memory copy, and never into its source;
- `DecryptSecretsFromConfig` rebuilds `Conf.Secrets` from `DynamicFlagMap`, then
  `ImmuableFlagMap`, then `DefaultFlagMap`. `DynamicFlagMap` is filled only at load, by
  `inject.toml` and by the peer event replay;
- so any reread (`SetClusterCredentialsFromConfig`, called by the four tag functions and at
  init) brings back the value loaded at start, and the next save writes it.

Today the place a credential lives is spread over the config maps, the secret version
store and Vault, with a different code path for each, and nothing guarantees they agree.
This design puts them behind one interface, with one read rule and one write rule.

## The interface

```go
// A place a credential lives. Repman reads and writes credentials only through it.
type CredentialStore interface {
    Name() string                                    // the credential's source, shown in the GUI
    Get(key string) (value string, ok bool, err error)
    Set(key, value string) error                     // a new current value (a new version if versioned)
    CanSet(key string) error                         // nil, or why not (shown in the GUI)
    Versioned() bool                                 // keeps history: a rotation can be undone
    Generates() bool                                 // makes its own passwords (Vault database engine)
}
```

| Implementation | Plugin | Holds | Set | Versioned | Generates |
|---|---|---|---|---|---|
| `PinnedConfig` (`config.toml`, `cluster.d`: today `ImmuableFlagMap`) | `secret_kv-config`, internal | the pinned value, or a Vault path | refused: repman never writes it | no | no |
| `DynamicConfig` (`<cluster>.toml`, written by `SaveCallBack`: today `DynamicFlagMap`) | `secret_kv-config`, internal | the encrypted value, or a Vault path | yes, when `monitoring-save-config` is on | no | no |
| `LocalSecretStore` (today `SecretVersionStore`) | `secret_kv-localstore`, external | encrypted versions | yes, with an encryption key and `monitoring-secret-versioning` on | **yes** | no |
| `VaultKV` (`VaultConfigStoreV2`) | `secret_kv-vault`, external, **enterprise** | the value at a path | `Patch` at the path, as the rotation already does | **yes** (Vault versions) | no |
| `VaultDbEngine` (`VaultDbEngine`) | `secret_kv-vault`, external, **enterprise** | nothing repman sets | refused | yes | **yes** (`rotate-role`) |

## Versioned stores: every secret in every active store (#2227)

Design closed 2026-10-28. It replaces the single store of "the four chains" below for the
choice of where a credential lives; the interface, the chain's one write point and the
cache stay.

### The stores

Two plugins:

| Plugin | What it is | Versions |
|---|---|---|
| `secret_kv-config` | built in, always there: the config (the seed) plus the local versioned file `cluster.d/<cluster>_secret_store.json` | when `monitoring-secret-versioning` is on, bounded by `monitoring-secret-versioning-keep-last` |
| `secret_kv-vault` | n entries `[[<cluster>.secret-kv]]`, each on or off: address, mount, auth (`approle`, `jwt` with the instance's Cloud18 identity), mode (`config_store_v2`, `database_engine`) | always (KV v2); repman sets `max-versions` to the same keep-last |

The local versioned file is part of `secret_kv-config`: the separate `secret_kv-localstore`
plugin of the sections below is not built, and the enterprise tiers there are left aside for now.

The vault entries follow the same model as archives and S3 providers (`config/plugin_entry.go`): name, plugin,
settings (secrets encrypted, F9), on/off, forms drawn from the manifest.

### The rules

1. **The config keeps the starting value while a store keeps versions**: the first value,
   known at provisioning or typed by an operator (`root:pass1`). With an active store keeping
   versions, a rotation or a change never writes the config: the current value and its
   versions live only in the active stores. **When no active store keeps versions**
   (versioning off, no vault entry), **the config is the store** (#2269): a change or a
   rotation writes the current value, encrypted, in the dynamic config (`<cluster>.toml`),
   which the save callback writes and the config push carries like any setting (the DR and
   the peers get it from git); the version store keeps the current and the previous value in
   memory only (`configKeeps`, 2), and `Conf.Secrets.OldValue` the previous one, until the
   next change. It needs the encryption key (`ErrNoEncryptionKeyConfig`, F9) and
   `monitoring-save-config` on (else a change would be lost at restart: refused, as before).
   Turning versioning off hands the current version to the config at the next save
   (`keepCurrentCredentialInConfig`, called by `SaveCallBack`), so a restart without the
   file reads it. `monitoring-secret-versioning` is on by default. A version is kept
   encrypted only (F9: the file is in the config push): with versioning on and no encryption
   key, the version store refuses a change (`ErrNoEncryptionKey`, before any write) and adds
   no version from a config file or a vault read, never a cleartext value; the GUI shows the
   refusal on each credential. Versioning off, the file is not written and a vault entry
   keeps its values server side: no key is needed. Provisioning, a reprovision and the regtests
   read the current version, never the starting value; the GUI shows it as the initial
   value.
1b. **Every reload restores the last value**: a restart, a config reload, a tag change, a
   peer replay, `SetClusterCredentialsFromConfig` all go through `chain.Refresh()`, which
   applies the newest version of the active stores; the config's starting value only when
   no store holds one (before the first change). This is what keeps #2017 fixed.
   At a cluster's start only, before monitoring begins, a secret the local file does not
   version (versioning off, or no version yet) is read once from the enabled vault entries
   (`Cluster.ReadVaultsAtStart`: a database engine first, else the newest version), bounded
   by `secret-kv-start-timeout` (3 s): the value becomes the current one with its own time,
   no new version. Past the timeout or on a failure the cluster starts on what it has, the
   entry is behind (WARN0271) and the full pass catches up. Never on the monitor tick.
2. **Every write is a version in every active store.** The version is made in memory at the
   write, with its time and writing instance; the file is written only by the cluster's save
   callback after `cluster.Save()` (T25) and is in the config git push (encrypted values
   only, F9; versions change only at rotations and changes, T17). Each vault through a bounded queue (T18: the latest value per key wins, a hard cap,
   drops counted) and a worker with a timeout per write, after the chain's lock: it never
   blocks the write, a provisioning or the monitor (F2). Only the active monitor writes.
3. **Read**: the newest version wins across the stores (timestamp and writing instance,
   the event log's LWW rule); the seed last. A store behind the others is a tracked state
   (WARN0271, security domain, per entry) and gets the winning version at the next pass.
4. **Full pass**: when a store is turned on, at start, and after queue drops (WARN0272).
   It runs in the worker, reads each store's current version and writes only when it
   differs: no version churn, no blocking read at load (the one exception is the bounded
   read at start of rule 1b).
5. **Paths are computed by repman, never typed**:
   ```
   <mount>/<path-prefix>/<cluster>/
       databases/<server name>/<key>
       proxies/<proxy name>/<key>
       apps/<app name>/<key>
       archives/<archive name>/<key>
   ```
   `path-prefix` is a setting of the vault entry, empty by default (then
   `<mount>/<cluster>/...`): the client lays its own Vault out with it; on Cloud18 the
   marketplace entry fills it with `<domain>/<subdomain>`. An active monitor and its standby
   share the entry, so they write the same folder. A secret the whole cluster shares (the database credential) is
   written under every server that uses it. Each key keeps the setting's name.
6. **What is a secret of the cluster**: the chain's credentials, the proxy passwords, the
   app secrets (generated password, secret variables, database user), the archive secrets.
   Instance secrets (git token, OAuth secret, the vault logins) stay instance-local and are
   never copied.
7. **Orchestrator objects are consumers, not stores**: the OpenSVC `sec` object and the
   Kubernetes Secret get the current version at provisioning and after each rotation, as
   today. Their writers report the values they set (`CopySecret`); nothing reads them back.
   The rotation writes `MYSQL_ROOT_PASSWORD` in the OpenSVC `env` secret through the v2 or
   v3 API the agent speaks (`CreateSecretKeyValue`). A failed write is WARN0275 (security):
   the active monitor writes the current password again every `secret-kv-retry-interval`,
   in the background, and the state closes once it is written (#2255).
8. **`database_engine`** is a vault store that generates: a rotation asks Vault
   (`rotate-role`); the value it returns is written as a new version to the other active
   stores; it is read from the engine first.
9. **Rotation** is allowed with a store keeping versions or with the config as the store
   (rule 1, #2269). A value set in a config file or on the command line (immutable,
   `ImmuableFlagMap`) is a starting value like the others: with a store keeping versions it
   is rotated and changed as versions, the newest version wins over it at a restart. When an
   operator edits it in the file, that edit is the current version at the next start: the
   local file keeps the value last seen in the config file (a version marked
   `from_config_file`, never pruned away) to tell the edit. With vault entries only
   (versioning off) nothing keeps that value and an edit is not told: the newest version in
   the vaults wins. **With no store keeping versions, an immutable credential is the one
   refusal** (`VersionStore.CanSet`, a held refusal): repman never writes the file and the
   config cannot hold a version over it. The other refusals of
   `canRotatePasswordsOutsideVault` are the means to keep the new value (the encryption key,
   `monitoring-save-config`); the conditions of the moment (a database down, replication
   stopped) refuse that run only.
10. **Turning a store off** stops its versions and leaves what it holds. Repman never
    deletes a secret; a removed object's secrets stay, a renamed one writes under its new
    name.
11. **Peers (active and standby)**: the file travels in the config git like
    `<cluster>.toml`; the peer reads it from there, so both see the same history. Vaults
    are shared and need nothing more.
12. **Migration, with the release (no toggle)**: the `vault-*` keys become the entry
    `vault-default`, `database_engine` included. A config value that is a Vault path is
    read once at load as the seed and written through the chain. A path pinned in a file
    is read at each start, as today, and a config state asks the operator to remove the
    pin. `SetSecretsToVault` (the old flat copy) is removed.
13. **repman's key**: OUT of #2227, a separate key-rotation issue: repman has no path that
    changes its encryption key today (it is read at start; `keygen --overwrite` replaces the
    file offline and re-encrypts nothing). The vaults encrypt their own data.
14. **Access**: an instance's vault policy writes its own clusters' spaces only; people
    read through the space policies (on the Cloud18 vault: the sponsor, the owner, the
    administrators).
15. **Never a value in a log** (F10); the log line names the store, path and key.
16. **Accepted race**: an API change made while a newer remote value is being applied may
    skip its vault writes; the next full pass repairs it.

### Regtest (T13)

MariaDB and MySQL in Docker: provision with `pass1`; rotate (`pass2`); reload the config;
add then drop a tag; restart repman. After each step: every server monitored with no auth
error, logged in with `pass2`; the config still holds `pass1`; the file holds both
versions. Variant with a Vault container and a vault entry on: the same, and the vault
holds `pass2` at `databases/<server>/db-servers-credential`.

Implemented as `testCredentialVersionsRotation` (in the default list, MariaDB and MySQL,
`monitoring-secret-versioning` on) and `testCredentialVersionsRotationVault` (run by name on
a cluster whose config enables a `[[<cluster>.secret-kv]]` on a Vault or OpenBao dev
container) in `regtest/test_credential_versions_rotation.go`. A repman restart is simulated
in-process as `testCredentialPersistTagReload` does: the config the server builds from the
files on disk, and the chain a start builds over it with the local file
(`Cluster.ChainAtStart`). A credential set in a config file is rotated as well (rule 9).

### Implementation (#2227)

| Piece | Where |
|---|---|
| entry list, manifests, migration of `vault-*` (database engine included) into `vault-default`, the flat Vault fields reset | `config/secret_kv.go` (`MigrateSecretKvs`, `SyncSecretKvFromConfig`) |
| helpers shared with archives and S3 providers | `config/plugin_entry.go` |
| the versions in memory (head of the cluster's chain: the config keeps the starting value; a change refused when no store keeps versions), the file's version format (`written_by` added) | `config/credstore/versions.go` (`VersionStore`), `cluster/secret_version_store.go` |
| the file written by the save callback only, staged in the config push | `Cluster.ReconcileSecretVersionStore` (called by `SaveCallBack`), `config/manager` `ClusterStagedJSONFiles` |
| the one Vault code: approle/jwt sign-in, KV v2 versions (`written-at`, `written-by` fields, `max-versions` at a secret's first write), database engine (`rotate-role`, `static-creds`) | `config/credstore/vault.go` (`VaultKV`) |
| bounded queue (latest version per secret, drops counted), `Version.Newer` (LWW) | `config/credstore/store_queue.go` |
| chain hook after its lock (`Chain.OnSet`) | `config/credstore/credstore.go` |
| layout, writes, worker, full pass, seed of bare paths, states, database engine rotation | `cluster/cluster_secret_kv.go` |
| fake Vault for the tests | `config/credstore/vaultfake` |

- **Settings** (TOML, T16): `[[<cluster>.secret-kv]]` with `name`, `plugin`, `enabled` and
  `address`, `mount`, `path-prefix`, `mode`, `auth`, `role-id`, `secret-id` (encrypted, left out without
  an encryption key); `secret-kv-queue-limit` (1000), `secret-kv-write-timeout` (10 s),
  `secret-kv-retry-interval` (60 s), `secret-kv-start-timeout` (3 s). An entry's `role-id` and `secret-id` are instance-local
  (never replayed), as `vault-role-id` and `vault-secret-id`.
- **Versioned secrets** (rule 1): the database credentials (`db-servers-credential`,
  `replication-credential`, `monitoring-write-heartbeat-credential`) and the proxy
  credentials. The archive secrets stay in their archive's entry, the other secrets
  (instance secrets, `backup-verification-credential`...) in the config as before; the file
  still records their history at each save.
- **The file** (`secret_kv-config`, `<cluster>/secret_store.json`): loaded at the cluster's
  start when versioning is on, written by the save callback only (T25), atomically, when a
  version changed; a standby merges the file the config pull brings (versions known by time
  and writer) and never writes it. A remote newer version is inserted with its own time and
  writer (no new version, rule 3).
- **Secret-store CLI tools** (`server/server_secret_store.go`): `secret-store-restore`
  restores a versioned secret as a new version of the file (offline: appended atomically,
  `AppendSecretVersionFile`; with `--apply-runtime`: through the running cluster's chain and
  its save), never into `cluster.d/<cluster>.toml`, so the config keeps its starting value;
  the other secrets go to the config file as before. `secret-store-prune` prunes a loaded
  cluster's version store (the save writes the file), offline the file as before.
  `secret-store-copy` is unchanged (an export copy).
- **Orchestrator and app writers** (rule 7): `reportOrchestratorSecret` after each
  successful OpenSVC `CreateSecretKeyValue*` and `k8sPatchSecretValues` (the root password
  under every database server, the shard proxy password under every shard proxy, an app's
  secret variables, git and S3 secrets under `apps/<app>`), and the app's generated password
  (`apps/<app>/app-random-password`). The instance's secrets are never copied.
- **API** (`server/api_secret_kv.go`, T6): `GET /api/clusters/{cluster}/secret-kv-list`
  (entries with `secretsSet` only, per-entry state: last write and behind/WARN0271, the
  queue's drops/WARN0272, the manifests, the copy settings, the credentials' initial source,
  current source, version count and last change); `POST .../secret-kv-actions/add`,
  `edit/{entry}`, `enable/{entry}`, `disable/{entry}`, `remove/{entry}` (409 when a config
  file sets `secret-kv`). ACL: `cluster-settings` for all; the list, enable and disable also
  `db-config-flag`, the grant a cluster's sponsor holds by default, so the sponsor turns an
  entry on or off. `secret-kv-queue-limit`, `-write-timeout`, `-retry-interval`,
  `-start-timeout` through the settings setter. No MCP tool: the archive entries have none.
- **GUI** (`share/dashboard_react`, Database Credentials): `SecretKvEntries.jsx` (entries,
  switch, state pill, form from the manifest with the archives' `SettingField`), each
  credential's current and initial source, version count and last change.
- **Cloud18 vault offer**: the catalogue file `.pull/marketplace-vault.json`, a JSON array
  the back-office pushes like `marketplace-archive.json`:
  ```json
  [{"name": "vault-fr", "title": "Cloud18 vault", "partner": "signal18",
    "address": "https://vault.example:8200", "mount": "kv", "role": "",
    "pathPrefix": "", "dc": "", "dcLocation": ""}]
  ```
  `mount` defaults to `kv`, `role` to the realm's client id, `pathPrefix` to the instance's
  URI `<domain>/<sub-domain>-<zone>` (as the buckets). Each Cloud18-registered cluster gets
  `cloud18-<name>`, sign-in `jwt`, **off**: the sponsor turns it on; a new catalogue updates
  its address, mount, role and prefix, never its switch. The entry records its origin
  (`offer = "<name>"` in the saved config); the list API gives `offer` (name, title, partner
  from the catalogue; null for the user's own entries); its edit and remove are refused (409:
  the back-office manages its settings), only enable and disable remain; a user's own entry
  cannot take a `cloud18-` name. The GUI shows a Cloud18 badge with the offer's title and
  partner and no Edit or Remove. The back-office side (the file, the
  vault's jwt role and the space policies of rule 14) is not built yet.
- **States**: WARN0271 (security, per entry: behind, or failing), WARN0272 (security: drops
  not repaired), WARN0273 (config, per key: a Vault path pinned in a file), WARN0274
  (security: a scheduled rotation skipped, #2269), WARN0275 (security: the OpenSVC env secret
  keeps an old `MYSQL_ROOT_PASSWORD`, rule 7).
- **Writers to wire later** call `Cluster.CopySecret(kind, object, key, value)`.

### The Cloud18 vault entry

On a Cloud18-registered instance the marketplace catalogue offers a `secret_kv-vault` entry
for the Cloud18 vault, as it offers Cloud18 storages for archives: its address comes with
the registration, it signs in with the instance's Cloud18 identity (`jwt`, #2159), no
setting to type. The sponsor turns it on per cluster.

## Packaging: the `secret_kv` plugin family

The stores are delivered as `secret_kv` plugins, the same model as the log plugins
(`doc/implementation/cluster/LOG_PLUGINS.md`): built-in plugins compiled into the binary,
external plugins as signed executables.

| Plugin | Kind | Tier | Stores |
|---|---|---|---|
| `secret_kv-config` | **internal**: compiled into every binary, always there | free | `PinnedConfig`, `DynamicConfig` |
| `secret_kv-localstore` | **external**: executable in `<ShareDir>/plugins/` | free | `LocalSecretStore` |
| `secret_kv-vault` | **external** | **enterprise** (license) | `VaultKV`, `VaultDbEngine` |

- **The interface is the plugin contract.** The internal plugin implements
  `CredentialStore` in Go. An external plugin implements the same operations (`get`,
  `set`, `can-set`, `capabilities`) over a wire protocol; repman wraps it in a
  `CredentialStore`, so the chain does not know which kind it calls.
- **The config plugin is always there**, so every repman, without any external plugin,
  runs the "No store" rows below. An external plugin missing, unsigned, unlicensed or not
  answering leaves the chain with the config only: change and rotation fall back to
  today's refusals, and a state says which plugin is missing and why.
- **Wire**: JSON over stdin/stdout like the log plugins, but one long-lived child process
  per store, one request and one answer per line, each with a timeout. Secrets only cross
  the pipe: never in arguments, environment or logs (the wire carries a `secret` type the
  logger masks).
- **Signature**: an external `secret_kv` plugin sees every password, so it is loaded only
  with a valid signature (`plugin-signing-public-key`, the `.sig` embedded in the package
  or image, never delivered apart from it). An unsigned one is refused even when
  signature checking is off for log plugins.
- **What crosses the pipe**:
  - `localstore` receives values already encrypted by repman (the encryption key never
    leaves repman) and stores ciphertext versions only;
  - `vault` receives the plain value (Vault encrypts it) and its Vault login (address,
    AppRole or token) at start.
- **Never blocks the monitor** (F2): reads happen at `chain.Refresh()`, not per tick, and a
  plugin timeout keeps the last cached value and raises a state; a write that times out is
  refused, never retried blindly.
- **Code moves**: the Vault code (`cluster/cluster_get.go` `GetVault*`, the KV and
  database engine rotation in `cluster/cluster_sec.go`) moves into `secret_kv-vault`; the
  `SecretVersionStore` moves into `secret_kv-localstore`.

## The chain

Each cluster builds an ordered chain of stores from its settings; the config always comes
last, because it is the starting value.

- **Read**: the first store of the chain that has the key wins. A config entry holding a
  Vault path is resolved by the Vault store.
- **Write**: the first store of the chain that accepts it (`CanSet == nil`), then the cache
  is refreshed. One write point: `chain.Set(key, value)`. No other code writes a secret.
- **`Conf.Secrets` is only a cache** of what the chain reads. `chain.Refresh()` rebuilds it
  and is safe to call from anywhere, at any time: every write went into a store, so a
  reread, a reload or a tag change finds the same value. This removes the cause of #2017.
- **Rotation** is allowed when a store takes the write and keeps it across a restart: a
  store keeping versions, the config as the store (#2269), or a store that `Generates`
  (that store rotates). An immutable credential needs a store keeping versions (rule 9).
- **A pin is a starting value.** A store records the pinned value it last saw. When the
  operator edits that value in the file, the file wins and its value goes into the store
  as a new version: the operator's intent beats an older stored value. Done by
  `VersionStore.TakeConfigFileValue` at a cluster's start (`takeConfigFileCredentials`,
  before the chain is read), versioning on.

## The four cases are four chains

| Chain | Read | Change | Rotation |
|---|---|---|---|
| **Vault**: `VaultKV` or `VaultDbEngine` → `DynamicConfig` → `PinnedConfig` | from Vault, at the path the config gives, pinned or not | KV: a new Vault version. Database engine: refused | KV: repman writes a new version. Database engine: Vault rotates |
| **Local**: `LocalSecretStore` → `DynamicConfig` → `PinnedConfig` | the store's latest version, else the config | a new version in the store, pinned or not | allowed (versioned) |
| **No store, not pinned**: `DynamicConfig` → `PinnedConfig` | `<cluster>.toml` | the config is the store (#2269): the current value in `<cluster>.toml`, the previous one in memory | allowed |
| **No store, pinned**: `PinnedConfig` | the file | refused: repman never writes the file | refused |

So:

- whether a value is **pinned** only matters when there is no store;
- **Vault or local** only decides who keeps the versions and who makes the passwords;
- a Vault credential is never written to repman's files: they hold the path only.

`CanSetCredential` and `canRotatePasswordsOutsideVault` (`cluster/cluster_sec.go`, #2042,
#2048) ask the chain.

## Scheduled rotation (#2269)

| Setting | Default | |
|---|---|---|
| `scheduler-rotate-passwords` | `false` | the switch (T14); on a client's cluster only with the client's agreement (F1) |
| `scheduler-rotate-passwords-cron` | `0 0 3 * * 0` | once a week, Sunday 03:00 |

The job (`rotatepasswords`, `GetRotatePasswordsFunction` in `cluster/cluster_scheduler.go`)
runs on the active monitor only and calls `ScheduledPasswordRotation`, which calls the same
`RotatePasswords` as the button. A run `CanRotatePasswords` refuses is skipped: one line in
the security log, and WARN0274 (security domain, re-added at each tick by
`CheckScheduledPasswordRotation`) until a scheduled rotation succeeds or the schedule is
switched off. A successful run writes one line in the security log too. API:
`settings/actions/switch/scheduler-rotate-passwords` and
`settings/actions/set-cron/scheduler-rotate-passwords-cron/{value}`, under the cluster
settings grant like the other scheduler settings; GUI: Settings > Scheduler, Rotate Passwords.

Old backups: `backupPasswordHistory` (`srv_job_backup_encryption.go`) reads the old root
passwords from the local file to open an encrypted backup taken before a rotation.
`backup-encryption` turns versioning on (`IsMonitoringSecretVersioningEnabled`), so a cluster
whose backups are encrypted with the root password always keeps the file: the config is
never its store, and every password that encrypted a backup stays in the history.

Regtest (T13): `testCredentialConfigRotationScheduled` (MariaDB and MySQL, in the default
list; versioning off and the scheduler on for the test): the schedule on with a cron of every
minute rotates the database credential; every server logs in with the new password, the
previous one is kept in memory, a config reload and a start from the files on disk run on the
new one, and `<cluster>.toml` holds it encrypted.

## What changes in code

1. **Setters**: the ~40 `Conf.Secrets.Set` calls become `chain.Set(key, value)`, with no
   exception (API settings, global settings, register, rotation, event replay, secret
   store restore, app random passwords).
2. **Refresh**: `DecryptSecretsFromConfig` and `DecryptSecretsFromVault` become one
   `chain.Refresh()`. `SetClusterCredentialsFromConfig` and the tag functions keep calling
   it, unchanged.
3. **Refusals**: `CanSetCredential` and `CanRotatePasswords` come from `chain.CanSet` and
   `Versioned` / `Generates`. The Database Credentials GUI section shows `Name()` as the
   source and `CanSet` as the refusal (today `CredentialInfo.Source` / `Refusal`).
4. **Vault setter** (#2057): in Vault mode a change is written to Vault (KV) or refused
   (database engine). Today the API setter replaces the path with the value.
5. **Start order** (#2041): the local store is read after the config, so its latest
   version wins over a pinned value; an edited pin becomes a new version.
6. **Saving**: `DynamicConfig.Set` changes the in-memory map and calls `cluster.Save()`;
   the file is written by `SaveCallBack` only (T25).
7. **Concurrency**: `chain.Set` updates its store and the cache under one lock; the maps
   behind `DynamicConfig` get that lock too (two API calls setting a secret at once must
   not write a map concurrently: a fatal error stops the monitor, F2).

## Plan

One epic issue for the interface, with these steps:

1. the interface, the internal `secret_kv-config` plugin (`PinnedConfig`, `DynamicConfig`),
   the chain and the cache; every setter on `chain.Set`: fixes #2017 (its test must
   pass), plus a set → tag → save → reload test and a `-race` test of concurrent setters;
2. the external plugin wire and its loader (signature, timeouts, state when missing);
3. the local versioned file inside `secret_kv-config` (no separate plugin), and the start order (#2041);
4. `secret_kv-vault` (enterprise) from the Vault code, and the Vault setter (#2057);
5. versioned stores: every secret in every active store, vault entries, the Cloud18 vault entry (#2227).

## Open questions

- **Proxy and other credentials** (`proxysql-password`, `haproxy-password`,
  `mail-smtp-password`, `backup-verification-credential`…): the same chain from step 1,
  or database and replication credentials first?
- **Vault write permission**: repman logs into Vault with its own identity (AppRole or
  token), and most sites give it a policy that can only *read* the credential paths. A
  change makes repman *write* a new version there. Proposed: the vault plugin asks Vault
  what repman may do on each path (`sys/capabilities-self`) at start and at each refresh;
  when writing is not allowed, Change and Rotate are greyed with the reason ("repman's
  Vault policy is read-only on secret/db1") and a state says so, before anyone clicks.
  The alternative is to find out only when a change is refused. Either way a rotation is
  safe: it writes Vault first and keeps the old password when Vault refuses.
- **Vault moving to enterprise**: Vault support is in the community binary today. A site
  using it without a license loses it at the upgrade: keep Vault *reads* in the free tier
  (only change and rotation enterprise), a grace period, or enterprise from a given
  release?
