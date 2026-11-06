# Credentials held as flat secrets, and the alert URLs that are plain settings

## Context

`config.SecretKeys` lists the flat config keys repman holds as secrets. A listed key is masked
on the API (`Config.MaskSecretsJSON`: REST and MCP), saved encrypted, and carried as
ciphertext in the config event log. #2264 listed eight keys that held credentials and had not
been listed: their values were returned in clear by `GET /api/clusters/{name}`, written in clear
to the saved `<cluster>.toml` and `event-changed.*.log`, and printed by the "API receive set
setting" log line, decoded from base64.

Four of the eight stay secrets:

| Key | What it holds |
|---|---|
| `monitoring-tunnel-credential` | SSH tunnel `user:password` |
| `registry-consul-credential` | Consul `user:password` |
| `registry-consul-token` | Consul token (no reader yet) |
| `mysqlrouter-pass` | MySQL Router password (no reader yet) |

The other four, the alert URLs, are plain settings again since #2342: see "Alert URLs" below.

## What a flat secret needs

Listing a key in `SecretKeys` alone breaks it. Each of these keys needs all of the following:

1. **A save case.** It is listed in `SecretKeys`, and both `Cluster.GetEncryptedValueFromMemory`
   and `ReplicationManager.GetEncryptedValueFromMemory` have a case returning
   `GetStableEncryptedValue(key, GetDecryptedValue(key))`. The default branch returns `""`, so
   a listed key without a case is saved empty and its event wipes it on the peers.
2. **Readers read the secret.** After a save, the struct field holds the ciphertext
   (`user:hash_…`: `EncryptSecretValue` keeps the part before the first `:` in clear), so a
   reader uses `Conf.GetDecryptedValue(key)`, never the field. A test against `""` can stay on
   the field.
3. **Setters write through the chain.** `CredentialChain().Set(key, value)` comes first, then the
   field. The save encrypts the chain's value, so a setter that sets the field alone is reverted
   at the next save.
4. **The change log masks it.** The key is in the `****` case of `GetApiChangeLogFormat`.

## Alert URLs

`alert-slack-url`, `cloud18-alert-slack-url`, `alert-teams-url` and `alert-teams-proxy-url` were
secrets from v3.1.47 to v3.1.48 (#2264). They are plain settings again (#2342): the back office
reads a client's configuration to find why an integration does not post, and an encrypted URL
hides the host, the channel and the token.

- They are not in `SecretKeys` and not in the save-case lists. The setters and the readers use
  the config fields, and the API shows them.
- `notFlatSecrets` in `config/secret_keys_test.go` lists them with that reason, so the guard on
  credential-like key names accepts them.
- How sensitive the URLs are (a webhook URL is a bearer credential; the Teams proxy URL can carry
  `user:password@`) is left for a separate discussion.

### A config saved by v3.1.47 or v3.1.48

Such a config holds each URL as `https:hash_<hex>`: `EncryptSecretValue` splits on the first `:`
and encrypts everything after it, so the host is encrypted too. Removing the keys from
`SecretKeys` alone leaves the field as that string, and every send fails with
`http: no Host in request URL`.

`Config.DecryptLegacyEncryptedURLs` reads the encrypted form back as the URL, with
`GetDecryptedEmbedded` (it decrypts every `hash_<hex>` token of a string, so it gives back the
whole URL from the form above). `Cluster.SetClusterCredentialsFromConfig` runs it on the cluster
config at start and at each reload, before `InitFromConf` configures the Slack hooks. It does
nothing without the encryption key, and leaves a plain URL alone. The next save writes the URL in
plain.

It runs on the cluster config only. Every reader of the four fields reads `cluster.Conf`; the
server-level `repman.Conf` values are not used to send an alert.

### Peer replay and the logs

- A peer event for one of these keys carries the URL as the peer saved it. A peer still on
  v3.1.47 or v3.1.48 sends it encrypted: the next start or reload of this repman reads it back.
- The config event log now records the URLs in plain.
- The API change log still prints `****` for them (the `****` case of `GetApiChangeLogFormat`
  lists them).
- A runtime change of `alert-slack-url` through the API updates the URL (`LogSlack.SetURL`) but
  does not activate the Slack hook: the hook is activated at cluster start, and only when the URL
  is already set.

## URLs and Vault

`Config.IsPath` decides whether a secret value is a Vault path to resolve. It returns false
for a value containing `://`: a URL is never a Vault path. Before #2264, a webhook URL
was taken for a path when a secret-kv entry or Vault was configured, and the URL was logged
as the path that could not be read.

## Peer replay of a secret

`applyPeerConfigEvent` decrypts a secret event and applies it through `setClusterSetting`. The
replay sets the secret's value along with the peer's ciphertext, so the readers of item 2 get
the value on the peer.

## Send errors

The Teams client and the Slack hook return HTTP errors that quote the webhook URL
(`Post "https://…": …`). `sendMsTeams` removes the URL from the error it logs. slackman
registers each hook wrapped in `urlRedactingHook`, which removes the URL from the error logrus
prints when a hook fails. This stays with the URLs plain.

## Not covered

`backup-restic-additional-env` (it may carry `AWS_SESSION_TOKEN`) is an archive setting, so
it belongs in `archiveSecretKeys`. As a flat key, the decrypt drops its comma-separated parts of
one character or less.

## Tests

- `config/secret_keys_test.go`:
  - every string `Config` key named like a credential is in `SecretKeys`, or in
    `notFlatSecrets` with the reason (the four alert URLs are there);
  - the tunnel and Consul credentials and the router password are masked in the API JSON;
  - `IsPath` on URLs;
  - `DecryptLegacyEncryptedURLs`: the four URLs, a plain URL left alone, nothing decrypted
    without the key.
- `cluster/cluster_secret_url_test.go`:
  - set → API JSON → save of the four URLs: shown on the API, saved in plain;
  - a config saved encrypted: the start reads the URLs, the next save writes them in plain (the
    test fails when the decrypt call is disabled);
  - every secret key has a save case.
- `server/server_eventlog_secret_test.go`: a plain peer event sets the URL; a legacy encrypted
  one is read back after a reload.
