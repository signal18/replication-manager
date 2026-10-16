# Credential chain: step 1 of #2075 (implementation)

Design: `CREDENTIAL_STORES.md`. This note covers what step 1 built: the interface, the
internal `secret_kv-config` plugin and the chain, with every runtime setter moved onto it.
It fixes #2017 (`TestTagChangeKeepsRuntimeCredentials`).

## Package `config/credstore`

- `CredentialStore`: `Name`, `Get`, `Set`, `CanSet`, `Versioned`, `Generates`.
- `PinnedConfig` reads `ImmuableFlagMap` (the files repman never writes) and refuses every
  change.
- `DynamicConfig` reads and writes `DynamicFlagMap`. `Set` stores
  `conf.EncryptSecretValue(value)` (the user in clear, the password `hash_`-encrypted); the
  chain then calls its save event (`cluster.Save` / `repman.Save`, no I/O: SaveCallBack
  writes, T25). It refuses only a pinned key. Its name, and `Persistent()`, follow `monitoring-save-config`
  (`Conf.ConfRewrite`): `dynamic-config` when on, `runtime-config` (memory only) when off.
- `Chain`: `NewConfigChain(conf, save)` = DynamicConfig, then PinnedConfig, then the flag
  default (`DefaultFlagMap`, source `default`).
  - read: the first store holding the key; `Source(key)` names it;
  - write: `Set(key, value)` into the first store whose `CanSet` is nil, then the cache entry
    becomes `Secret{Value: value, OldValue: <previous>}`;
  - `SetAll(changes...)` writes credentials that go together (the rotation's) as one unit:
    every key's store is chosen under one lock before any is written; a store failing
    midway has the ones already written put back to their former value (still in the
    cache, which changes only once all are written). `Set` is `SetAll` of one change;
  - save events run after the lock is released, once per store: a save that reaches the
    chain again (`Source`, `Refresh`) cannot deadlock it (F2);
  - a refusal is a `*RefusedError` (`errors.Is(err, ErrChangeRefused)`); `Reason(err)` is
    the text shown in the GUI;
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
- Rotation outside Vault writes its new values with `chain.SetAll` before any server is
  changed; a proxy credential is rotated only when the chain takes it (`chain.CanSet`).
  The durable file is still written by the deferred save: a crash between the `ALTER USER`
  and that save is closed in step 3 (versioned pending/current/previous record, #2041).
- `cluster.ErrCredentialChangeRefused` is `credstore.ErrChangeRefused`.
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

- `config/credstore/credstore_test.go`: read order, set survives refresh, refusals and their
  reason, scopes, concurrent setters (`-race`), a save event re-entering the chain (fails on
  a deadlock after 5 s), a store failing midway through `SetAll` (nothing changed).
- `cluster/cluster_credstore_test.go`: set → tag → save → reload from `[saved-<cluster>]`.
- `TestTagChangeKeepsRuntimeCredentials` (#2017), `TestGetCredentialsInfo` (sources).
- Regtest `testCredentialPersistTagReload` (T13), on a live cluster through repman's own API
  (regtest token, real routes and ACL): a credential set through the settings API survives
  a database tag added and dropped, is saved encrypted by the save callback, and is the value
  a start builds from the files on disk: the `LoadClusterConfigFromDisk` hook runs the
  server's own loader (`ReconstructLiveClusterConfig`) and puts the server's config maps
  back. A pinned database credential is refused by the API and unchanged. It uses
  `backup-verification-credential` (never sent to a monitored server) and puts the former
  value back. Run it by name:
  `POST /api/clusters/<cluster>/tests/actions/run/testCredentialPersistTagReload`.
  A regtest cannot restart repman: the real restart is a lab check (a wrong verification
  credential set, repman restarted, still refused; the right one set, restarted, connects).
