# Dynamic resources and resources set in /etc (#2142)

A resource axis set in `cluster.d` (`prov-db-cpu-cores`, `prov-db-memory`, `prov-db-disk-iops`,
`prov-db-disk-size`) is immutable (`ImmuableFlagMap`): a dynamic change to it is saved to
`<cluster>/overwrite.toml`, which no start reads (server.go, the read is commented out), and each
start reads the `/etc` value back. dev3 2026-10-22: disk pinned at 2G, the disk follow grew it
and the volume to 9G, each start brought the plan back to 2G (the binlog budget and the plan's
`max_binlog_total_size` with it) a minute before the follow raised it again.

- `resourceAxisPinned(axis)` (cluster_resource_pins.go): the dynamic driver never moves a pinned
  axis: `growAxisInPlan`, `shrinkAxisInPlan`, `raiseCPUToPlan`, `followDiskUsage`, and
  `alignDBResourceToPlan` keeps it (the service plan apply already did, cluster_set.go).
- `checkPinnedResourceStates`, each tick under the dynamic config: ERR00119 for the disk,
  WARN0266 for the others, naming the key and its value.

## Disk follow without the binlogs
`followDiskUsage` sized `prov-db-disk-size` on the whole datadir (`DiskMaxBytes`, `df` used),
binlogs included, and grew the volume to exactly that. Now per server
`diskFollowNeedBytes(used, binlogFilesBytes, prov-db-binlog-total-size-pct)` =
(used - binlogs) / (1 - pct): the binlogs never grow the plan, and the disk keeps their share,
which `max_binlog_total_size` (the plan's, under the dynamic config) bounds.

## Open: leaving a pin
Removing the key from `cluster.d` makes the next start take the flag's default (4G memory, 20G
disk, 1 core, 300 iops), not the running value: the dynamic value sits in the unread
overwrite.toml, and a settings reload rebuilds from the files too.
