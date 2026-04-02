Backup Encryption Implementation Plan (Single-File Scope)

Status: Draft

Date: 2026-03-11

Source ADR: doc/implementation/backup/backup-encryption.md

Scope decision

- Implement encryption only for single-file backups in phase 1.
- Included:
  - Physical backups that produce one file (for example .xbstream and .xbstream.gz).
  - Logical backups that produce one file (for example mysqldump.sql.gz and backup-save-script file outputs).
- Excluded in phase 1:
  - Directory-based logical backups (mydumper, dumpling, splitdump directories).
  - For excluded modes, emit clear WARN logs and continue without encryption.

Goals

- Encrypt eligible backups immediately after creation and before restic upload.
- Reuse the same key and IV derivation already used by encrypted write-log payloads.
- Keep existing backup flow unchanged when encryption is disabled.
- Keep restore behavior explicit: encrypted backups must be decrypted before import/reseed.

Non-goals (phase 1)

- No new key management system.
- No migration of existing unencrypted backups.
- No archive-and-encrypt implementation for directory backups.

Current code touchpoints

- Backup orchestration:
  - cluster/srv_job_backup.go
    - JobBackupLogicalWithOptions
    - JobFinishReceiveFile (physical receive completion)
- Existing decryption flow used as reference:
  - cluster/srv_job_logs.go (DecryptAES256)
  - server/api_database.go (SHA256/MD5 derivation from node password)
- Hash helpers:
  - utils/crypto/crypto.go (GetSHA256Hash, GetMD5Hash)
- Runtime/config surfaces:
  - config/config.go (Config struct)
  - server/server.go (CLI flags)
  - server/api_cluster.go (runtime switch/set handling)
- Restic target verification fallback logic:
  - cluster/srv_job_restic.go (alternateCompressionPath)

Phase 1: Config and feature gating

1) Add configuration field

- Add `BackupEncryptionEnabled bool` to `config.Config` with:
  - `mapstructure:"backup-encryption-enabled"`
  - `toml:"backup-encryption-enabled"`
  - `json:"backupEncryptionEnabled"`
- Add `BackupEncryptionPassphrase string` to `config.Config` with:
  - `mapstructure:"backup-encryption-passphrase"`
  - `toml:"backup-encryption-passphrase"`
  - `json:"backupEncryptionPassphrase"`

2) Add CLI flag

- In `server/server.go`, add:
  - `--backup-encryption-enabled`
  - `--backup-encryption-passphrase`
  - Help text explaining it applies to single-file backups only in phase 1.

3) Add runtime API handling

- In `server/api_cluster.go`:
  - Add toggle route handling for `backup-encryption-enabled`.
  - Add set-setting support so API set commands can enable/disable it.

Acceptance criteria

- Flag appears in server help.
- Setting is persisted in config output and visible in API JSON.
- Runtime toggle works like other backup booleans.

Phase 2: Encryption primitive and shared helper

1) Add encryption helper mirroring log decryption style

- Add method in cluster backup path (or shared helper in cluster package) that runs:
  - `openssl aes-256-cbc -e -a -nosalt -K <sha256_hex> -iv <md5_hex>`
- Keep behavior symmetric with existing `DecryptAES256` implementation.

2) Derive key and IV from server password

- Resolve passphrase with precedence:
  - `REPLICATION_MANAGER_BACKUP_PASSPHRASE` environment variable
  - `backup-encryption-passphrase` cluster config/flag value
  - admin user password from `api-credentials`
  - admin user password from `api-credentials-external`
  - fallback to `server.Pass` for backward compatibility
- Reuse:
  - `crypto.GetSHA256Hash(passphrase)` as key
  - `crypto.GetMD5Hash(passphrase)` as IV

3) Add file-level helper for single files

- Steps:
  - Read source file bytes.
  - Encrypt using helper.
  - Write `<source>.enc`.
  - Validate output file exists and is non-empty.
  - Remove plaintext source file only after successful write/validation.

4) Metadata update rules

- On success, set backup metadata fields:
  - `Encrypted = true`
  - `EncryptionAlgo = "aes-256-cbc"`
  - `Dest = <source>.enc`
- Do not store raw key material in metadata.

Acceptance criteria

- Encrypted output is base64 OpenSSL output and decryptable with documented command.
- Plaintext file is removed only after encrypted artifact is verified.
- Metadata points to encrypted path.

Phase 3: Integrate logical backup path

1) Integration point

- In `JobBackupLogicalWithOptions`, after backup generation succeeds and before metadata write/restic scheduling:
  - If encryption disabled: no change.
  - If enabled and destination is a single file: encrypt and update metadata.
  - If enabled and destination is a directory: log WARN and continue without encryption.

2) Supported logical modes in phase 1

- Supported:
  - mysqldump single-file output
  - backup-save-script when destination resolves to a file
