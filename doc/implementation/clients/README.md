# Clients Implementation Notes

## `decrypt-backup` quick usage

Use `replication-manager-cli decrypt-backup` to decrypt backup files created with backup encryption.

Examples:

```bash
# Decrypt logical backup (auto output: strips .enc)
replication-manager-cli decrypt-backup --input /var/backups/mysqldump.sql.gz.enc

# Decrypt logical backup with explicit output path
replication-manager-cli decrypt-backup \
  --input /var/backups/mysqldump.sql.gz.enc \
  --output /var/backups/mysqldump.sql.gz

# Decrypt physical backup with explicit output path
replication-manager-cli decrypt-backup \
  --input /var/backups/xtrabackup.xbtream.gz.enc \
  --output /var/backups/xtrabackup.xbtream.gz
```

Notes:

- If `--password` is omitted, the CLI prompts interactively.
- Backups are encrypted with OpenSSL salted passphrase mode (`openssl enc -aes-256-cbc -a -salt -pass pass:<passphrase>`). The CLI uses passphrase decryption by default.
- Legacy backups encrypted with explicit key/IV mode (`-nosalt -K -iv`) are still supported via a fallback path, but this mode is deprecated.
