# mydumper trx-tables on MariaDB

Since mydumper 0.20.1, a logical backup of a MariaDB server through mydumper fails unless it
runs with `--trx-tables=0`. repman adapts the mydumper options to mydumper >= 0.18.1
(`mydumperTrxTablesArgs`, cluster/srv_job_backup.go) and logs what it changed.

## What repman does (mydumper >= 0.18.1)

| Options | repman | Log |
|---|---|---|
| `--less-locking`, `--trx-consistency-only` (the default `backup-mydumper-options`) | removed | INFO |
| no trx-tables option, MariaDB | `--trx-tables=0` added | INFO, same line |
| no trx-tables option, MySQL / Percona | nothing added: mydumper's default applies | — |
| a trx-tables option configured | kept | WARN when it fails the backup: `--trx-tables` / `--trx-tables=1` on MariaDB with mydumper >= 0.20.1, `--no-trx-tables` with mydumper < 0.21.1 |

The INFO line lists every change in one line, written only when something changed:

```
mydumper 1.0.11 on <server>: backup-mydumper-options adapted: --less-locking, --trx-consistency-only removed
(refused by mydumper since 0.18.1); --trx-tables=0 added (since mydumper 0.20.1, the Aria system tables of
MariaDB are dumped only with --trx-tables=0)
```

Before, the deprecated flags were replaced with `--trx-tables` on every engine, without a log line
(#1351).

The warning reads a configured value the way mydumper does (`mydumperTrxTablesOn`): mydumper sets
`trx_tables=atoi(value)`, or 1 without a value (src/mydumper/mydumper_arguments.c, 0.20.1-1 and
1.0.11-1). So trx-tables mode is on for `--trx-tables` alone or a value whose `atoi` is not 0, and
off for any other value, words included:

| `--trx-tables…` | MariaDB backup, mydumper 0.20.1-1, 0.20.1-2, 0.21.1, 0.21.3, 1.0.1, 1.0.3, 1.0.5, 1.0.11 |
|---|---|
| (no value), `=1`, `=2`, `=01`, `=1x`, `=-1`, `=+1` | refused (exit 133 on 0.20.1, exit 1 after) |
| `=0`, `=`, `=false`, `=FALSE`, `=no`, `=off`, `=true`, `=TRUE`, `=+0`, `=+-1` | runs |

(`=+1`, `=+0`, `=+-1` run on 0.20.1-1 and 1.0.11 only.)

## Why

### mydumper

`--trx-tables` tells mydumper that every table it dumps is transactional: it releases the global
lock right after the consistent snapshot. From 0.20.1 it checks the claim and refuses a table that
is not transactional. `--trx-tables=0` makes it dump those tables under the global lock first.

Each release from 0.18.1 to 1.0.11 (Debian bookworm packages), against MariaDB 11.4 (its system
schema included, as in a repman backup):

| mydumper | `--less-locking --trx-consistency-only` | `trx-tables` default | `--trx-tables` | no trx option | `--trx-tables=0` | `--no-trx-tables` |
|---|---|---|---|---|---|---|
| 0.17.1 (the images' version) | accepted | — (no option) | — | — | — | — |
| 0.18.1, 0.19.1, 0.19.3 | refused: "--less-locking is deprecated and its behaviour is the default" | FALSE | exit 0 | exit 0 | exit 0 | unknown option (0.19.3) |
| 0.20.1-1, 0.20.1-2 | refused | 1 | aborts, exit 133: "Non transactional table found: `mysql`.`column_stats`" | aborts, exit 133 | exit 0 | unknown option |
| 0.21.1, 0.21.3 | refused | 1 | exit 1: "Non transactional table found" | exit 1 | exit 0 | exit 0 |
| 1.0.1, 1.0.3, 1.0.5, 1.0.11 | unknown option | 1 | exit 1 | exit 1 | exit 0 | exit 0 |

- `--trx-tables=0` is accepted by every version from 0.18.1; `--no-trx-tables` only from 0.21.1.
- The refusal is per table and per engine: on MariaDB 11.4 with mydumper 1.0.11, a table in
  MyISAM, Aria, MEMORY or CSV is refused with `--trx-tables` and dumped with `--trx-tables=0`;
  InnoDB is dumped with both.

### The servers

The `mysql` schema of every MariaDB has Aria tables; MySQL 8.0 and 8.4 have none that mydumper
dumps (it leaves out the CSV `general_log` and `slow_log`: they are in no dump of this test):

| Server | `mysql` schema engines |
|---|---|
| MariaDB 10.6.28, 10.11.19, 11.4.13, 11.8.9, 12.3.3 | Aria 24, CSV 2, InnoDB 4 |
| MySQL 8.0.35, 8.4.11 | InnoDB 36, CSV 2 |

## Proof through repman

develop with the change, mydumper 1.0.11 as `mydumper` of the repman container, a logical backup
requested through the API (`backup-logical-type` mydumper):

| Server | Options | mydumper received | Result | Backup position |
|---|---|---|---|---|
| MariaDB 10.11 | default | `--trx-tables=0` | success (without the change: "Non transactional table found", exit 1) | equal to the primary's file, position and GTID |
| MySQL 8.0 | default | no trx option | success | equal |
| MySQL 8.4 | default | no trx option | success | equal |
| MariaDB 10.11 | default + `--trx-tables` | `--trx-tables` | fails, after the WARN line | — |
| MariaDB 10.11 (mydumper 0.17.1) | default | `--less-locking --trx-consistency-only` | success, unchanged | equal |

## Not covered

- MySQL / Percona with mydumper 0.18.1 to 0.19.x: repman added `--trx-tables` there before; it now
  adds nothing, and the default of those versions is trx-tables off. mydumper then looks for the
  non-transactional tables and dumps them before it releases the global lock (its `--no-trx-tables`
  help), so the lock can be held a little longer than before. MySQL 8.0 / 8.4 have none in the dump;
  the lock time was not measured.
- `--sync-thread-lock-mode=NO_LOCK` or `SAFE_NO_LOCK` configured: mydumper then forces trx-tables
  on whatever the trx-tables option (src/mydumper/mydumper_start_dump.c, 0.20.1-1 and 1.0.11-1), so
  a MariaDB backup is refused despite `--trx-tables=0`. Not run; the default `AUTO` is not affected
  (the backups above ran with it).
- MySQL / Percona before 8.0 (MyISAM system tables): not given `--trx-tables=0`.
- A MySQL / Percona 8.0+ server with a non-transactional table of its own: mydumper refuses it in
  trx-tables mode (its default since 0.20.1); its error is in the backup log.
