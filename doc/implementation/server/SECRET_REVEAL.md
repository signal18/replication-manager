# One-time secret links

*Status: implemented (`server/server_secret_reveal.go`).*

A password reaches a person by mail **as a one-time link, never in the mail**. A mail is copied,
forwarded, indexed and kept in mailboxes for years; a link that works once and expires is not.

## Who uses it

- **Password rotation** (`POST /api/clusters/{cluster}/users/rotate-password`). When the rotated
  user's login is an email, the new password is mailed to that user as a link, and the answer no
  longer carries it (`password` empty, `revealMailedTo`, `revealExpires`): who rotated it does not
  see it, so a ban by a sysops leaves the sysops without the new password. A login with no address
  (`admin`), no mail server, or the links off: the answer carries the password, shown once, as
  before, with `revealError` saying why it was not mailed. The mail is sent after the rotation,
  outside its lock: a slow mail server holds no other rotation.
- **Credentials mails** (`users/send-credentials`, and the sponsor's credentials mail). The
  connection details stay in the mail; the password is behind a link, **one per recipient** (a
  link opened by one recipient shows nothing to the other). Before, these mails carried the
  database password in clear. With the links off or impossible, the mail is not sent: a password
  is never mailed in clear (security laws outrank the convenience of the mail).
- **The cluster's Vault token** (`POST /api/clusters/{cluster}/send-vault-token`): to each
  mail-to address (the cluster's external users left out), the Vault address and the token behind
  a link of its own. It was mailed in clear before (`SendVaultTokenByMail`, removed).

## How it works

- `newSecretReveal(for, cluster, title, items)`: 32 random bytes, base64url, as the code. The store
  keeps only its SHA-256 and the secrets, in `WorkingDir/secret-reveals.json`, the whole file
  encrypted with the instance's key, mode 0600, rewritten atomically (as `api-tokens.json`). It is
  not in the config repo's staged files (T25): never pushed.
- The link is `<api-public-url>/reveal#<code>`. The code is in the **fragment**: a browser never
  sends it to the server or a proxy, so it is in no access log. The page takes it out of the
  address bar at once and asks for the secret only on a click: a mail scanner that opens the link
  burns nothing and sees nothing.
- `POST /api/secret-reveal {code}`: public (no login, the code is the credential), answers the
  secrets once and deletes the reveal before answering; a second open, an expired or an unknown
  code get the same 404. `Cache-Control: no-store`. Each open and each refusal is a security event
  (who the reveal was for, never a value).
- Bounded (F3, F4): expired reveals are dropped at every write of the store, and at most 256 are
  kept, the oldest dropped first. An expired reveal stays on disk, encrypted, until the next write;
  it cannot be opened.
- The open route is public: an index of the stored hashes and their expiry is kept in memory, so an
  unknown or expired code is refused without the disk or the store's lock (a flood of junk codes
  costs a map lookup), and the refusals make one security event a minute at most, with their count.
- No link without a usable address: `api-public-url` must be an http(s) address with a host, else no
  reveal is stored and the rotation shows the password once. A link whose mail fails is deleted.

## Settings (server scope, global settings)

- `api-secret-reveal` (default on): the switch (T14). Off: rotations show the password as before,
  credentials mails are refused.
- `api-secret-reveal-expire-hours` (default 24).
- The link is built on `api-public-url`: it must be the address the recipients reach.
