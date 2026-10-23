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
  identical bytes: no rewrite, no git change, no config event.
- **When the config does not keep them** (`s3ProvidersInConfig`): without the dynamic config
  (`ConfRewrite` off), or with `s3-provider` set in a config file (pinned), `s3providers.json`
  stays their store, written as before.

## Migration

`LoadS3Providers`: the config's blocks first; with none, `s3providers.json`. Read from the
file with the dynamic config, the providers are marked `fromFile` and a save is asked; the
first `SaveConfigFile` that writes the blocks renames the file `s3providers.json.migrated`
(kept, never read again). A failed save leaves the file in place: the next start reads it
again, nothing is lost.

## Peers

The saved config diffs per entry (`flattenTomlValues`, named tables):
`s3-provider.<name>.<key>`. A peer applies the event with `ApplyS3ProviderSetting`
(`server/server_eventlog.go`): a key's ciphertext is decrypted with the shared key and
kept, so both peers save the same bytes; a provider the events create joins the list only
once its keys validate (held in `pending` until then); unsetting its `name` removes it.
Before #2155 the providers never reached the peers.

## Unchanged

The `/s3providers` API and the GUI. A CRUD call now persists through the next config save
(`cluster.Save()`), like the archives, instead of writing the file synchronously.
