# Credential chain: step 1 of #2075 (implementation)

Design: `CREDENTIAL_STORES.md`. This note covers what step 1 built: the interface, the
internal `secret_kv-config` plugin and the chain, with every runtime setter moved onto it.
It fixes #2017 (`TestTagChangeKeepsRuntimeCredentials`).

## Package `config/credstore`

- `CredentialStore`: `Name`, `Get`, `Set`, `CanSet`, `Versioned`, `Generates`.
- `PinnedConfig` reads `ImmuableFlagMap` (the files repman never writes) and refuses every
  change.
- `DynamicConfig` reads and writes `DynamicFlagMap`. `Set` stores
  `conf.EncryptSecretValue(value)` (the user in clear, the password `hash_`-encrypted) and
  calls the save event (`cluster.Save` / `repman.Save`, no I/O: SaveCallBack writes, T25). It
  refuses only a pinned key. Its name, and `Persistent()`, follow `monitoring-save-config`
  (`Conf.ConfRewrite`): `dynamic-config` when on, `runtime-config` (memory only) when off.
- `Chain`: `NewConfigChain(conf, save)` = DynamicConfig, then PinnedConfig, then the flag
  default (`DefaultFlagMap`, source `default`).
  - read: the first store holding the key; `Source(key)` names it;
  - write: `Set(key, value)` into the first store whose `CanSet` is nil, then the cache entry
    becomes `Secret{Value: value, OldValue: <previous>}`;
  - `CanSet`, `Persists`, `Versioned` answer for the store a change would go to;
  - `Refresh()` rebuilds `Conf.Secrets` from the stores (`Config.RebuildSecrets(source)`);
  - one mutex covers store reads, writes and the cache updates that go with them.

## Wiring

- `Config.SecretKeys` lists the credential keys; `DecryptSecretsFromConfig` is now
  `RebuildSecrets(configSecretSource)`, the same read order without a chain.
- `Cluster.CredentialChain()` and `ReplicationManager.CredentialChain()` build the chain
  lazily and again when `Conf` is replaced.
- `SetClusterCredentialsFromConfig` (called by every tag change and reload) calls
  `CredentialChain().Refresh()`.
- Setters on the chain, returning its refusal: `SetDatabaseCredentials`,
  `SetReplicationCredential`, `SetProxyServersCredential`, `SetResticPassword`,
  `SetBackupVerificationCredential`, the secret settings of `setClusterSetting` and of the
  global settings, cloud18 register (`cloud18-gitlab-password`), the rotation outside
  Vault, Docker registry credentials, external API users, app random passwords, and the
  secret store restore (which now decrypts the stored value before setting it: it used to
  set the ciphertext as the live password).
- Refusals: `CanSetCredential` and `canRotatePasswordsOutsideVault` use `chain.CanSet`;
  `CanSetCredential` (database and replication credentials) also refuses when
  `!chain.Persists`: in static mode their loss at the next restart locks repman out (#2048). `GetCredentialsInfo.Source` is `chain.Source` (Vault
  still overrides it). The GUI labels `runtime-config` and `default`.
- Save: `SaveConfigFile` (cluster) and `SaveDynamic` (server) wrote a secret only when its
  field differed from the default; they now also write a secret present in
  `DynamicFlagMap`, encrypted (`GetEncryptedValueFromMemory`, stable ciphertext).

## Left on direct `Secrets.Set` on purpose

- Vault resolution (`DecryptSecretsFromVault`): step 4 moves it into `secret_kv-vault`;
- the encrypted-form cache entries, `inject.toml` and the peer config event replay: they
  load values, they are not changes;
- the git access token: `InitGitConfig` pins it in `ImmuableFlagMap`, so the chain would
  refuse its runtime update: kept as it is (decided).

## Tests

- `config/credstore/credstore_test.go`: read order, set survives refresh, refusals, scopes,
  concurrent setters (`-race`).
- `cluster/cluster_credstore_test.go`: set → tag → save → reload from `[saved-<cluster>]`.
- `TestTagChangeKeepsRuntimeCredentials` (#2017), `TestGetCredentialsInfo` (sources).
