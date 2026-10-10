# Encrypted tokens embedded in rendered app variables (#1915)

## Problem

Application templates receive secrets through the substitution view: `{{app.db.password}}`,
`{{app.randompassword}}`, `{{apps.#(name==x).randompassword}}`. The view hands them
**encrypted** (`hash_<hex>`, see `Config.GetEncryptedString`). At render time
(`cluster/prov_opensvc_app.go`, the five sites that write secret keys, config keys,
conditional values and the docker command) the value went through
`Config.GetDecryptedPassword`, which decrypts a value only when the **whole** value is the
encrypted string. A secret embedded in a longer value, the PostgreSQL DSN of Mattermost
(`postgres://user:hash_...@prx1:5432/db`), reached the container as ciphertext:
`password authentication failed for user "mattermost2"` (pg-active-passive, 2026-10-07).

## Fix

`Config.GetDecryptedEmbedded(key, value)` replaces every token matching
`hash_[0-9a-fA-F]{34,}` by its decryption and is used at the five render sites. A whole
encrypted value behaves as before.

Limits, by construction of the cipher (`utils/crypto`, AES-CFB, no authentication):

- a token is decrypted when it has at least 34 hex digits (16-byte IV plus data) and an
  even length; the decryptor panics on shorter input, so shorter matches are left as they are;
- a foreign token of a valid length cannot be told from ours: it decrypts to something else.
  A value carrying `hash_` plus 34 hex digits is always treated as one of our secrets.

## Log masking

`dbhelper.CreateAppUser` and `dbhelper.SetAppUserPassword` return the statement to log
with the password **masked** (`*.*`), built where the SQL is built: the PostgreSQL
statements are rebuilt with the mask as literal, the MySQL ones go through
`dbhelper.MaskAppPassword`, which masks the raw password, the doubled-quote literal and
the backslash-escaped spelling, and masks nothing for an empty password. The cluster
code logs what the helper returns, nothing else. Before this, the `ALTER ROLE ... PASSWORD`
of the owned-account re-apply path was logged in clear.

Tests: `config/config_decrypt_embedded_test.go`, `utils/dbhelper/app_db_test.go`.
