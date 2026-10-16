# Credential stores: one interface for config, local store and Vault (design)

Status: **design, not implemented** (2026-10-16). Issues: #2017 (cause), #2041 (local
store wins at start), #2057 (Vault settings and setter). Related laws: T2 (one way to do
a thing), T25 (tracked files written only by the save callback).

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
3. `secret_kv-localstore` from `SecretVersionStore`, and the start order (#2041);
4. `secret_kv-vault` (enterprise) from the Vault code, and the Vault setter (#2057).

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
