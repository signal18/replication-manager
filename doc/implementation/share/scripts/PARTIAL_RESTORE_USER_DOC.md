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

A reseed replaces the databases one at a time while the server keeps running,
so the data volume of the server being reseeded holds the old data **and** the
unpacked backup at the same time. It needs free space for:

- the unpacked backup, about the size of the master's data directory without
  binary logs;
- a target floor of 10% of the volume, checked while the backup is received
  and between the restore phases;
- a margin for what the job writes after the backup is unpacked (prepare files
  and a temporary server): twice the redo log size of the server, at least
  512 MiB. If the redo log size cannot be read, the job does not start the
  transfer and says so in the job log.

The job caps the incoming backup at the free space minus the floor and the
margin, and checks the floor again after the prepare and after the table
definitions were read. If it does not fit, the job stops before any database
or table is changed, removes the unpacked backup and says so in the job log.
The floor is checked at those points, not reserved: another process writing on
the volume, or a backup with a much larger redo log than the server's, can
still cross it between two checks.

Examples (the margin is not the total free space left, it is added on top of
the 10% floor):

| Volume | Free space | Redo log | Floor (10%) | Margin | Largest backup accepted |
|---|---|---|---|---|---|
| 1 TB | 400 GB | 4 GiB | 100 GB | 8 GiB | about 292 GB |
| 100 GB | 40 GB | 96 MiB | 10 GB | 512 MiB | about 29.5 GB |

On a volume that cannot hold the old data and the backup together, reseed
with a logical backup instead.

The unpacked backup is removed once the restore succeeds; after a failure
other than a lack of space it is kept for investigation until the next reseed.

### If a reseed job is interrupted

If the jobs process dies during a reseed (killed, out of memory), the next
run of the jobs container, within about two minutes, marks the job as failed and
cleans up after it; a new reseed can then be started as usual.
