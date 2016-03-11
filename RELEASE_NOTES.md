0.6.0 Release Notes
===================

 * replication-manager now uses the POSIX flag syntax instead of the Go flag syntax. All flags now use the double dash (--) syntax.

 * Failover and switchover logic has been entirely rewritten. As a consequence, failover can now use the monitoring console without restarting it, and topologies are preserved across failovers.

 * Failed masters can be reintroduced automatically if the `--autorejoin` option is true. This option is enabled by default.

 * The monitoring console has a default

 * Failed servers are now displayed as Standalone Servers on the monitoring console.

 * Servers now correctly set read-only or read-write mode. In the monitoring console, Ctrl-R sets read-only mode interactively, and Ctrl-W sets read-write mode on the slaves.

 * replication-manager can now log its messages to a file with the `--log` option.

 * It is now possible to set a connection timeout with `--timeout`. It helps if some servers are unresponsive.

 * Automatic failover mode comes now with 3 tunables:

 ** It is now possible to set the number of master failures with the `--failcount` option.

 ** It is now possible to set the number of automatic failovers with the `--failover-limit` option. Default is 0 (unlimited). If you want to have similar behavior as MHA, use `--failover-limit=1`

 ** It is now possible to set the interval between automatic failovers with the `--failover-time-limit` option. Default is 0 (no interval).

 * A bug with failover setting the read-only flag on the promoted master has been corrected.

 * Several major bugs have been corrected and replication-manager should not crash anymore in various conditions.
