# Secrets set at runtime (#2017)

A secret (`db-servers-credential`, `replication-credential`, a proxy password...) lives in
three places:

- `Conf.Secrets`: the decrypted value the cluster uses;
- the config maps `DynamicFlagMap` (dynamic layer), `ImmuableFlagMap` (`/etc`),
  `DefaultFlagMap`: the encrypted value the config was loaded with;
- the saved config files, written by `SaveConfig`.

`DecryptSecretsFromConfig` **rebuilds** `Conf.Secrets` from the maps (dynamic, then
immutable, then default). A secret changed at runtime only in `Conf.Secrets` is lost by
a rebuild: the map's older value comes back, and the next save writes it.

## Rules

1. **Only an init rebuilds the secrets.** `InitFromConf` (cluster start, `ReloadConfig`
   on a configuration just reread from disk) and the server's `InitConfig`. Tag changes
   (`AddDBTagConfig`, `DropDBTagConfig`, `AddProxyTag`, `DropProxyTag`) do not: they
   called `SetClusterCredentialsFromConfig` until #2017, a leftover of
   `SetClusterVariablesFromConfig` turned into a credential rebuild by b5a56fddd (2023).
2. **A secret set through the API is recorded in the dynamic layer.**
   `setClusterSetting` calls `Conf.RememberSecret(name)`: the value, encrypted
   (`GetStableEncryptedValue`, never the plaintext), goes to `DynamicFlagMap`, so a later
   rebuild finds it. Called before the secret version store is reconciled.
3. **Other runtime setters save.** Password rotation, Vault, the credential setters
   change `Conf.Secrets` then call `SaveConfig`; the reload that follows rereads the saved
   value. A new setter that does not go through `setClusterSetting` saves too, or calls
   `RememberSecret`.
4. **Secrets are locked.** `Conf.Secrets` is a `*SecretMap` (#2039): every access goes
   through `Get`, `Lookup`, `Set`, `Delete`, `Snapshot` (to range over a copy) or
   `Replace` (the rebuild swaps the whole set at once), under its own RWMutex; copies of
   a `Config` share the store, as they shared the map. `RememberSecret` writes the
   `DynamicFlagMap` under `dynamicFlagMapMu` (package level: `Config` is copied by value),
   and the secret reads of `DecryptSecretsFromConfig` / `GetStableEncryptedValue` take it.

Tests: `config/config_remember_secret_test.go`, `config/secrets_test.go` (concurrent setters, readers and rebuilds, run with `-race`), `cluster/cluster_tag_credentials_test.go`.
