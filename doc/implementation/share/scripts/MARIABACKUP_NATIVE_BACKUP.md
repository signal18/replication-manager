# mariabackup physical backup: native `--backup --target-dir` instead of `--innobackupex`

## Problem

The `mariabackup` job of `share/scripts/dbjobs_new.sh` runs `mariabackup` in `--innobackupex` mode and streams the backup
to the server (`--stream=xbstream ... | socat`). In that mode `mariabackup` expects the target directory as a positional
argument, even when it streams. The command had none, so with the connection options written as long options
(`--user --host --password --port`) it stopped at once:

```
innobackupex: Missing argument
```

Nothing was streamed (0 bytes), `backup.out` had no `completed OK!`, the job row ended in state 5 ("No successful record
(complete OK!) found") and the server raised WARN0115.

The short form of the connection options (`-u$USER -h$MYSQL_SERVER -p$PASSWORD -P$MYSQL_PORT`) did not show the problem, by
accident: `-h` is `--datadir` in `mariabackup` (the host option is `-H`), and in `--innobackupex` mode the token
`-h<host>` was taken as the positional target directory (`Backup created in directory '.../-h<host>/'`), which created a
directory named `-h<host>` in the working directory of the job. This is what `mariabackup` prints; it is not stated in its
documentation.

## Change

`share/scripts/dbjobs_new.sh`, job `mariabackup`: the command runs the native form of `mariabackup`, as the `xtrabackup` job does:
`--defaults-file` first (the native mode requires it to be the first argument), then `--backup`, and the target directory named
with `--target-dir="$LOG_DIR/"`. In this script `LOG_DIR` is `TMP_DIR`, the jobs data directory. `--innobackupex`, which is
deprecated (`mariadb-backup` 11.8 says so), is gone from the command. The command does not depend on how the connection options
are written, and no `-h<host>` directory is created any more.

The log lines that the server and the job read are the same in both modes: the binlog position line
(`filename '..', position '..', GTID of the last change '..'`, parsed by the server) and the `completed OK!` line (grepped by
`doneJob`). The native form prints `completed OK!` once, the legacy mode twice; the check needs one.

## Tests

`share/scripts/tests/mariabackup_target_dir/docker_check.sh` (real Docker, standalone, run by hand like the other
`docker_check.sh` scripts of `share/scripts/tests/`). It takes the command line of the job from the script itself, runs it in the
official `mariadb` image, with binary logging on, from the job's working directory with the script's variables set, and asserts
that: the command is the native form (`--defaults-file` first, `--backup`, `--target-dir=`, no `--innobackupex`); it exits 0,
streams data and prints `completed OK!` and the binlog position line in the forms the job and the server read; the target
directory holds nothing but the job's `backup.out` and the working directory gets no stray entry; the backup is restorable the way
`reseedmariabackup` uses it (the stream unpacks with `mbstream`, `--prepare --export` exits 0, and a fresh server opens that
directory with an InnoDB and an Aria table intact, `CHECK TABLE` OK); and the previous form (`--innobackupex`, no target
directory) fails with `Missing argument` and 0 bytes, so the check can see the bug. Images: `mariadb:10.6`, `10.11`, `11.8`.
The native command was also run by hand on 10.1, 10.2, 10.5, 11.4 and 12.3 (`completed OK!`).

## Not covered

- A reseed run through replication-manager (`reseedmariabackup` and the restore after it): the backup artifact was checked, not the
  whole reseed. The `flashbackmariabackup` job was not examined.
- How the server stores the binlog coordinates: the line it parses is identical, the storing itself was not run.
- Versions of MariaDB other than the ones tested.
- Jobs scripts delivered by other means than this file (for instance through the OpenSVC moduleset) were not examined.
