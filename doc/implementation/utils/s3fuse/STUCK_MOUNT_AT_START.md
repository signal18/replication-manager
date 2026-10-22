# A backups folder mount left stuck by a dead replication-manager (#2091)

The backups folder mount (`backup-streaming`) is served by replication-manager itself
(`utils/s3fuse`, GeeseFS). A normal stop unmounts it (`Cluster.Stop`, phase
`backupFolderUnmount`). A replication-manager that dies abruptly (SIGKILL, OOM kill, `docker
kill`, a crash) while one of its threads waits on a request to its own mount leaves that thread
in the kernel waiting for an answer nobody can give (`request_wait_answer`, state D): the
process never ends, stays a zombie holding its ports (10001, 2003, 10005), and the next start
dies on `listen udp :2003: bind: address already in use`. Seen on dev3 on 2026-10-22, recovered
by hand from the host: `echo 1 > /sys/fs/fuse/connections/<n>/abort`, then `umount -l`.

**At start** (`ReplicationManager.Run`, before Carbon and the HTTP server bind),
`s3fuse.AbortStuckMounts(<working dir>, 3s)` reads `/proc/self/mountinfo`, takes the FUSE
mounts under the working directory, and reads each connection's `waiting` count in fusectl
(`/sys/fs/fuse/connections/<device minor>`):

- nothing waiting: left alone (a live server, or a dead mount the cluster's start detaches,
  `detachDeadBackupMountAtStart`); no delay;
- waiting at the first read and still at a second read 3 s later: the connection is aborted
  (the waiting request fails, the stuck process ends and releases its ports) and the mount
  detached (`MNT_DETACH`); then one second for the ports.

The abort needs write access to fusectl: root on a host. A container shows `/sys` read-only
without fusectl: when there is a FUSE mount to check and `/sys/fs/fuse/connections` is empty,
fusectl is mounted in replication-manager's own mount namespace for the check, then unmounted
(CAP_SYS_ADMIN, which the in-process FUSE mount needs anyway; checked on dev3's container: the
connections are listed and `abort` is writable). Without that right (a rootless image) the error
says so in the log and the start goes on (and fails to bind, as before): the abort is then
needed from the host. Serving the mount from a
second process of the replication-manager binary would cover every case (follow-up).

## At the stop (#2142)
The start cannot see every stuck mount: when the stop's unmount is refused (busy), `Unmount` shuts
the file system down and the lazy unmount takes the mount out of mountinfo, but a thread of ours
still in the mount waits on its connection forever; the process never ends and keeps its ports
(dev3 2026-10-22, after a SIGTERM: `fusermount: Device or resource busy`, then the next start
failed on `:2003 bind: address already in use`, and `AbortStuckMounts` found no FUSE mount).
`unmountBackupFolder` now aborts our own connection (`s3fuse.AbortMount`, found by the mount
point in mountinfo, before the lazy unmount) whenever the unmount is refused or times out.
