# Local backup encryption plan

## Status and scope

This is the implementation plan for encrypting **local backup artifacts**. It
does not replace, configure, or depend on Restic repository encryption.

The first plan is for **which encryption key to use**. The requested policy is
deliberately simple:

- The encryption passphrase is the **database root password**, resolved via
  `cluster.GetDbPass()` (the password portion of Repman's
  `db-servers-credential` config field). This is the one shared passphrase for
  local backup artifacts and any remote-backup workflow the operator
  configures with the same root password.
- Deployments enabling this feature must configure `db-servers-credential` as
  the database root credential; that requirement is how the shared root
  password reaches Repman.
- This is the **only** passphrase source in the first delivery. Do not add an
  environment override, dedicated backup passphrase, API-password fallback, or
  fallback to an individual server's transient credential.
- Do not add a second local-backup passphrase, key ring, key identifier, or key
  rotation workflow in this delivery.
- Do not log, persist in metadata, expose through the API, or pass the
  passphrase as a process argument.
- This plan does not alter an existing Restic repository password. If an
  operator wants the same secret for Restic and local recovery, that remains an
  explicit operator configuration choice.

Existing plaintext backups stay supported. Encryption applies only to newly
created artifacts after the feature is enabled.

## Current code paths

The local backup directory is returned by `GetMyBackupDirectory()` in
`cluster/srv_job_backup.go`. It currently creates the directory with
`os.ModePerm`, and normal backup artifacts are written there in plaintext.

The main producers are:

| Artifact | Producer | Current output |
| --- | --- | --- |
| Physical backup | `JobBackupPhysicalWithOptions()` | `xtrabackup` or `mariabackup` `.xbstream[.gz]` file |
| Mysqldump | `JobBackupLogicalWithOptions()` / `JobBackupMysqldump()` | `mysqldump.sql.gz` or splitdump directory |
| Mydumper and Dumpling | `JobBackupMyDumper()` / `JobBackupDumpling()` | directory tree |
| Custom save script | `JobBackupScript()` | artifact at Repman's supplied destination |
| Binlog copy | `JobBackupBinlog()` | raw local binlog file |

`JobFinishReceiveFile()` currently writes physical-backup metadata and queues
Restic after the receiver finishes. Logical backup completion follows the same
publication order: artifact first, then metadata, then optional Restic.

`backupmgr.BackupMetadata` already carries `Encrypted` and `EncryptionAlgo`,
and `restore_catalog.go` already exposes the `encrypt` capability. No producer
currently sets the fields and no restore path decrypts an artifact.

## Design

### Encryption format

Use the Go `filippo.io/age` library with an scrypt passphrase recipient. The
output is an authenticated streaming `age` file, so a wrong password or a
modified artifact fails before it can be restored.

The passphrase is resolved in memory with `cluster.GetDbPass()`. It is supplied
to the Go writer/reader only; no external encryption command is invoked.

Final artifact names are:

| Source shape | Encrypted artifact |
| --- | --- |
| Single file | `<source>.age` |
| Directory | `<source>.tar.age` |
| Binlog | `<binlog>.age` |

Directories are archived with Go's `archive/tar` and then encrypted. This
preserves the complete tree while presenting one atomic encrypted artifact.

### Publication rules

1. A producer writes its normal artifact into the server backup directory.
2. Repman creates an encrypted `*.partial` sibling with mode `0600`.
3. Repman completes and closes the encrypted file, then renames it atomically
   to the final `.age` or `.tar.age` path.
4. Only after that rename does Repman remove the plaintext source.
5. Repman updates `BackupMetadata.Dest`, sets `Encrypted=true` and
   `EncryptionAlgo="age-scrypt"`, writes metadata, and optionally queues
   Restic.

If encryption fails, Repman keeps the source artifact, records the backup as
failed, and does not create a successful encrypted metadata record or Restic
snapshot. It must never delete the only restorable copy.

The first version protects retained local artifacts. There is necessarily a
short plaintext staging period while database backup tools produce their data.
The backup directory and temporary restore directories must therefore be
owner-only (`0700`), and artifacts/metadata must be `0600`.

## Implementation steps

### 1. Configuration and security plumbing

Add one dynamic TOML/Viper flag, following the existing backup-encryption
naming already used in the repository's in-flight work:

```toml
backup-encryption-enabled = false
```

Add `BackupEncryptionEnabled` to `config.Config`, register
`--backup-encryption-enabled` in `server.AddFlags`, and make it available
through the existing cluster settings API and React backup settings page.

The API and GUI expose only the enable switch and a read-only description:
`Encryption key source: database root password`. They never accept, display, or
return the password.

When the switch is on, resolve the password only through
`cluster.GetDbPass()` and reject an empty result before starting a backup.
Return a clear non-secret error when it is absent. The switch is the required
off-switch: disabled preserves today's plaintext behavior.

Files to change:

- `config/config.go`
- `server/server.go`
- `server/api_cluster.go`
- `share/dashboard_react/src/Pages/Settings/BackupSettings.jsx`
- `share/dashboard_react/src/redux/settingsSlice.js`
- `share/dashboard_react/src/services/settingsService.js`

### 2. Shared artifact helper

Add `utils/backupmgr/local_encryption.go` and
`utils/backupmgr/local_encryption_test.go`.

The package owns one implementation for:

- `EncryptFile(source, destination, passphrase)`
- `EncryptDirectory(sourceDir, destination, passphrase)`
- `OpenDecryptedFile(source, passphrase)` for streaming restores
- `MaterializeDecryptedDirectory(source, tempBase, passphrase)` for directory
  and physical restores