- Not supported in phase 1:
  - mydumper, dumpling, splitdump directory output

Acceptance criteria

- Logical single-file backups end as `.enc` artifacts.
- Directory logical backups complete normally with explicit WARN explaining encryption skip.
- Restic, when enabled, uses encrypted destination path.

Phase 4: Integrate physical backup path

1) Integration point

- In `JobFinishReceiveFile` for physical tasks:
  - After receive completes and metadata is available.
  - Before restic scheduling.
  - Encrypt received single-file artifact, update metadata, then proceed.

2) Failure handling

- If encryption fails:
  - Keep plaintext file intact.
  - Mark backup as failed/incomplete where appropriate.
  - Skip restic upload for that failed encrypted run.

Acceptance criteria

- Physical backup file is replaced by `.enc` when enabled.
- No restic snapshot is created from plaintext when encryption is required and fails.

Phase 5: Restic compatibility adjustments

1) Path fallback awareness

- Extend fallback logic in `alternateCompressionPath` to handle encrypted suffixes:
  - `.gz.enc` <-> `.enc` variants when checking for target presence.
  - Preserve existing `.gz` fallback behavior.

2) Restic source selection

- Ensure restic path selection relies on updated metadata destination, not stale plaintext path.

Acceptance criteria

- Restic backup verification can resolve encrypted naming variants.
- Restic path resolution works for encrypted files in both scheduled and ad-hoc flows.

Phase 6: Decryption tooling and documentation

1) Decryption utility

- Add CLI command `replication-manager-cli decrypt-backup` that:
  - Accepts `--input`, `--output` (optional), and `--password` (optional prompt when omitted).
  - Runs passphrase-based OpenSSL decrypt command by default.
  - Falls back to legacy SHA256/MD5 key/IV mode for older artifacts (deprecated).
  - Writes output file (default: strip `.enc`).
  - Example:
    - `replication-manager-cli decrypt-backup --input /var/backups/mysqldump.sql.gz.enc --output /var/backups/mysqldump.sql.gz`
  - OpenSSL equivalent (primary):
    - `openssl enc -d -aes-256-cbc -pass pass:<passphrase> -in backup.sql.gz.enc -out backup.sql.gz`
  - OpenSSL equivalent (legacy fallback):
    - `openssl aes-256-cbc -d -a -nosalt -K <sha256_hex_key> -iv <md5_hex_iv> -in backup.sql.gz.enc -out backup.sql.gz`

2) Documentation updates

- Update backup/restore docs to state:
  - Encrypted backups require decryption before import/reseed.
  - Directory-based logical backups are not encrypted in phase 1.
  - Password rotation implications for historical backup decryptability.

Acceptance criteria

- Operator can decrypt a produced `.enc` backup and restore successfully.
- Docs include exact command examples and caveats.

Test and validation plan

Unit-level

- Encryption helper tests:
  - Encrypt known plaintext, decrypt with OpenSSL command, verify byte-equivalence.
  - Error path coverage (missing openssl, empty input, write failure).
- Metadata tests:
  - `Encrypted`, `EncryptionAlgo`, and `Dest` are set correctly on success.

Workflow/integration-level

- Logical mysqldump with encryption enabled:
  - Output file ends with `.enc`.
  - Plaintext file removed.
  - Restic path uses encrypted artifact.
- Physical backup with encryption enabled:
  - Received file converted to `.enc` before restic.
- Directory logical mode with encryption enabled:
  - Backup succeeds, no encryption performed, WARN emitted.

Negative tests

- Wrong password decryption fails.
- Encryption command failure does not delete plaintext source.
- Encryption enabled + no server password returns actionable error.

Operational safeguards

- Log all encryption state transitions with module `config.ConstLogModTask` or backup stream module.
- Never remove plaintext before encrypted file is fully written and validated.
- Keep behavior backward compatible when `backup-encryption-enabled=false`.

Risks and mitigations

- Risk: CPU overhead on large files.
  - Mitigation: document impact, keep feature opt-in.
- Risk: password changes make old backups undecryptable without old password.
  - Mitigation: document password rotation policy and retention of historical secrets.
- Risk: partial files on crash during encrypt/write.
  - Mitigation: write to temp file, fsync/rename to final `.enc` where practical.

Rollout plan

1) Land config/flag and helper primitives.
2) Land logical single-file integration.
3) Land physical integration.
4) Land restic compatibility updates.
5) Land decrypt helper + docs.
6) Enable in selected non-production clusters and observe backup/restore cycle.

Definition of done

- End-to-end backup with encryption enabled produces decryptable `.enc` artifacts for supported single-file flows.
- Restic archives encrypted artifacts only for supported encrypted runs.
- Restore runbook includes decrypt step and validated example commands.
- Automated tests cover success, skip, and failure paths described above.
