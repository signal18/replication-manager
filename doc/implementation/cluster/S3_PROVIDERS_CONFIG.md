# S3 providers as native cluster config (#2155)

A cluster's S3 providers (the storages its archives and its backups folder use) were kept in
`<WorkingDir>/<cluster>/s3providers.json`, outside the cluster config: not in the config
repository the instance pushes to GitLab (the BO could not see which storage an archive
uses), not restored with the config, and not sent to the peers.

## Where they are now

`[[saved-<cluster>.s3-provider]]` blocks in `<cluster>/<cluster>.toml`, after the archive
list, with the keys an `[[s3-provider]]` block of `inject.toml` already uses:

```toml
[[saved-belair.s3-provider]]
access-key = "hash_…"          # encrypted, like every secret of the file
endpoint = "https://s3.bso.example"
name = "bso-s3"
provider-source = "custom"
region = "fr-par"
secret-key = "hash_…"

[[saved-belair.s3-provider]]
cloud18-sso = true             # no keys: the Cloud18 SSO gives them
endpoint = "https://minio.crm.ovh-fr-2.signal18.cloud18.io"
name = "cloud18"
provider-source = "custom"
region = "eu-west-1"
```

- **Read** (`config.S3ProviderFromSettings`) and **written** (`config.S3ProvidersToml`) by the
  same functions as `inject.toml`. `Config.S3Providers` (`S3ProviderBlocks`, `toml:"-"
  json:"-"`) only carries the blocks from the file to `LoadS3Providers`: never in the API's
  config object; the save renders them from the cluster's provider list.
- **Stable ciphertext**: encryption uses a random IV. The ciphertext a key was read (or
  replayed) with is reused while it encodes the same key (`stableS3Cipher`), so a save writes
  identical bytes: no rewrite, no git change, no config event. A remembered ciphertext the
  current encryption key does not decrypt to the same key (rotated key) is encrypted again;
  the cache forgets the keys no provider holds at each render (`pruneS3Cipher`).
- **Never in clear**: the saved config is pushed to the config repository. Without an
  encryption key the config does not keep the providers (below), and `S3ProvidersToml`
  refuses a key without the `hash_` prefix.
- **When the config does not keep them** (`s3ProvidersInConfig`): without the dynamic config
  (`ConfRewrite` off) or without an encryption key, `s3providers.json` (local, 0600) stays
  their store, written as before.
- **Pinned** (`S3ProvidersPinned`: `s3-provider` set in a config file): the list is read from
  that file's blocks and never written. The `/s3providers` add, modify and drop answer 409
  (change the config file), a peer's event is refused. The providers the instance adds itself
  (Cloud18 storage, backups folder mount, `inject.toml`) live in memory only, added again at
  each start or pull.

## Migration

`LoadS3Providers`: the config's blocks when the config keeps them, when they are pinned, or
when there is no `s3providers.json`; else the file. Read from the file with the dynamic
config, the providers are marked `fromFile` and a save is asked; the first `SaveConfigFile`
that writes the blocks renames the file `s3providers.json.migrated` (kept, never read again).
Only a list read from the file renames it. Records skipped at load (invalid, duplicate) are
then only in the `.migrated` file: a WARN says how many. A failed save leaves the file in
place: the next start reads it again, nothing is lost.

## Peers

The saved config diffs per entry (`flattenTomlValues`, named tables):
`s3-provider.<name>.<key>`. A peer applies the event with `ApplyS3ProviderSetting`
(`server/server_eventlog.go`): a key's ciphertext is decrypted with the shared key and
kept, so both peers save the same bytes. Events come one key at a time: a provider the events
create, or change through an invalid step (from the SSO to keys: `access-key` arrives while
`cloud18-sso` is still set), is held in `pending` and set once its keys validate; unsetting
its `name` removes it.
Before #2155 the providers never reached the peers.

## Unchanged

The `/s3providers` API and the GUI (except the 409 when pinned). A CRUD call now persists
through the next config save (`cluster.Save()`), like the archives, instead of writing the
file synchronously: `SaveS3Providers` renders the blocks first, so a provider that cannot be
saved (a key that cannot be encrypted) fails the call and is rolled back; a write error of
the config save itself is logged by the save, after the call has answered.
