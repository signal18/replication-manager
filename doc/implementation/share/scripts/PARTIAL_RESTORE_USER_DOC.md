# Draft for docs.signal18.io: physical reseed and flashback

*Draft of the user documentation (T8). To publish on docs.signal18.io; not
part of the implementation docs.*

## Physical reseed and flashback

A physical reseed replaces the data of a database server with a physical
backup (mariabackup or xtrabackup) of the master; a flashback does the same
with the server's own backup. replication-manager sends the backup to the
server's jobs container, which unpacks it, prepares it and replaces the data
**while the database server keeps running**: the database container is never
stopped. Once done, replication-manager applies the backup's GTID position
and restarts replication.

### What is restored

- Every table, with its data, indexes, foreign keys, generated columns and
  partitions (InnoDB, Aria, MyISAM and other file-based engines)
- Views, stored functions and procedures, triggers and events. On a replica,
  events are created disabled there, as replication itself does.

### What is not restored

- Users and grants: the server keeps its own accounts (on a replica they
  arrive through replication).
- Rows of MEMORY tables: they never exist in a backup; the tables come back
  empty.
- Databases that exist only on the server being reseeded are kept as they
  are.

### When the reseed stops without changing anything

The job fails, the server's data is left untouched and the reason is in the
job log, when:

- the backup could not be prepared;
- a table is subpartitioned, or a database name contains characters stored
  encoded on disk (for example `-`): use a logical reseed for such servers;
- the unpacked backup would leave less than 10% free space on the data
  volume.

If a single table cannot be imported, it is left out and named in the job
log, the job fails, and the server is not put back into replication.

### Space needed

The data volume of the server being reseeded needs free space for the whole
unpacked backup, about the size of the master's data directory without
binary logs, plus 10% of the volume, which always stays free. The unpacked
backup is removed once the restore succeeds; after a failure it is kept for
investigation until the next reseed.

### If a reseed job is interrupted

If the jobs process dies during a reseed (killed, out of memory), the next
run of the jobs container, within about two minutes, marks the job as failed and
cleans up after it; a new reseed can then be started as usual.
