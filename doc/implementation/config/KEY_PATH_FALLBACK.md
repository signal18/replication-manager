# Config: Monitoring Key Path Fallback

## Scope
This document captures the configuration-side change for monitoring key path fallback behavior.

## Change Summary
- The fallback path for `.replication-manager.key` now uses the current user's home directory (or `monitoring-confdir-extra` when set), instead of the hardcoded `/home/repman` path.
- This avoids permission errors when running under non-root users.

## Behavior
- If the configured key path is not writable, replication-manager attempts the fallback path:
  - `$HOME/.config/replication-manager/.replication-manager.key`
  - or `monitoring-confdir-extra/.replication-manager.key` when set.
- The fallback directory is created if missing, and the key is generated in that location.
