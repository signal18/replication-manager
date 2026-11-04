# App template secrets stored encrypted (#2248)

A template can write a secret as a literal: a `secret` variable's value, a per-agent
(`conditional`) value of that variable, a git clone `pass`, an S3 mount `secretkey`. When an app
took its deployment from such a template, the literal was copied as is into
`<cluster>/apps/<app>.toml`, and the config push carried it to the git repository in clear (F9).
These values are now encrypted (`hash_…`) on the way in, as the deployment API already does for
a value added or modified by hand.

## Where

`(*Cluster).EncryptDeploymentSecrets(d *config.Deployment) bool` (`cluster/cluster_app.go`) is
called on every path that copies a deployment from outside the app:

| Path | Call site |
|---|---|
| App created from a template | `AddSeededApp`, after the parsed template is unmarshalled |
| `settings/actions/reset-from-template` and its `/preview` | `buildValidatedTempAppConfigFromTemplate`, before the projection |
| Peer import (`apps/peer-import/apply`) | `ImportAppConfig`, after the load; the saved file is rewritten when a value was encrypted |

The peer import case covers an app the peer still holds in clear (created from a template
before this change): the importer does not keep it in clear.

## Rules

- Encrypted: `secret` variables (value and each per-agent value), `GitClone.GitPass`,
  `S3Mount.SecretKey`. Other variable types and the S3 access key are left as they are.
- Skipped: an empty value, a value that starts with `hash_`, and a value with an encrypted token
  inside it (`config.HasEncryptedToken`: `hash_` and at least 34 hex digits).
  - A value starting with `hash_` is a ciphertext, decrypted whole by `GetDecryptedPassword`. Its
    length is not checked: the ciphertext is the hex of a 16-byte IV and the plaintext, so an
    encrypted empty value (what the modify route stores for an emptied secret) has 32 hex digits,
    and a failed encryption leaves `hash_` alone.
  - A token inside a longer value comes from a placeholder: `{{app.db.password}}` in a DSN
    renders `postgres://user:hash_…@proxy/db`. Encrypting that value again would wrap the
    ciphertext, and provisioning (`GetDecryptedEmbedded`, same 34-digit matcher) decrypts one
    level only, so the container would get the inner ciphertext instead of the password.
  - A clear value that merely contains `hash_` (`my_hash_pw`) is encrypted.
- Without an encryption key, `GetEncryptedString` returns the value unchanged and nothing is
  reported as encrypted.
- Provisioning is unchanged: `OpenSVCCreateAppVariableMaps` decrypts a whole `hash_…` value, as
  for a value added through the API.

## Not changed

An app that already holds a clear value keeps it until the value is modified through the API;
the change does not rewrite existing app files. Save-as-template copies the app's stored values,
so a template saved from an app created after this change holds ciphertext.

## Tests

- `cluster/cluster_app_template_secrets_test.go`: the function (literals, per-agent values, empty,
  whole ciphertext, encrypted empty value, bare `hash_`, clear value containing `hash_`, DSN with an
  embedded ciphertext, second pass, nil), `AddSeededApp` from a
  template with every literal, `ImportAppConfig` of a peer export holding the literals (memory
  and saved file).
- `server/api_app_template_secrets_test.go`: reset from a template with every literal.
