# Local backup encryption

## Scope

`backup-encryption` encrypts local backup artifacts written by
Replication Manager. It is independent of Restic repository encryption and
does not change the Restic repository password or its configuration.

The implementation protects artifacts created after the option is enabled:

- physical backups;
- logical backups, including mysqldump, mydumper, dumpling, splitdump, and
  custom save-script output;
- local binlog copies.

Existing plaintext artifacts remain restorable through their existing paths.
Encryption is intentionally limited to Repman-managed local backup artifacts;
it does not encrypt database files in place.

## Configuration and key source

Enable local backup encryption per cluster:

```toml
backup-encryption = true
```

The encryption password is the database root password resolved by
`cluster.GetDbPass()` from `db-servers-credential`. No independent backup
password, environment-variable override, API password, or command-line secret
is supported.

The password is never written to backup metadata, exposed through the API, or
passed to external backup commands by the encryption implementation. It is held
only for the operation that needs it.

### Password history and rotation

Each encrypted backup records the active password version in
`BackupMetadata.EncryptionKeyVersion`. Restore candidates are tried in this
order:

1. the password active at the backup time according to `secret_store.json`;
2. the password matching `EncryptionKeyVersion`, when present;
3. the current database root password;
4. other recorded password versions, newest first.

Consequently, a backup created before a root-password rotation remains
restorable while the corresponding password remains in the configured secret
history. Operators must retain an adequate secret-version history for their
backup retention period.

## Artifact format

### Encryption

Single-file artifacts use the suffix `.enc`; encrypted directory artifacts use
`.tar.enc` and contain a tar stream. Directory encryption writes the tar stream
directly into the cipher and never creates an intermediate plaintext tar file.

The ciphertext is compatible with OpenSSL `enc`:

- file header: `Salted__` followed by an eight-byte salt;
- cipher: AES-256-CBC with PKCS#7 padding;
- derivation: PBKDF2-HMAC-SHA256 with 600,000 iterations;
- OpenSSL-compatible key material: the first 32 bytes are the AES key and the
  next 16 bytes are the CBC IV.

Metadata identifies this format as:

```text
openssl-aes-256-cbc-pbkdf2-sha256-i600000
```

The iteration count is not encoded in the OpenSSL file header. It is therefore
part of the format identifier; a future iteration-count change requires a new
identifier and support for this one.

### Integrity sidecar

Every local encrypted artifact has a mandatory sidecar at:

```text
<artifact>.hmac
```

For example, `mysqldump.sql.gz.enc` is paired with
`mysqldump.sql.gz.enc.hmac`.

The sidecar contents are exactly:

```text
hmac-sha256-v1:<64 lowercase hexadecimal characters>\n
```

The HMAC key is derived with the AES key and IV in one 80-byte PBKDF2 output:

| Bytes | Purpose |
| --- | --- |
| `0..31` | AES-256 key |
| `32..47` | AES-CBC IV |
| `48..79` | HMAC-SHA256 key |

The authenticated input is the exact on-disk encrypted artifact, including the
OpenSSL header and salt, prefixed with the domain separator
`repman-backup-hmac-v1\0`.

`OpenDecryptedFile` reads and strictly parses the sidecar, verifies the full
ciphertext for each candidate password, and releases no plaintext until a
matching HMAC is found. A missing, malformed, unsupported, or mismatched
sidecar returns `backupmgr.ErrBackupIntegrity`.

The direct `DecryptStream` path used for an already-open stream (such as a
Restic dump pipe) retains its streaming behavior: it cannot pre-verify an
adjacent sidecar and does not make a second Restic download solely for HMAC
verification. A local encrypted artifact restored from disk is always checked
by `OpenDecryptedFile` first.

### Manual decryption

The encrypted payload can be decrypted with a stock OpenSSL CLI:

```bash
openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -md sha256 \
  -in mysqldump.sql.gz.enc -out mysqldump.sql.gz
```

This command decrypts the OpenSSL-compatible payload but does not validate the
Repman HMAC sidecar. Use Repman restore paths when integrity verification is
required.

## Publication and cleanup

When encryption is enabled, producers first write plaintext only to a
`.partial` staging path. On successful completion Repman:

1. encrypts the staged file or directory into an encrypted `*.partial` sibling
   with mode `0600`;
