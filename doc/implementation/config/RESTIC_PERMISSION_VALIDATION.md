# Restic Permission Validation

## Summary

Restic permission modes accept only octal digit values in the 6xx or 7xx range
(e.g., 600, 700, 750, 755). This keeps configuration consistent with OpenSVC's
permission inputs (string values like "700"/"600").

## Entry Points

- config/config.go
  - parseResticMode() converts octal digit values to os.FileMode.
  - ValidateResticPermissions() enforces allowed ranges and returns
    configuration validation errors.
- server/api_cluster.go
  - API updates for backup-restic-dir-mode and backup-restic-file-mode
    validate inputs and return errors if invalid.
- cluster/cluster_bck.go
  - StartResticManager() calls ValidateResticPermissions() and logs a warning
    on invalid inputs.

## Rules

- Valid values: 600-777 (octal digits only)
- Zero value: 0 means "use defaults" (0700 for dirs, 0600 for files)
- Invalid values (examples): 400, 888, -1, 0o700 (parsed to 448 and rejected)

## Behavior

- API updates with invalid values return errors and do not apply changes.
- Invalid values from config files are rejected by validation; defaults are
  used when values are zero or invalid.

## Rationale

- Aligns with OpenSVC permission formatting (string octal digits).
- Avoids ambiguous interpretation of decimal values.
- Ensures stronger defaults for security (owner-only permissions).

## Examples

Valid:
- backup-restic-dir-mode = 700
- backup-restic-file-mode = 600

Invalid:
- backup-restic-dir-mode = 400
- backup-restic-file-mode = 888

