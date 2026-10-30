# The backups folder mount is served by a child process (#2246)

Until #2246 the backups folder mount (`backup-streaming`, GeeseFS behind `DeadlineFS`) ran inside
replication-manager's process. `DeadlineFS` bounds every request and recovers a panic, but a Go
`fatal error` cannot be recovered: a GeeseFS stack overflow on an unlink (unbounded recursion in
`BufferList.RemoveRange` / `ascendChange`) ended the process. A thread still waiting on its own
mount then kept the dead process as a zombie holding its ports: the instance looked alive and
monitored nothing (F2).

## The process

`s3fuse.New` starts `replication-manager backup-folder-mount` (hidden command,
`s3fuse.ServerCommand`) from `/proc/self/exe`, the running binary even once an upgrade replaced
it on disk. The child runs what used to run in process (`serve`: GeeseFS, `DeadlineFS`,
`disablePoll`). It runs as `replication-manager backup-folder-mount` (its argv[0]; the kernel
names the process `exe`, so it is found by its command line, never its name), in its own process
group (a Ctrl-C to replication-manager's group never stops it), with only `PATH`, the proxy and
CA variables (`AWS_CA_BUNDLE` included) and `TMPDIR` of replication-manager's environment (F10). It loads no configuration:
the hidden command reads stdin and writes fd 3. A crash of the child closes its `/dev/fuse` descriptor: the kernel aborts the
connection, a request of replication-manager waiting on the mount fails (`ENOTCONN`) instead of
waiting forever, and the monitor goes on.

## The protocol

- **Parent → child, stdin, one JSON message per line** (`request`): the mount (`mountRequest`:
  point, bucket, endpoint, region, timeouts, memory limit, debug), then the S3 credentials
  (`credentialsMessage`), sent again each time `Credentials.Get` returns new ones (checked every
  30 s: the Cloud18 SSO renews them). In the child, `relayedCredentials` returns the last ones
  received and reads as expired as soon as newer ones arrive. Credentials never go in the
  arguments, the environment or a log (F10).
- **Child → parent, file descriptor 3, one JSON event per line** (`event`): `mounted`, `failed`
  (the mount could not be made, its error), `late` (a request answered EIO at its deadline:
  `OnLate`, WARN0262), `panic` (`OnPanic`, the stack printed on stderr), `unmounted` (the answer
  to a stop, its error when the unmount was refused).
- Event lines are read up to 64 KiB, an error in an event cut at 2000 bytes, so an event is
  never cut and lost.
- **The child's stdout and stderr** (GeeseFS's log, a Go crash) go line by line to `OnLog`, each
  line cut at 2000 bytes (`readLines`: a longer line never stops the reading, so the child never
  blocks writing); the last line is kept for the error of its end.

`Mount.Serves` and `Mount.ETags` stay in the parent: the mounts it supervises, and the bucket's
listing through the parent's own S3 client.

## Lifecycle

- **Start:** `New` returns once the child said `mounted`, its `failed` error, its exit, or after
  3 minutes (killed).
- **Stop** (`Mount.Unmount`, at a settings change or the cluster's stop): stdin is closed; the
  child flushes, unmounts and exits. A refused unmount (busy) is answered `unmounted` with its
  error: the child keeps serving, so a request still in the mount gets its answer, the caller
  detaches the mount (`fusermount -uz`), and the child ends once the mount is released, at most
  10 minutes later. Bounded at 50 s for the answer (no answer: killed) and 30 s for the exit,
  then SIGKILL.
- **The child ends on its own** (a crash, a kill, an unmount from outside): `Join` returns why
  (exit status and last line). Its output is read at most 5 s past its exit: a process left
  holding one of its pipes never hides the end. The cluster takes its "mount ended" path: the dead mount detached,
  the catalogue follows the local folder, WARN0261 shows the reason, and the mount is made again
  after 1, 2, 4, 8 then 10 minutes for failures in a row (`backupMount.failedLocked`); a mount that
  served 10 minutes starts over at 1 minute, a change of its settings is tried at once and starts the count over.
- **replication-manager dies:** the child gets SIGTERM (`Pdeathsig`) and unmounts. Pdeathsig
  follows the OS thread that started the child, not the process: the child is started from a
  goroutine locked to its thread for the child's whole life (`startLocked`).
- **At the next start** (`AbortStuckMounts`): a mount's server still running while no other
  replication-manager runs lost its parent and is killed; its mount answers ENOTCONN and the
  cluster's start detaches it. A mount's server never counts as "another replication-manager".

## No new parameter

The child replaces the in-process mount; `backup-streaming` stays the mount's switch (T14). The
cleanup of a stuck mount at start and at the stop (STUCK_MOUNT_AT_START.md) stays as a second
safety net.

## Seen on dev3 (2026-10-30)

A `kill -9` of the server while mydumper wrote into the mount: the monitor answered throughout,
WARN0261 said why, the mount came back after the delay. mydumper itself did not exit on its write
errors and held the cluster's backup slot until killed by hand: mydumper has no write-stall
watchdog (#2249), a gap of the logical backup, not of the mount.