2. writes and syncs the paired HMAC sidecar as `*.hmac.partial`;
3. atomically publishes the sidecar and encrypted artifact individually;
4. updates metadata; and
5. removes the plaintext staging artifact.

The final artifact name derives from the logical backup name rather than the
staging name. Artifact encryption failure leaves the source untouched for the
helper, but the calling backup job marks the backup incomplete and removes its
staged plaintext. It raises `WARN0219`; a failed encrypted job is never
published as a successful plaintext backup.

If publishing succeeds but removal of the plaintext staging artifact fails,
Repman retains the valid encrypted artifact and raises `WARN0220` so the
residual plaintext is visible and actionable.

Physical backup job failures that are detected after its receiver has
published an encrypted artifact withdraw both the artifact and its HMAC
sidecar, then mark the metadata incomplete.

Encrypted artifacts and their sidecars move and are removed together during:

- `.old` and `.err` rollback/retention handling;
- backup deletion by ID;
- ad-hoc and binlog retention;
- failed physical-backup withdrawal; and
- startup cleanup of orphaned sidecars.

Discovery and binlog reconstruction ignore `.enc.hmac` files. Stale
`.partial` output is not a backup and is excluded from backup discovery and
Restic tasks.

When encryption is enabled, backup staging and restore directories are
owner-only. Encrypted artifacts and their HMAC sidecars are created with mode
`0600`; temporary restore directories use mode `0700`. Existing backup and
binlog metadata keep their established permissions because they contain no
encryption key material.

## Metadata and restore admission

Successful encrypted backups set:

```text
Encrypted            = true
EncryptionAlgo       = openssl-aes-256-cbc-pbkdf2-sha256-i600000
IntegrityAlgo        = hmac-sha256-v1
EncryptionKeyVersion = <active secret history version>
```

The encryption fields are additive. Existing metadata fields, including legacy
`EncryptionKey`, `Checksum`, `ResticFilePath`, and `BackupStrategy`, remain for
compatibility and are not repurposed to hold secrets.

Before materializing or opening a local encrypted artifact, Repman checks any
metadata matching that exact artifact path. A declared `EncryptionAlgo` or
`IntegrityAlgo` that this build does not support is rejected before plaintext
is released. Metadata is optional for local encrypted binlog copies; they are
accepted when their required HMAC sidecar verifies.

File restores and physical SST sending stream verified plaintext. Directory
restores decrypt and extract into an owner-only temporary directory, reject tar
paths and links that escape it, invoke the existing restore flow, and remove
the temporary directory afterward.

## Physical backup command compatibility

`share/scripts/dbjobs_new.sh` uses explicit long-form connection options for
physical backup tools:

```sh
mariabackup --innobackupex --defaults-file="$MYSQL_CONF/my.cnf" \
  --databases-exclude=.system --protocol=TCP --user="$USER" \
  --host="$MYSQL_SERVER" --password="$PASSWORD" --port="$MYSQL_PORT" \
  --stream=xbstream
```

MariaBackup interprets `-h` as `--datadir`, whereas MySQL client parameters
commonly use `-h` for the host. Its command therefore uses explicit long-form
connection options rather than the generic client parameter string. XtraBackup
8 completion output (`[Xtrabackup] completed OK!`) is recognized so the
encrypted physical-backup failure handling does not withdraw a successful
artifact.

## Validation status

Focused encryption tests cover OpenSSL interoperability, HMAC generation and
verification, missing/malformed/tampered sidecars, password history, metadata
algorithm admission, staging publication, and artifact/sidecar lifecycle.

The following checks pass:

```sh
go test ./utils/backupmgr -count=1 -run 'Test(Encrypt|OpenDecryptedFile|MaterializeDecryptedDirectory|HMAC|OpenSSLCLI|RepmanDecrypts)'
go test -tags server ./cluster -count=1
bash -n share/scripts/dbjobs_new.sh
git diff --check
```

The full `go test ./utils/backupmgr -count=1` suite exceeded the available
command timeout in this environment; focused encryption tests pass. The
release gate remains Docker/regtest coverage against real MariaDB, MySQL, and
Percona source/restore combinations.

## Non-goals

- Re-encrypting existing plaintext backups.
- Replacing or changing Restic repository encryption.
- Adding a separate backup passphrase, KMS/HSM integration, or customer-managed
  keys.
- Authenticating a direct Restic stream with a mandatory second download.
- Eliminating the short plaintext staging interval required by external backup
  tools.
