# Schema Scan Refactor Plan

## Goals
- Reduce metadata lock pressure by avoiding per-table SHOW CREATE TABLE.
- Minimize round trips with bulk information_schema queries.
- Fix correctness issues (pointer bug, repeated hashing, O(n^2) index assembly).
- Keep behavior compatible across MySQL/MariaDB and PostgreSQL where possible.
- Add richer table metadata to Table objects while keeping volatile fields out of CRC.

## Scope
- Files: utils/dbhelper/schema.go, utils/dbhelper/types.go, cluster/cluster.go,
  utils/dbhelper/schema_test.go.
- Add: doc-only plan is this file, optional helper LoadDDLForDiff.

## Planned Changes
1. Bulk-load tables, columns, and indexes in a single pass per metadata source.
   - getAllTables(): information_schema.TABLES with extended fields.
   - loadAllColumns(): information_schema.COLUMNS for all non-system schemas.
   - loadAllIndexes(): information_schema.STATISTICS for all non-system schemas.

2. Use short context timeouts for metadata queries.
   - QueryxContext/SelectContext with a dedicated short timeout.
   - No explicit transactions (autocommit only).

3. Fix pointer bug in tablemap construction.
   - Use &tables[i] rather than &t range variable.

4. Replace per-schema deferred hashing with a single final phase.
   - Hash columns and indexes once after all metadata is loaded.
   - Compute TableCrc from structured metadata (schema, name, engine, row_format,
     table_collation, create_options, plus ordered columns and indexes).
   - Exclude volatile fields (table_rows, data_length, index_length,
     auto_increment) from TableCrc.

5. Index assembly performance fix.
   - Use per-table map[index_name]index_position to avoid O(n^2) scans.

6. Logging allocations.
   - Replace string concatenation with strings.Builder for query logs.

7. Optional DDL fetch for human-readable diffs.
   - Add LoadDDLForDiff(ctx, db, myver, schema, table).

## Compatibility Notes
- PostgreSQL path keeps working with placeholder fields.
- TableCrc changes from DDL-based CRC to structured metadata CRC.
- Column and index CRCs remain compatible but will be ordered as information_schema
  provides (preserving order).

## Test Updates
- schema_test.go will be adjusted for bulk query expectations and new CRC.
- Add expectations for new TABLES columns (row_format, table_collation, etc.).
