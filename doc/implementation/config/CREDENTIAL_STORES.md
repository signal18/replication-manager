# Credential stores: one interface for config, local store and Vault (design)

Status: step 1 (`secret_kv-config`, the chain) implemented in `config/credstore`; versioned
stores (#2227) designed and closed 2026-10-28, rules 1-5, 8, 9, 11, 12 implemented for the
database credentials and the chain keys (branch `feat/vault-secrets-copy-2227`, not merged):
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

1. **The config keeps the starting value forever**: the first value, known at provisioning
   or typed by an operator (`root:pass1`). A rotation or a change never writes the config:
   the current value and its versions live only in the active stores. When no active store
   keeps versions (versioning off, no vault), a change is refused as a rotation is (rule 9),
   so the config never holds anything else. Provisioning, a reprovision and the regtests
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
   entry is behind (WARN0269) and the full pass catches up. Never on the monitor tick.
2. **Every write is a version in every active store.** The version is made in memory at the
   write, with its time and writing instance; the file is written only by the cluster's save
   callback after `cluster.Save()` (T25) and is in the config git push (encrypted values
   only, F9; versions change only at rotations and changes, T17). Each vault through a bounded queue (T18: the latest value per key wins, a hard cap,
   drops counted) and a worker with a timeout per write, after the chain's lock: it never
   blocks the write, a provisioning or the monitor (F2). Only the active monitor writes.
3. **Read**: the newest version wins across the stores (timestamp and writing instance,
   the event log's LWW rule); the seed last. A store behind the others is a tracked state
   (WARN0269, security domain, per entry) and gets the winning version at the next pass.
4. **Full pass**: when a store is turned on, at start, and after queue drops (WARN0270).
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
8. **`database_engine`** is a vault store that generates: a rotation asks Vault
   (`rotate-role`); the value it returns is written as a new version to the other active
   stores; it is read from the engine first.
9. **Rotation** is allowed when an active store keeps versions (the file with versioning
   on, or any vault entry); refused otherwise, as today (`canRotatePasswordsOutsideVault`,
   its check becoming "an active store keeps versions"). A pinned value stays refused.
   Turning versioning or a store off is allowed; it only brings that refusal back.
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
13. **repman's key**: changing it re-encrypts the file; the vaults encrypt their own data.
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
(`Cluster.ChainAtStart`). The rotation needs `db-servers-credential` not pinned in a config
file.

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
- **States**: WARN0269 (security, per entry: behind, or failing), WARN0270 (security: drops
  not repaired), WARN0271 (config, per key: a Vault path pinned in a file).
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
- **Rotation** is allowed when the store taking the write is `Versioned` (a lost new
  password can be recovered from its history), or when a store `Generates` (that store
  rotates).
- **A pin is a starting value.** A store records the pinned value it last saw. When the
  operator edits that value in the file, the file wins and its value goes into the store
  as a new version: the operator's intent beats an older stored value.

## The four cases are four chains

| Chain | Read | Change | Rotation |
|---|---|---|---|
| **Vault**: `VaultKV` or `VaultDbEngine` → `DynamicConfig` → `PinnedConfig` | from Vault, at the path the config gives, pinned or not | KV: a new Vault version. Database engine: refused | KV: repman writes a new version. Database engine: Vault rotates |
| **Local**: `LocalSecretStore` → `DynamicConfig` → `PinnedConfig` | the store's latest version, else the config | a new version in the store, pinned or not | allowed (versioned) |
| **No store, not pinned**: `DynamicConfig` → `PinnedConfig` | `<cluster>.toml` | saved to `<cluster>.toml` | refused (no history) |
| **No store, pinned**: `PinnedConfig` | the file | refused: the file wins at restart | refused |

So:

- whether a value is **pinned** only matters when there is no store;
- **Vault or local** only decides who keeps the versions and who makes the passwords;
- a Vault credential is never written to repman's files: they hold the path only.

The last row is today's behaviour for every credential outside Vault (`CanSetCredential`
and `canRotatePasswordsOutsideVault` in `cluster/cluster_sec.go`, #2042 and #2048): the
safe default until the stores exist.

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

- **The pin rule**: should an operator's edit of a pinned value always win over the
  store (proposed), or should the store win and raise a state that the file differs?
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
