# Splitdump reload into a ColumnStore cluster (#2010)

A ColumnStore cluster (topology `columnstore`, #2002) is loaded from an OLTP parent's
logical backup: InnoDB tables. ColumnStore refuses most InnoDB DDL, and nothing in
ColumnStore, CMAPI or MariaDB converts it (`CREATE TABLE … ENGINE=Columnstore AS SELECT`
keeps the column definitions and fails the same way). The splitdump reload converts each
table, automatically when the target cluster's topology is `columnstore`.

## Where

`restoreSplitdumpFileGo` (`cluster/srv_job_backup.go`): once a table's schema file
(`schema.table-schema.sql[.gz]`, `splitdump.IsTableSchemaFile`) has run on the restore's
pinned connection, `convertTableToColumnstore` (`cluster/cluster_columnstore_convert.go`)
runs on the same connection, before the data phase. The restore sessions already have
`FOREIGN_KEY_CHECKS=0` (`prepareRestoreConn`), so a child's dump CREATE TABLE passes even
though its parent is already ColumnStore. The data phase is unchanged.

## Mechanism: no SQL parser

1. The dump's `CREATE TABLE` runs as written: an InnoDB table, MariaDB parses it.
2. Its columns, CHECK constraints, indexes, foreign keys and options are read from the
   target's `information_schema` (`dbhelper.GetTableColumns`, `GetTableChecks`,
   `GetTableIndexNames`, `GetTableForeignKeyNames`, `GetTableOptions`).
3. `columnstoreTableDDL` (pure, unit-tested) builds the ColumnStore CREATE TABLE from the
   columns only: keys, foreign keys, partitions and table options disappear by construction.
4. The ColumnStore table is created as `table__mcs`, then the InnoDB table dropped and
   `table__mcs` renamed into its place: a failed CREATE leaves the InnoDB table to load into.

A table that is not InnoDB is left as it is. A table ColumnStore cannot hold (a spatial
type, DECIMAL over 38 digits, a type unknown to the conversion) stays InnoDB with a warning
and the restore goes on. Every change is logged per table.

## Rules (safe: from the schema only, no data read)

| InnoDB | ColumnStore | Why |
| --- | --- | --- |
| TINYINT [UNSIGNED] | SMALLINT [UNSIGNED] | ColumnStore reserves -128, -127 / 254, 255 |
| SMALLINT [UNSIGNED] | MEDIUMINT [UNSIGNED] | reserves -32768, -32767 / 65534, 65535 |
| MEDIUMINT [UNSIGNED] | unchanged | full range |
| INT [UNSIGNED] | BIGINT [UNSIGNED] | reserves -2³¹, -2³¹+1 / 2³²-2, 2³²-1 |
| BIGINT | DECIMAL(19,0) | reserves -2⁶³, -2⁶³+1 |
| BIGINT UNSIGNED | DECIMAL(20,0) UNSIGNED | reserves 2⁶⁴-2, 2⁶⁴-1 |
| DECIMAL ≤ 38 digits, FLOAT, DOUBLE | unchanged | the doc's 18-digit limit is outdated |
| BIT(n) | BIGINT UNSIGNED, DECIMAL(20,0) UNSIGNED for BIT(64) | no BIT type |
| YEAR | SMALLINT | no YEAR type |
| ENUM / SET | VARCHAR(longest value / all values joined) | no ENUM/SET |
| BINARY, VARBINARY | BLOB | no BINARY/VARBINARY |
| UUID / INET6 / INET4 | CHAR(36) / VARCHAR(39) / VARCHAR(15) | no such types |
| CHAR/VARCHAR over 8000 bytes (CHARACTER_OCTET_LENGTH) | TEXT, MEDIUMTEXT, LONGTEXT | 8000-byte limit |
| JSON (longtext + `CHECK (json_valid(col))`) | JSON | accepted as a type |
| any other CHECK calling a function | removed | ColumnStore refuses a function call in a CHECK |
| keys, AUTO_INCREMENT, FOREIGN KEY, partitions | removed | no key of any kind |
| expression default (`current_timestamp()`, `b'101'`), ON UPDATE | removed | literal defaults only |
| generated column | a plain column of its type | the dump holds its values |
| NOT NULL on a string column | removed | a row INSERT keeps `''`, the bulk load reads it as NULL |
| ZEROFILL | removed | |
| comment containing `autoincrement` or `compression` | removed | ColumnStore reads them as options |
| other NOT NULL, literal DEFAULT, charset/collation, comments, DATE/TIME/DATETIME/TIMESTAMP | kept | |

The reserved integer values fail an insert in strict mode (`MCS-2025: Data truncated`) and
are clamped otherwise; measured on dev3 (ColumnStore 25.10.6), together with every other
rule above. Sources besides the tests on dev3: the ColumnStore mtr suite
(`mcs9_create_table_negative`, `mcs59_nonscalar_datatypes`, `mcs62_binary_datatypes`,
`mcs73_indexes_keys`, `mcs8_create_table_with_constraints`, `mcs4_create_table_defined_values`,
`type_timestamp`).

## Validation

- `TestColumnstoreTableDDL`, `TestColumnstoreTableDDLRefusal`, `TestColumnstoreChecks`:
  the DDL generated from a dev3 table's `information_schema.COLUMNS`.
- `TestColumnstoreConversionLive` (runs with `CSCONV_DSN`): on the dev3 UM, a parent and a
  child with a foreign key in dump order, and a table with every rule above, each created
  as the dump writes it and converted by `convertTableToColumnstore`; the rows a dump holds
  (the reserved extremes of every integer type, BIT, ENUM, SET, BINARY, UUID, INET, JSON, a
  generated column's value, an orphan child row) load and read back unchanged.

## Limits

- Load speed: the dump's multi-row INSERTs put 250,000 rows in a ColumnStore table in 9.9 s
  on dev3, against 0.7 s in InnoDB. A large reload would want the data phase through
  ColumnStore's bulk load (LOAD DATA or cpimport).
- A logical backup OF a ColumnStore cluster: `mariadb-dump` locks tables by default, which
  ColumnStore refuses (MCS-2029); it needs `--skip-lock-tables` and `--skip-add-locks`.
