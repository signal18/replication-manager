# ColumnStore table rebalance (#2008)

A ColumnStore cluster (topology `columnstore`, #2002) stores each table's extents on the
dbroots of its PMs. A PM added later brings an empty dbroot: new inserts prefer it, but the
existing tables stay where they were written, and the new PM does not help queries on them.
ColumnStore's own `redistributeData` went away with mcsadmin (CMAPI 23.10 has no
equivalent), and ColumnStore refuses a same-engine `ALTER TABLE … ENGINE=Columnstore`
(`CAL0009 … table already exists`). The rebalance rebuilds a table by SQL instead.

## Surface

| Where | What |
| --- | --- |
| `POST /api/clusters/{c}/actions/columnstore-rebalance` | `{schema, confirm}`: every ColumnStore table not on every dbroot (optionally of one schema) |
| `POST /api/clusters/{c}/schema/{s}/{t}/actions/columnstore-rebalance` | `{confirm}`: one table |
| ACL | rule `/actions/columnstore-rebalance` → `db-optimize`, a table rebuild like alter-engine |
| MCP | `cluster-rebalance-columnstore`, `cluster-rebalance-columnstore-table` |
| GUI | Schema tab, on a columnstore cluster only: Rebalance ColumnStore button, and Rebalance next to the engine of each ColumnStore table; `ColumnstoreRebalanceModal`, plan first, Run after a Check |

## Plan (`Cluster.PlanColumnstoreRebalance`)

On the UM (the cluster's master), columnstore topology only:
- The dbroots: `SystemConfig.DBRootCount` of `/etc/columnstore/Columnstore.xml`, read with
  `LOAD_FILE` (`dbhelper.GetColumnstoreDBRootCount`), so an empty dbroot counts. When the
  server cannot read it (no FILE privilege, `secure_file_priv`), the dbroots holding extents.
- The tables: `dbhelper.GetColumnstoreTableExtents`, the column extents of each table per
  dbroot and their data size: the columns from `information_schema.COLUMNSTORE_COLUMNS`,
  then one `COLUMNSTORE_EXTENTS … WHERE OBJECT_ID = ?` per column. Not a join, a range nor
  an `IN (…)`: the view's full scan stops at `ObjectIDManager::size()` (the number of object
  ids in use, `is_columnstore_extents.cpp`), and ColumnStore reuses the ids a DROP frees; once
  a table was dropped, newer objects lie past that bound and are missing. On dev3 the table
  a rebalance had just rebuilt had no extents in a join while `editem` showed its 3, one per
  dbroot. Only the equality reads one object directly.
- A table goes into the plan when a dbroot holds none of its extents. Skipped with the
  reason: already on every dbroot; a copy left by a rebalance (`__rebalance`, `__old`
  suffixes), or a table that still has one; an autoincrement column; a table of one extent
  per column, too small to spread (a 3-row table rebuilt on dev3 landed on one dbroot
  again); a name too long for the suffix (64 characters).
- Autoincrement: ColumnStore keeps a column's next value in its catalog. A copy of the rows
  restarts it, and ColumnStore refuses to set it on a column already autoincrement
  (`ALTER TABLE … COMMENT='autoincrement=col,N'`: MCS-4006; `AUTO_INCREMENT=`: not supported).
  On dev3, rows 1, 2, 3 copied, the next insert got id 1 again. Such a table is skipped,
  detected by its declarations (`dbhelper.GetColumnstoreAutoincrementTables`: a column
  comment `autoincrement`, a table comment `autoincrement=`), because
  `COLUMNSTORE_COLUMNS.IS_AUTOINCREMENT` reports 1 for every column.

## Run (`Cluster.RunColumnstoreRebalance`)

One rebalance per cluster at a time, refused when the UM changed since the plan; a dedicated
`GetNewDBConn`, never the monitor's pool; one table after the other in the background,
stopping at the first error. For `s.t` (`rebalanceColumnstoreTable`), on two pinned
connections, *lock* and *copy*:

1. copy: `CREATE TABLE s.t__rebalance LIKE s.t`
2. lock: `FLUSH NO_WRITE_TO_BINLOG TABLES s.t WITH READ LOCK`, bounded by
   `lock_wait_timeout` = `db-servers-read-timeout`. The writers to `s.t` wait, the readers
   go on. `LOCK TABLES` would not do: ColumnStore refuses it (MCS-2029).
3. copy: `INSERT INTO s.t__rebalance SELECT * FROM s.t`; the new extents spread over every
   dbroot (each PM writes its share).
4. copy: `RENAME TABLE s.t TO s.t__old, s.t__rebalance TO s.t`, which queues behind the
   lock. The run polls `information_schema.PROCESSLIST` until the copy connection waits for
   a metadata lock, and only then:
5. lock: `UNLOCK TABLES`. The pending exclusive RENAME is granted before the queued
   writers, which then write into the new table. Unlocking before the RENAME is queued
   would let a writer write into the old table between the two.
6. `DROP TABLE s.t__old`.

Any failure before the RENAME drops `t__rebalance`, releases the lock and leaves `s.t` as it
was; a failure of the last drop leaves `t__old` and says so. Progress on the `Cluster`
(`columnstoreRebalance`), raised every tick: CINF0016 while it runs, WARN0241 with the table
and the error once it stopped (until the next rebalance).

## Limits

- The copy needs the table's size free on the PMs while it runs (the plan shows the sizes).
- The writes to the table wait for the whole copy: about 3 s for 250,000 rows on dev3, so
  minutes on a large table. The reads go on.
- The rebuilt table keeps its definition (`CREATE TABLE … LIKE`); what ColumnStore keeps
  outside it is the autoincrement next value, hence the skip above.

## Validation

By hand on dev3 (`cstry`: UM um1, PMs pm1 and pm2, three OpenSVC failover services): a
250,000-row table on dbroots 1 and 2 (5 + 5 extents) became 5 + 5 + 5 on dbroots 1, 2 and 3;
a writer that arrived during the copy waited about 8 s and its row is in the new table, not
in the old one.