Each helper must reject symlinks escaping the source directory, preserve useful
file modes in tar entries, close every writer before rename, and return cleanup
functions for temporary plaintext material. All errors must identify an artifact
path but never contain passphrase material.

Do not add encryption logic separately to each backup tool and do not reuse
`utils/crypto.Password`, which is a configuration-string helper rather than a
streaming authenticated file format.

### 3. Encrypt before metadata and Restic

Create one `ServerMonitor` finalization helper in `cluster/srv_job_backup.go`.
It accepts `*backupmgr.BackupMetadata`, detects file versus directory output,
performs encryption, updates `Dest` and encryption fields, and returns an error
without publishing partially encrypted metadata.

Call it at these points:

- After physical receive completion in `JobFinishReceiveFile()`, before
  `WriteBackupMetadata()` and `BackupRestic()`.
- After successful logical output generation in
  `JobBackupLogicalWithOptions()`, before `WriteBackupMetadata()`.
- After a custom save script succeeds, after verifying the supplied destination
  exists.
- After each successful binlog copy in `JobBackupBinlog()`, before binlog
  metadata is persisted and before an optional Restic snapshot.

The `.old`/`.err` retention transitions must use the final encrypted path. Do
not leave an unencrypted previous artifact behind because a new encrypted backup
was created.

Restic remains optional. When enabled, it must run only after local encryption
has completed, so it snapshots the encrypted artifact and its metadata rather
than the plaintext staging output.

### 4. Metadata and catalog

Extend `backupmgr.BackupMetadata` only with non-secret format data if needed,
for example `EncryptionFormat`. Continue using the existing `Encrypted` and
`EncryptionAlgo` fields. `EncryptionKey` must remain empty: it must never be
used to store the database password or a derived key.

Update metadata write/read tests and the restore catalog so that only completed
encrypted artifacts receive the `encrypt` capability. Existing metadata with
unset encryption fields remains valid and represents a plaintext backup.

Files to change:

- `utils/backupmgr/backup.go`
- `cluster/backup_helpers.go`
- `cluster/restore_catalog.go`

### 5. Restore and reseed

Add a single restore-artifact resolver at the boundary where a local path is
selected. It must inspect metadata first, then use the `.age` suffix as a
fallback for an artifact selected by path.

- Mysqldump restores decrypt as a stream into the existing MySQL restore reader;
  do not write a second plaintext dump to disk.
- Splitdump, Mydumper, Dumpling, River, and physical backups decrypt/extract to
  an owner-only temporary directory, pass that directory to the existing
  restore/reseed function, then remove it through a deferred cleanup.
- Physical reseed and flashback must not set their success state until decrypt
  and extraction complete.
- Binlog/PITR code must decrypt each selected binlog before replay while
  retaining the original logical binlog name for ordering and position logic.

The existing unencrypted restore paths must remain unchanged when metadata says
`Encrypted=false`.

Files to change:

- `cluster/srv_job_backup.go`
- `cluster/srv_job_restic.go` where a local artifact is selected after a Restic
  restore
- `cluster/restore_catalog.go`
- relevant binlog/PITR helpers in `cluster/`

### 6. Permissions, state, GUI, and documentation

- Change local backup-directory creation from `os.ModePerm` to `0700` and write
  metadata as `0600`.
- Emit task-domain state and redacted task logs for encryption failure; do not
  silently fall back to a plaintext published backup when encryption is enabled.
- Add the GUI switch alongside backup settings, with a confirmation warning
  that local backups will use the database root password; the operator must
  ensure `db-servers-credential` is configured with that root credential.
- Add a user-facing documentation page covering enablement, recovery with
  `age`, the short staging exposure, and the fact that changing the DB root
  password makes older encrypted backups unavailable in this first delivery.
- Keep this document as the technical design record.

## Test plan

### Unit tests

- Single-file and directory round trips.
- Empty, large, and compressed inputs.
- Wrong password, damaged header, damaged ciphertext, and incomplete `.partial`
  file handling.
- No plaintext artifact removal on encryption failure.
- `0600` artifacts and metadata plus `0700` directories.
- Metadata/capability compatibility for plaintext and encrypted backups.

### Cluster tests

- Mysqldump file, splitdump, Mydumper, Dumpling, physical backup, custom script,
  and binlog encryption publication order.
- Logical and physical reseed from encrypted local artifacts.
- Encrypted binlog replay/PITR ordering.
- Restic is queued only after encryption succeeds.
- Disabled switch keeps existing output names and restore behavior unchanged.

### Docker/regtest release gate

Run an actual MariaDB/MySQL/Percona scenario that creates encrypted logical and
physical local backups, reseeds a node from each, and validates data and
replication afterward. Add a binlog/PITR scenario when binlog encryption lands.

## Explicit non-goals for this delivery

- Key rotation, key IDs, key rings, and re-encryption of existing artifacts.
- Migration of existing plaintext backups.
- Changing Restic's repository encryption model or password.
- Encrypting database server files in place.
- Eliminating the temporary plaintext staging period produced by external backup
  tools.

## Repository coordination before implementation

Tracked by the existing labelled issue #1110 ("Backup encryption in
maintenance jobs"); no new issue needed. Implementation continues on
`feat/local-backup-encryption`, branched fresh off `origin/develop`.

The `backup-encryption` branch is an earlier, superseded exploration
(OpenSSL AES-256-CBC, an explicit `backup-encryption-passphrase`/`-keyring`
config override, per-file restore mode, a CLI decrypt command). It
contradicts this delivery's single-key/no-override/no-keyring policy above
and must not be merged forward as-is; treat it as reference only, not a base
to build on.
