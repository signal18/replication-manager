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

The abort needs write access to fusectl: root on a host, or a privileged container. Without it
the error says so in the log and the start goes on (and fails to bind, as before); a normal
container or a rootless image still needs the abort from the host. Serving the mount from a
second process of the replication-manager binary would cover every case (follow-up).
