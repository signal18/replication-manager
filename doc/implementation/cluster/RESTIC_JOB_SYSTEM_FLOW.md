# Restic Job System Flow

## Overview

This document summarizes the job-system changes introduced in `cluster/srv_job.go` on
`feature/restic-04-job-system`. It focuses on payload support, restic-backed reseed
flows, and backup option handling. The intent is to clarify execution flow and
backward-compatibility expectations for maintainers.

## Key Changes

- Jobs table now includes a `payload` column (MEDIUMTEXT) for task-specific metadata.
- Job inserts support optional payloads via `JobInsertTaskWithPayload`.
- Backup jobs accept `BackupRunOptions` to control backup line, retention
  (duration or days), and restic enablement per run.
- Restic reseed flows support dump/restore/mount modes with payload validation.
- Restic backup state is tracked per method (logical/physical) to keep lock state
  consistent across async restic callbacks.

## Flow Summary

### Jobs Table
1. `jobsCreateTable()` ensures the table has required columns (`payload` included).
2. `JobsUpdateEntries()` reads `payload` into `config.Task.Payload`.
3. `JobInsertTaskWithPayload()` routes to `jobInsertTask()` with optional payload.

### Backup Jobs
- `JobBackupLogicalWithOptions()` and `JobBackupPhysicalWithOptions()` normalize
  backup line selection and set `BackupMeta` fields including `RetentionDuration`,
  `RetentionDays`, and `ResticEnabled`.
- If restic is enabled, the restic flag is set immediately to avoid an "idle gap"
  between backup completion and restic queueing.

### Restic Backup
- `BackupRestic()` queues restic backups and uses async callbacks to clear restic
  flags and update metadata when the snapshot is created.

### Restic Tagging Configuration
- `backup-restic-tags`: comma-separated tag templates. Each template can include
  placeholders and is rendered per backup. Templates with missing values are
  skipped. Compact form is supported (e.g., `cluster,backup-type,line`).
- `backup-restic-host`: optional override for restic `--host` during backup.
  Leave empty to use restic's default hostname (no alias).

Supported placeholders for templates:
- `{tenant}`, `{cluster}`, `{engine}`, `{version}`, `{backup-type}`, `{backup-tool}`, `{line}`

Behavior notes:
- Unknown placeholders log a warning and skip that tag.
- Empty values result in the tag being skipped.
- Template entries without `{}` are treated as:
  - a literal tag if they contain `:`
  - a shorthand for `{key}` when the entry matches a supported key
- Template entries wrapped in single or double quotes are treated as literal tags
  (quotes are stripped). Commas inside quoted entries are allowed.
- Default configured tag set:
  `tenant,cluster,engine,version,backup-type,backup-tool,line`
- The `line` tag is always emitted as `line:default` or `line:adhoc`.
- Older snapshots without a `line` tag are treated like `line:default` by purge policies (they are not protected by `line:adhoc` keep-tag).

### Restic Purge Grouping
- `backup-restic-purge-group-by` maps to restic `forget --group-by`. Default is `host,paths` for stable grouping.
- Leave empty to use restic defaults.
- Use `default` to use restic defaults explicitly.
- Use `none` to disable grouping and treat all snapshots as one group.
Examples:
- `backup-restic-purge-group-by = "host,paths"` (group by host + paths, default)
- `backup-restic-purge-group-by = "host,tags"` (group by host + tags)
- `backup-restic-purge-group-by = "default"` (restic defaults)
- `backup-restic-purge-group-by = "none"` (single group)

### Restic Purge Keep Tags
- `backup-restic-purge-keep-tag` sets `forget --keep-tag` values.
- Use this to protect ad-hoc snapshots from global purge policies (default: `line:adhoc`).
- Keep-tag templates only support `{cluster}` and `{tenant}` placeholders; other placeholders
  are rejected. Quote a tag to keep it literal.
- Separate keep-tag filters with spaces (commas are also accepted for legacy configs).
  Quote a tag to include commas for AND semantics in restic.

### Reseed Jobs
- Physical reseeds can be driven by restic payloads (restore or mount) with optional
  override paths.
- Logical reseeds support both ad-hoc reseed by metadata ID and restic-based restore
  or mount (mysqldump and mydumper).

## Function Review Notes

### jobsCreateTable()
- Adds `payload` column and ensures table compatibility by altering existing schemas.
- Backward compatible: existing databases get a non-breaking `ALTER TABLE`.

### JobsUpdateEntries()
- Scans `payload` into runtime tasks; existing rows without payload are safe.

### jobInsertTask()
- Uses parameterized SQL for insert with optional payload.
- Reuses previous ID for same task to keep single-row semantics.

### JobBackupPhysicalWithOptions()
- Honors `BackupLine` and retention policy (`RetentionDuration` or `RetentionDays`).
- Skips `.old` rotation for ad-hoc backups (no false overwrite).

### JobBackupLogicalWithOptions()
- Sets restic logical flag early to avoid a lock gap.
- Preserves existing behavior when restic is disabled.

### BackupRestic()
- Async callback updates metadata on completion.
- Uses per-method restic flags for logical vs physical backups.

## Compatibility

- Schema change is additive (`payload` is nullable); no existing data is invalidated.
- New task payload usage is optional; legacy callers can keep using `JobInsertTask()`.
- Backup options default to existing behavior when not provided.
- Restic-specific behavior only activates when `BackupRestic` is enabled.

## Known Follow-ups

- None currently tracked. Mysqldump file handling and close semantics have been
  corrected in the restic-04 branch.

## Test Scenarios

Restic purge coverage validates group-by and retention policy combinations to
ensure snapshot retention remains stable across configurations.
