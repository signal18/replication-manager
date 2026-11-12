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
   never copied. *Replaced 2026-11-11: see "Two levels, every store the same".*
7. *(The orchestrator becomes a store too, 2026-11-11: see "Two levels, every store the
   same". The `env` delivery below is unchanged.)*
   **Orchestrator objects are consumers, not stores**: the OpenSVC `sec` object and the
   Kubernetes Secret get the current version at provisioning and after each rotation, as
   today. Their writers report the values they set (`CopySecret`); nothing reads them back.
   The rotation writes `MYSQL_ROOT_PASSWORD` in the OpenSVC `env` secret through the v2 or
   v3 API the agent speaks (`CreateSecretKeyValue`). A failed write is WARN0275 (security):
   the active monitor writes the current password again every `secret-kv-retry-interval`,
   in the background, and the state closes once it is written (#2255). Each write is
   numbered: a write overlapped by a later one (a rotation during a retry) leaves the state
   open, since the agent may have kept the older value, and the retry writes the current
   one. The state lives in memory: after each start (a restart, a failover to this monitor)
   the active monitor reads the secret on a v3 agent, every `secret-kv-retry-interval` until
   the agent answers, and opens WARN0275 when it differs from the current password. A
   missing secret raises nothing (a cluster monitored, not provisioned, on OpenSVC has none); a v2 agent has no read of a key, so
   on v2 a state open at a restart is lost until the next rotation.
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

The app secret versions (#2306) are covered by `testAppSecretVersions` (in the default list,
`monitoring-secret-versioning` on) and `testAppSecretVersionsVault` (run by name on a cluster
whose config enables a KV `[[<cluster>.secret-kv]]`), in `regtest/test_app_secret_versions.go`.
Through repman's API, a monitor-only app (no plan reservation, so the test runs on any cluster)
gets a secret variable, a per-agent value of it, a new value, then the variable and the app are
dropped; after each save the local file holds one more version per changed secret under
`apps/<app>/<key>`, stored as one ciphertext, an unchanged value adds none, a dropped variable
or app keeps its versions, and with a vault entry each value is read back from
`apps/<app>/<key>` (`Cluster.SecretKvReadObject`). Versions persist across runs (rule 10): the
test counts from what the file holds when it starts.

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
  writer (no new version, rule 3). `ReconcileSecretVersionStore` is called by
  `SaveCallBack` only (#2041): a setter or a job marks the store dirty and asks a save. A
  reader of the history that needs the current secrets as versions at once (a backup
  recording the password version it used) calls `recordDirtySecretVersions`, which records
  them in the version store in memory and writes no file; the next save writes it. The
  history is read with `VersionStore.Versions`, which leaves the pending write alone
  (`Export` takes it).
- **Secret-store CLI tools** (`server/server_secret_store.go`): `secret-store-restore`
  restores a versioned secret as a new version of the file (offline: appended atomically,
  `AppendSecretVersionFile`; with `--apply-runtime`: through the running cluster's chain and
  its save), never into `cluster.d/<cluster>.toml`, so the config keeps its starting value;
  the other secrets go to the config file as before. `secret-store-prune` prunes a loaded
  cluster's version store (the save writes the file), offline the file as before.
  `secret-store-copy` is unchanged (an export copy). An app secret's versions
  (`apps/<app>/<key>`) are history: `--all` leaves them out, an explicit app key is refused.
- **Orchestrator and app writers** (rule 7): `reportOrchestratorSecret` after each
  successful OpenSVC `CreateSecretKeyValue*` and `k8sPatchSecretValues` (the root password
  under every database server, the shard proxy password under every shard proxy, an app's
  secret variables, git and S3 secrets under `apps/<app>`), and the app's generated password
  (`apps/<app>/app-random-password`). The instance's secrets are never copied. An app's
  secrets are reported with `Compare` (#2306): written only where an entry holds another
  value, so a provisioning that sets an unchanged value makes no vault version.
- **App secret versions** (rules 2, 6 and 10 for apps, #2306): the save callback runs
  `versionAppSecrets` before `ReconcileSecretVersionStore`. Each app secret whose value
  differs from its last version becomes a new version in the version store under
  `apps/<app name>/<key>`, written to the local file by the same save, and is queued to every
  active vault entry with `Compare` (written only where the entry holds another value, so a
  value provisioning already reported makes no new vault version). It covers every writer of
  an app (API add and modify, create and reset from a template, peer import, provisioning):
  the version is made at the save that follows the write, not at the write. This is the path
  the secrets outside the chain already take (`ReconcileSecretVersionStore` → `Record` at the
  save), without its dirty mark: every save compares the app secrets, so no writer needs a call
  and a writer added later cannot miss one. The secrets
  (`appSecretValues`): secret variables (`<name>`, a per-agent value `<name>@<agent>`), git
  clone passwords and S3 secret keys under the names provisioning writes, `app-random-password`
  and `app-db-pass`; never `SENSOR_API_KEY`. A value is encrypted whole (`GetEncryptedString`):
  an app secret is any string, and `EncryptSecretValue` would leave the part before a `:` in
  clear. Without the encryption key nothing is recorded; without the local file (versioning
  off) memory keeps the last two values of a key. The full pass queues the current app secrets
  too (`queueAppSecrets`), so an entry turned on later gets them. A removed app or secret keeps
  its versions.
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

### Orchestrator copy through the StoreWriter queue (#2356, follow-up)

Rule 7 keeps: the orchestrator is a **consumer, not a store**, nothing is read back as a Chain
source. What changes is the *write side*. Before #2356 the OpenSVC `sec` object and the
Kubernetes `Secret` were written by bespoke provisioning code with their own WARN0275 retry,
outside the queue that drives the Vault entries. #2356 makes them `credstore.StoreWriter`s:

- `openSVCStore` / `k8sStore` (`cluster/store_orchestrator*.go`) implement `WriteAll` and a
  `ReadCurrent` used only by the WARN0275 drift reconciliation (F5: the drift is observed,
  never trusted as a source).
- The worker (`secretWritePass`) dispatches a reserved, always-on queue entry `orchestrator`
  (not a user Vault entry; the sponsor cannot turn it off) to the sink chosen by
  `prov-orchestrator`. The orchestrator copy gets the same bounded/timeout/requeue/behind
  machinery (T18, WARN0271/0272) as the Vault entries.
- A rotation keeps a **one-deep `<KEY>_OLD` history** in the flat orchestrator secret (bounded,
  F4/T18): the superseded value, for the rotation transition window and a cheap rollback,
  mirroring how the config versions its starting value.
- The copied key set gains the **admin API password** (`REPLICATION_MANAGER_PASSWORD`), queued
  to the orchestrator sink only — never the Chain/Vault (rule 6). It is queued at
  **provisioning/restart** (`openSVCCreateMaps*`); the admin credential is config-driven with no
  runtime change site, so that is its currency — not a runtime re-queue.

**Timeouts: one budget per `WriteAll`, carried by our context down to the HTTP request.** Each
sink derives one context from the pass with the store timeout (`secret-kv-write-timeout`,
`orchestratorCallContext`) and hands it to every call: on OpenSVC the `...Ctx` collector calls
(`GetSecretKeyValueV3Ctx`, `CreateSecretKeyValueCtx`), on Kubernetes the `Get` and the `Patch`.
The read, the `<KEY>_OLD` write and the write of a key share that budget; once it is spent the
remaining items are requeued without calling the agent, so N items against a dead agent end in
about one timeout, not 3·N. Each request also keeps the collector's own cap
(`ContextTimeoutSecond`, 10 s; an unset value falls back to 10 s, never "expired at once").
Nothing is abandoned in a goroutine: an earlier version wrapped each call in one
(`withinTimeout`), which left the request running after the timeout, so a stale write could land
after a newer one. **Residual window:** cancelling the request closes it on our side, but an
agent that already received the whole body may still apply it. The item is requeued and the
next pass writes the newest value; the queue keeps the newer of a requeued and a pushed version
(`Requeue` keepNewer), so the store converges.

**No certificate, no call.** When the cluster's OpenSVC p12 cannot be loaded (WARN0099),
`openSVCStore.WriteAll` and `ReadCurrent` return `errOpenSVCCertNotLoaded` at once, requeuing
everything: the store-behind state names that cause instead of a TLS error after a timeout. The
certificate state itself is described in `doc/implementation/opensvc/CLIENT_REUSE.md`.

Staged (F8): the bespoke direct writes stay as the provisioning seed; removing them and folding
WARN0275 onto `ReadCurrent` land once the T13 regtest proves the queue path. K8s uses its auth
header (`REPMAN_AUTH_HEADER`), not `REPLICATION_MANAGER_PASSWORD`, so the admin copy is
OpenSVC-only.

### A user's password rotation (the ban of a compromised user)

`POST /api/clusters/{cluster}/users/rotate-password` `{"username"}` gives one API user a new
generated password (24 characters, `GeneratePasswordLength`), returned once, never logged. It is the
way to ban a compromised user: its sessions carry its password, so they are refused from the next
request, and every API token it owns is revoked (`revokeUserAPITokens`).

- **Who:** the rotation grant (`cluster-rotate-passwords`), held by sysops and extsysops; dbops,
  extdbops and sponsors do not hold it. Another user of the instance's list (a server secret) is
  rotated only by a holder of `cluster-server-credentials` on every cluster of the instance
  (`holdsServerCredentials`), the rule of the instance's name; a user may always rotate its own
  password. No role reads a password: the new one is shown to the one who rotated it, once.
- **No escalation:** the caller rotates its own password, or that of a user holding no grant the
  caller lacks (`CanRotateFor`), on every cluster concerned: the new password is handed to the
  caller, so rotating a user with more rights would hand those over.
- **One at a time:** rotations are serialized (each reads a list, changes one entry, writes it).
- **Half-way failure:** what was rotated stays rotated and the user's tokens are revoked; the
  answer names the clusters done. Undoing it would give the compromised password back.
- **Which list:** a user of the instance's list (`api-credentials`, read by every cluster) gets the
  new password on every cluster holding it, all checked before any change; a user of a cluster's
  own list (`api-credentials-external`) on that cluster. Every other user keeps its password.
- **Repman is the reference:** `api-credentials` is versioned (`isVersionedKey`): the change is a
  version in the local store, with no OpenSVC and no Vault needed. A value set in a configuration
  file is rotated as versions (rule 9); refused only with no store keeping versions. A value edited
  in the file is taken at the next start, and at the upgrade the file's value wins once over a
  history recorded before the list was versioned (`AdoptConfigFileValue`).
- **Refused:** `system` (its key is derived from the instance key), the registered Cloud18 user and
  accounts without a local password (they sign in with Cloud18: rotate at the identity provider).
- **On disk before the answer:** a change of a server secret writes the server's file at once
  (`writeServerSecretFile`, the save callback's part for that file run synchronously, as
  `SaveConfig(wait)` does for a shutdown or a registration; atomic, a temp file and a rename, and
  ordered with the save's own write by a lock). A crash or a failover right after the answer keeps
  the ban. When the file cannot be written, the new password stays in force (undoing it would give
  the compromised one back) and the answer says so (`persistError`), as for the tokens.
- **Copy:** off by default (`secret-kv-copy-api-credentials`, server scope, global settings). On,
  the instance's list, in clear, goes to the OpenSVC secret `system/sec/repman`, key
  `<host of api-public-url>_api-credentials`, through the first cluster on OpenSVC; the answer says
  whether it was written or skipped, and a failed copy is said (`copyError`): the copy then still
  holds the compromised password. The Vault copy is not written here yet.
- **Trade-off of the copy:** it holds every user's password of the instance's list in clear, readable
  by the infrastructure's administrators only (the OpenSVC system namespace); it lets them reach a
  repman whose own login is lost. Turn it on only where that trade is wanted, until the OpenSVC
  store replaces it.
- **Tokens:** a revocation that fails (the token store not saved) is undone in memory and said in
  the answer (`tokensError`) and in the GUI: a ban with live tokens is not a ban.
- **Limits:** rotations are serialized among themselves, not with the user add, update and drop
  paths, which write the cluster's own list. A token caller is checked with its owner's grants:
  the owner could do the same directly. Rotating one's own password through a token revokes that
  token too; the answer carrying the new password is its last. Token revocation takes the clusters
  before the token store's lock, never the manager's lock under it.

### Two levels, every store the same (design, 2026-11-11; reverses rules 6 and 7)

Decided with the maintainer on 2026-11-11, in the rotation PR. Before it, an instance secret was
never copied (rule 6), OpenSVC was a consumer and not a store (rule 7), and the rotation copied the
instance's list to OpenSVC by a path of its own while Vault kept the old value. Now every store
is treated the same; only the path a level maps to differs per store.

- **The level comes from the config.** A secret whose setting is tagged `scope:"server"` is a
  server secret: one value per repman instance. Every other secret is a cluster secret: a key of
  the cluster's config. `api-credentials` gets the tag. There is no third level: the copies of
  a database or proxy credential under each server (`databases/<server>`, `proxies/<proxy>`) are
  delivery to the container that reads it, not a level, and stay as they are.
- **Entries have a level too.** The cluster entries (`[[<cluster>.secret-kv]]`) get the cluster
  secrets, as today; a sponsor may switch them on and read them (rule 14). Server secrets go only
  to server entries (`[[secret-kv]]` in the server config): never to a cluster entry, so a
  sponsor or a member never reads one.
- **One chain per level.** A server secret has one credential chain and one version store for the
  instance (`<working-dir>/secret_store.json`, written by the server's save callback, T25, and at
  once by a change of a server secret, and carried by its config push like the cluster files),
  not a copy per cluster: the clusters read it from there. A rotation of `api-credentials` is one
  version, not one per cluster.
- **A server secret is set in the global settings only.** `setServerSetting` asks the server
  credentials grant on every cluster, sets it through the server's chain, then every cluster reads
  it from the server and uses it (`Cluster.ApplyServerSecret`), and the server's file is written.
  Through one cluster's settings it is refused (`Cluster.SetCredential`, `ErrServerSecret`). A
  peer's event log never applies one to a cluster: the server's file in the config pull brings it.
- **The upgrade.** An older release kept versions of server secrets in each cluster's file. At
  start, before the config files' values are checked, the server reads every cluster's file and
  merges them, and writes its own file. A cluster hands them over again at each load of its file
  (`FileBinding.Loaded`), and drops its copy only once the server's file holds them; when the
  server cannot write, the cluster keeps them. A cluster's file that cannot be read is never
  overwritten (`ErrFileUnread`, read again at each save, then merged), and while one is unread the
  value in `/etc` is not taken over the versions at that start. With versioning off there is no
  versioned file: an older release kept the list in the config, which the server still reads. A
  cluster section that sets `api-credentials` is not read: the server level answers first, the
  cluster's own list is `api-credentials-external`.
- **Clocks.** Versions are ordered by time when two monitors' files merge. A new version is never
  stamped before the last one (`credstore.AfterLast`): after a failover to a monitor whose clock is
  behind, its rotation still sorts after the version it replaces, and the banned value never comes
  back as the current one. The clocks are still expected to be close (NTP): two rotations made on
  two monitors at once, each before seeing the other's, are ordered by their clocks.
- **The standby is behind until its next pull.** The server's file is on the active monitor's disk
  before the rotation is answered; it reaches the standby by the config push and pull only. A
  standby promoted before that pull still holds the old list, so the banned password works there
  until the pull (or until the operator rotates again on the new active monitor). The pair shares
  the data directory where it can, which closes the window.
- **One change of a server secret at a time.** `serverSecrets.setMu` is held by every change of a
  server secret, across the read and the write of a rotation's read-modify-write of the list, and
  by the global settings' setter: no change lands between a rotation's read and its write.
- **Only sysops and extsysops reach the server level**: they alone set the server entries and
  change a server-scope key, through one grant both hold (outside the `global` family, which
  extsysops does not get). No other role reads or writes either.
- **Plugins: config, vault, opensvc, kubernetes**, one interface. The core hands every active
  entry the same thing: a version of a key at a level (server, or cluster and its name). Where
  it lands is internal to the plugin: the core never builds a store path, and has no case per
  store. The queue is shared (versions, newest wins, retry, bounds, WARN0271). What the
  plugins do today, for reference only:

  | Level | vault | opensvc |
  |---|---|---|
  | Server | `<mount>/system/repman/<host>/<key>` | `system/sec/repman`, key `<host>_<key>` |
  | Cluster | `<mount>/<path-prefix>/<cluster>/<key>` | `<cluster>/sec/repman`, key `<key>` |

  `<host>` stands for the instance's name below. The OpenSVC cluster `env` secret (the
  `MYSQL_ROOT_PASSWORD` the containers read) stays the provisioning's delivery, written as today.
- **The instance's name** in a shared store is `<domain>/<subdomain>/<zone>`, never the URL (an
  instance may be registered and not public, an on-prem URL may be a bare hostname). Registered:
  the registration (`cloud18-domain`, `cloud18-sub-domain`, `cloud18-sub-domain-zone`). Not
  registered: its own name, three server settings (`instance-domain`, `instance-sub-domain`,
  `instance-sub-domain-zone`): the company, the hostname (read once at the first start and saved,
  prefilled) and the location (an office, a country), asked in a window at the sign-in of a
  sysops or extsysops (grant `cluster-server-credentials` on every cluster) through
  `/api/clusters/settings/actions/instance-name`. The `cloud18-*` settings are never filled
  with the local name: their readers take them as "registered". Name not complete: server
  secrets stay in the local version store and the window asks again. "No thank you, don't ask again"
  (for one who does not plan to version and rotate secrets, nor wants the security, workload and
  schema monitoring plugins) sets `instance-name-declined`: the window no longer opens, for any
  sysops or extsysops, until that global setting is switched off. The name stays local until
  the instance is registered with a Cloud18 account (community plugins, config versioning in the
  Cloud18 GitLab).
- **The move.** When the name changes (a licence loaded, a registration, a renamed cluster for
  its cluster secrets), every server store gets every kept version of every server secret at
  the new name, oldest first, from repman's own version store (never read from the old path).
  The old copy stays (repman never deletes a secret), with a `moved-to` key naming the new one.
  A store not yet moved is behind (WARN0271) until a pass writes it.
- **A store holds its own login.** The Vault entry's secret id or the OpenSVC certificate's
  password are copied to that store too: the copy is information, the login is read through
  the chain (config first), so there is no loop.
- **The `/etc` writer closes the loop.** repman starts as root and drops to `--user` (server.go);
  before the drop it starts a child of its own binary (as the backups mount does, #2246) that
  stays root. Its one request: replace credential K in file F, refused unless F is under
  `/etc/replication-manager`, K is a credential tagged server and F already holds K; the value is
  written encrypted with the repman key (F9), in place, owner and mode kept. A change of a
  server credential is a version (it wins at once), then a copy in every server store, then the
  file: at the next start the file and the newest version agree. Not started as root, or the
  file not writable: the version still wins, and a state says the file is behind.
- **Replaces** the rotation's own OpenSVC copy and `secret-kv-copy-api-credentials`.

### Server credential stores (implemented)

The server level's vault entries, decided with the maintainer on 2026-11-12: the server secrets
in any Vault, the Cloud18 one or the client's own, kept apart from every cluster so a cluster's
sponsor or member never reaches them.

- **Config:** `[[default.server-secret-kv]]` (`Config.ServerSecretKvs`, `toml:"-"`, `json:"-"`),
  read from `[default]` and `[saved-default]` only (`LoadServerSecretKvs`), saved by the server's
  save (`SaveDynamic`, `SecretKvsTomlAt(SecretKvLevelServer, "saved-default")`, secrets
  encrypted, F9) unless a config file sets the key (then the API refuses, 409). A cluster's
  config never holds it: `GetClusterConfig` clears it, whatever the cluster's section says. The
  entry format, the plugin (`secret_kv-vault`) and the methods are the cluster's, given a level
  (`config.SecretKvLevel`: `PutSecretKvAt`, `SetSecretKvEnabledAt`, `DeleteSecretKvAt`,
  `SecretKvActiveAt`, `SecretKvListAt`).
- **Path:** `<mount>/<path-prefix>/system/repman/<instance name>/<key>`
  (`ServerSecretKvFolder`), the name being `InstanceStoreName`. While the name is not complete,
  nothing is written and the entry is behind. Two of a client's instances collide only if the
  client gives them the same prefix and the same name; on the Cloud18 vault the registration
  keeps the names apart.
- **Writes:** the server chain's `OnSet` hook queues every change of a server secret (a
  rotation, a global setting); a full pass writes every server secret's current version (compare)
  at start, when an entry is added, changed or turned on, and after versions are taken over from
  a cluster's file. Active monitor only. The queue and the worker are the cluster's
  (`credstore.StoreRunner`, shared): newest wins, bounded, retried every `secret-kv-retry-interval`.
- **Who:** `/api/clusters/settings/actions/server-secret-kv` (list, `add`, `edit`, `enable`,
  `disable`, `remove`): the server credentials grant on every cluster (`instanceNameACL`, ACL rule
  `cluster-server-credentials`), the list included, so no other role sees an entry. Refused on a
  standby (409). Each change is a security event. GUI: the global settings, "Server Credential
  Stores" (`SecretKvEntries level='server'`).
- **States:** a server entry behind is `GWARN024` on the server's state machine and `WARN0320`
  on every cluster's security state machine (`ServerGlobals.ServerStoresBehind`): broken for the
  server, broken for every cluster that reads the server secrets.
- **Not yet:** the Cloud18 vault offered at the server level by the registration (a sysops adds
  it as an entry with `auth = jwt` meanwhile), the OpenSVC and Kubernetes stores, and the move on
  a name change.

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
A cron that does not parse is refused by the setter (API, GUI: HTTP 400 from `set-cron`), the
former cron kept. One read from a config file at start cannot be scheduled (`SetScheduler`
returns the parse error) and opens WARN0274, while the schedule is on. The skip reason is in
memory: after a failover the new active monitor shows WARN0274 from its own first skipped run,
up to one cron period later.

Saved before the databases change: `RotatePasswords` sets the new values through the chain
(`SetAllRevertible`), then `WaitConfigSaved` asks for a save and waits for the config-sync gate
to complete one that started after the ask (`ConfigSavedAsOf`, the start of the last save
completed; a save whose version file was not written does not count). Only then does it change
the master's users, the replicas' channels, the proxies and the orchestrator secret. The
rotation writes no file itself (T25): the gate is still the only writer. A save that does not
come within two gate periods (30 s at least: a standby repman never saves, a busy cycle skips a
gate) puts the former values back (the stores, `Conf.Secrets` with its `OldValue`, the copies)
and the run is abandoned, nothing changed, a skipped run for the schedule. So a repman stopped
before the save starts on the former credential, and after it on the new one, which the master
holds as soon as the rotation reaches it. A stop while the servers are changed leaves the ones
not reached yet on the former password, as a failed step does (`OldValue` is in memory only). One rotation at a time (`inPasswordRotation`): the wait can outlast a cron period,
and `CanRotatePasswords` refuses a second one while it runs. Left: a save that started before
the deadline and completes after the revert writes the new values, the next save the former
ones; repman stopped between those two saves starts on a password no database took.

Outcome: a step that fails after the save (a user on the master, a replica's channel, the
orchestrator secret) is logged and the next steps still run; `RotatePasswords` then returns
`ErrRotatedWithErrors` listing them, and the scheduled run logs "done with errors" in the
security log. Not a skipped run: the new credentials are in force.

Old backups: `backupPasswordHistory` (`srv_job_backup_encryption.go`) reads the old root
passwords from the local file to open an encrypted backup taken before a rotation.
`backup-encryption` turns versioning on (`IsMonitoringSecretVersioningEnabled`), so a cluster
whose backups are encrypted with the root password always keeps the file: the config is
never its store, and every password that encrypted a backup stays in the history.

Regtest (T13): `testCredentialConfigRotationScheduled` (MariaDB and MySQL, in the default
list; versioning off and the scheduler on for the test): the schedule on with a cron of every
minute rotates the database credential; every server logs in with the new password, the
previous one is kept in memory, a config reload and a start from the files on disk run on the
new one, and `<cluster>.toml` holds it encrypted. The test waits for the run to end
(`IsInPasswordRotation`, up to two gate periods for the save) and fails when the new
credential was put back.

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

## Cluster accounts and the version store (#2358, #2359)

- **An added or removed account is recorded.** `addUser` and `DropUser` write `api-credentials-external`
  through the chain, then mark the version store dirty (`MarkSecretVersionStoreDirty`), as
  `setAddedProxysqlCredential` does. Before, an account added after the last start lived in memory only:
  the next start read the store's older version first (#2227 rule 1) and the account was gone, and a
  removed account came back. Known limit: removing the **last** external account leaves an empty value,
  which the store does not record (it keeps encrypted values only), so that account comes back at the
  next start; a cluster always keeps `system` there in practice.
- **users/add creates an SSO-only account for a Cloud18 identity** (`AddAPIUser`): on a Cloud18 instance,
  an email added without a password goes to `AddSSOOnlyUser`. With `AddUser`'s random password it was
  local-only (`IsLocalOnlyAccount`), so every SSO session of it (a Cloud18 login, another instance's realm
  token) was refused on each cluster ("No valid ACL", an empty `/api/clusters`). A non-email user, or an
  instance that is not Cloud18, keeps the random password `users/send-credentials` sends.
