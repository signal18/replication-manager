# Webhook URLs and other credentials as flat secrets

## Problem

`config.SecretKeys` lists the flat config keys repman holds as secrets. A listed key is masked
on the API (`Config.MaskSecretsJSON`: REST and MCP), saved encrypted, and carried as
ciphertext in the config event log. Eight keys holding credentials were not listed. Their values
were returned in clear by `GET /api/clusters/{name}`, written in clear to the saved
`<cluster>.toml` and `event-changed.*.log`, and printed by the "API receive set setting" log
line, decoded from base64. This was reproduced on develop 6152edb6d on the lab for the three
Teams/Slack URL keys.

| Key | What it holds |
|---|---|
| `alert-slack-url` | Slack incoming-webhook URL: the URL is the credential |
| `cloud18-alert-slack-url` | Cloud18 Slack incoming-webhook URL |
| `alert-teams-url` | Teams webhook URL |
| `alert-teams-proxy-url` | proxy URL for Teams, may carry `user:password@` |
| `monitoring-tunnel-credential` | SSH tunnel `user:password` |
| `registry-consul-credential` | Consul `user:password` |
| `registry-consul-token` | Consul token (no reader yet) |
| `mysqlrouter-pass` | MySQL Router password (no reader yet) |

## What a flat secret needs

Listing a key in `SecretKeys` alone breaks it. Each of these keys needs all of the following:

1. **A save case.** It is listed in `SecretKeys`, and both `Cluster.GetEncryptedValueFromMemory`
   and `ReplicationManager.GetEncryptedValueFromMemory` have a case returning
   `GetStableEncryptedValue(key, GetDecryptedValue(key))`. The default branch returns `""`, so
   a listed key without a case is saved empty and its event wipes it on the peers.
2. **Readers read the secret.** After a save, the struct field holds the ciphertext
   (`https:hash_…`: `EncryptSecretValue` keeps the part before the first `:` in clear), so a
   reader uses `Conf.GetDecryptedValue(key)`, never the field. A test against `""` can stay on
   the field.
3. **Setters write through the chain.** `CredentialChain().Set(key, value)` comes first, then the
   field. The save encrypts the chain's value, so a setter that sets the field alone is reverted
   at the next save. The URL setters (`Cluster.setSecretURL`) also refuse `config.SecretMask`:
   the API returns `*:*` for a secret, and a GUI form saved unchanged sends it back.
4. **The change log masks it.** The key is in the `****` case of `GetApiChangeLogFormat`.

## URLs and Vault

`Config.IsPath` decides whether a secret value is a Vault path to resolve. It returns false
for a value containing `://`: a URL is never a Vault path. Before this change, a webhook URL
was taken for a path when a secret-kv entry or Vault was configured, and the URL was logged
as the path that could not be read.

## Peer replay

`applyPeerConfigEvent` decrypts a secret event and applies it through `setClusterSetting`. The
URL setters take base64, so a plain URL from an event fails there and is applied by
`applyGenericClusterKey`, which sets the field only. The replay now sets the secret's value
along with the peer's ciphertext, so the readers of item 2 get the URL on the peer.

## Send errors

The Teams client and the Slack hook return HTTP errors that quote the webhook URL
(`Post "https://…": …`). `sendMsTeams` removes the URL from the error it logs. slackman
registers each hook wrapped in `urlRedactingHook`, which removes the URL from the error logrus
prints when a hook fails.

## Not covered

`backup-restic-additional-env` (it may carry `AWS_SESSION_TOKEN`) is an archive setting, so
it belongs in `archiveSecretKeys`. As a flat key, the decrypt drops its comma-separated parts of
one character or less.

## Tests

- `config/secret_keys_test.go`:
  - every string `Config` key named like a credential is in `SecretKeys`, or in
    `notFlatSecrets` with the reason;
  - the 8 keys are masked in the API JSON;
  - `IsPath` on URLs.
- `cluster/cluster_secret_url_test.go`:
  - set → API JSON → save → restart round trip of the 4 URL keys;
  - the mask is refused;
  - every key has a save case.
- `server/server_eventlog_secret_test.go`: peer replay of an encrypted URL.
