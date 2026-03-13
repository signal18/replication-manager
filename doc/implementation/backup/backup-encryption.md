ADR‑001: Encrypting Backup Files Using AES‑256‑CBC
Status

Proposed – 2026‑03‑11

Context

The replication-manager project performs regular backups of MariaDB/MySQL clusters. Both physical and logical backups are orchestrated from the cluster/srv_job_backup.go module. Backups are typically compressed (using gzip) and then stored locally and/or uploaded to remote storage (e.g., via restic). Unlike logs, these backups are not encrypted, which means sensitive database contents remain in plain text on disk or in transit.

Meanwhile, the API endpoint used for writing logs (api_database write‑log) requires clients to submit encrypted payloads. The server decrypts these payloads by computing a 256‑bit key from the server’s password using SHA‑256 and a 128‑bit initialization vector (IV) from the same password using MD5, then invoking openssl aes‑256‑cbc to decrypt the base64‑encoded payload. The key and IV derivation is implemented in utils/crypto/crypto.go, where GetSHA256Hash and GetMD5Hash return the hexadecimal hashes of a string. Backup encryption is intentionally separate so we can use OpenSSL’s salted passphrase mode for streaming backups while keeping log encryption unchanged.

Decision

We will add optional encryption for backup files. When enabled, backups will be encrypted immediately after compression and before storage or upload. The encryption mechanism uses OpenSSL’s salted passphrase mode for streaming encryption, matching the server backup flow:

Passphrase – Use the configured backup encryption passphrase (resolved per existing precedence).

Encryption algorithm – Use AES‑256 in CBC mode, base64‑encode the output, and include OpenSSL’s salt. In terms of tooling this is equivalent to running:

openssl enc -aes‑256‑cbc -a -salt -pass pass:<passphrase> -in <backup_file> -out <backup_file>.enc

Decryption for this format is the symmetric opposite:

openssl enc -d -aes‑256‑cbc -a -pass pass:<passphrase> -in <backup_file>.enc -out <backup_file>

File naming – Encrypted backups will receive an .enc suffix (e.g., backup.sql.gz.enc or xtrabackup.xbstream.enc) so operators can distinguish them from unencrypted files.

Configuration – Introduce a configuration flag (e.g., BackupEncryptionEnabled / `backup-encryption-enabled`) and a passphrase setting (`backup-encryption-passphrase`). When encryption is enabled, passphrase resolution order is: `REPLICATION_MANAGER_BACKUP_PASSPHRASE` env var, then `backup-encryption-passphrase`, then admin password from `api-credentials`, then admin password from `api-credentials-external`, then server password fallback for backward compatibility.

Decryption utilities – Provide a helper command (`replication-manager-cli decrypt-backup`) that accepts an encrypted backup and a password, runs passphrase decryption by default, and falls back to the legacy key/IV scheme for older artifacts:

openssl enc -d -aes‑256‑cbc -a -pass pass:<passphrase> -in <backup_file>.enc -out <backup_file>

Example CLI usage:

replication-manager-cli decrypt-backup --input /var/backups/mysqldump.sql.gz.enc --output /var/backups/mysqldump.sql.gz

This ensures that restore procedures can decrypt the backup before feeding it into the database.

Legacy compatibility note – Older backups that used explicit key/IV mode remain decryptable via a fallback path, but this mode is deprecated and planned for removal once legacy artifacts are retired.

Implementation notes

- In cluster/srv_job_backup.go, after a backup file is created and compressed, check the BackupEncryptionEnabled flag. If enabled, call the OpenSSL streaming helper with passphrase (`openssl enc -aes-256-cbc -a -salt -pass pass:<passphrase>`), write the encrypted output to a new file with the .enc extension, and remove (or securely delete) the unencrypted file.

When using restic or other remote storage tools, upload the encrypted file instead of the plaintext backup. This preserves end‑to‑end encryption over the transport.

Update documentation and restore scripts to reflect the need to decrypt the backup before import.

Consequences

Security improvement – Sensitive data in backups will be protected at rest and in transit. This aligns backup handling with the encryption model already used for log ingestion.

Uniform key management – Using a single backup passphrase avoids introducing additional keys. Operators must safeguard the passphrase, as it controls access to backups.

Performance impact – Encryption and decryption add CPU overhead during backup and restore operations. Large physical backups may take longer to process. Proper resource planning is necessary.

Compatibility considerations – Old backups created without encryption remain unencrypted. If the backup passphrase changes, backups encrypted with the previous passphrase can only be decrypted with that old passphrase. Administrators should document passphrase changes and maintain secure records for decryption.

Implementation complexity – Additional code is required to perform encryption and handle configuration. Thorough testing is necessary to ensure that encryption integrates correctly with existing backup workflows and remote storage mechanisms.

Alternatives considered

Do nothing – Leaving backups unencrypted would keep the current implementation simple but exposes data to potential compromise if storage media or transfers are intercepted.

Use a different encryption scheme – Options like RSA public‑key encryption or generating a random AES key per backup were considered. However, these approaches would necessitate key management infrastructure and complicate restores. Leveraging the existing AES‑256‑CBC scheme and deriving keys from the password provides consistency and reuses proven code.
