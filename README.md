## replication-manager [![Build Status](https://travis-ci.org/mariadb-corporation/replication-manager.svg?branch=master)](https://travis-ci.org/mariadb-corporation/replication-manager)

**replication-manager** is an high availability solution to manage MariaDB 10.x GTID replication.  
It detects topology and monitor health to trigger slave to master promotion (aka switchover), or elect a new master on failure detection (aka failover).

To perform switchover, preserving data consistency, replication-manager uses a mechanism similar to common MySQL failover tools such as MHA:

  * Verify replication settings
  * Check (configurable) replication on the slaves
  * Check for long running queries on master
  * Elect a new master (usually the most up to date, but it could also be a designated candidate)
  * Put down the IP address on master by calling an optional script
  * Reject writes on master by calling FLUSH TABLES WITH READ LOCK
  * Reject writes on master by setting READ_ONLY FLAG
  * Reject writes on master by decreasing MAX_CONNECTIONS
  * Kill pending connections on master if any remaining
  * Watching for all slaves to catch up to the current GTID position
  * Promote the candidate slave to be a new master
  * Put up the IP address on new master by calling an optional script
  * Switch other slaves and old master to be slaves of the new master and set them as read-only

When **replication-manager** is used as an arbitrator it will have to drive a proxy that routes the database traffic to the leader database node (aka the MASTER). We can advise usage of:

- A layer 7 proxy as MariaDB MaxScale that can transparently follow a newly elected topology via similar settings:

```
[MySQL Monitor]  
type=monitor  
module=mysqlmon  
servers=%%ENV:SERVERS_LIST%%  
user=root  
passwd=%%ENV:MYROOTPWD%%  
monitor_interval=500  
detect_stale_master=true

[Write Connection Router]  
type=service  
router=readconnroute  
router_options=master  
servers=%%ENV:SERVERS_LIST%%  
user=root  
passwd=%%ENV:MYROOTPWD%%  
enable_root_user=true  
```

- With monitor-less proxies, **replication-manager** can call scripts that set and reload the new configuration of the leader route. A common scenario is an VRRP Active Passive HAProxy sharing configuration via a network disk with the **replication-manager** scripts           
- Using **replication-manager** as an API component of a group communication cluster. MRM can be called as a Pacemaker resource that moves alongside a VIP, the monitoring of the cluster is in this case already in charge of the GCC.

## ADVANTAGES

A **replication-manager** Leader Election Cluster is best to use in such scenarios:

   * Dysfunctional node does not impact Availability and Performance  
   * Heterogeneous node in configuration and resources does not impact Availability and Performance  
   * Leader Pick Performance is not impacted by the data replication
   * Read scalability does not impact write scalability
   * Network inter connect quality fluctuation
   * Can benefit of human expertise on false positive failure detection
   * Can benefit a minimum cluster size of two data nodes
   * Can benefit having different storage engine

This is achieved via following drawbacks:

   * Overloading the leader can lead to data loss during failover  
   * READ Replica is eventually consistent  
   * ACID can be preserved via route to leader always
   * READ Replica can be guaranteed COMMITTED READ under monitoring of semi-sync no slave behind feature

The history of MariaDB replication has reached a point that replication can almost in any case catch with the master. It can be ensured using new features like Group Commit improvement, optimistic in-order parallel replication and semi-synchronous replication.

MariaDB 10.1 settings for in-order optimistic parallel replication:

```
slave_parallel_mode = optimistic  
slave_domain_parallel_threads = %%ENV:CORES%%  
slave_parallel_threads = %%ENV:CORES%%  
expire_logs_days = 5  
sync_binlog = 1  
log_slave_updates = ON
```

Leader Election Asynchronous Cluster can guarantee continuity of service at no cost for the leader and possibly with "No Data Loss" under some given SLA (Service Level Availability).      
Because it is not always desirable to perform automatic failover in an asynchronous cluster, **replication-manager** enforces some tunable settings to constraint the architecture state in which the failover can happen. In the field, a regular scenario is to have long periods of time between hardware crashes: what was the state of the replication when this happens? If the replication was in sync, the failover can be done without loss of data, provided that we wait for all replicated events to be applied to the elected replica, before re-opening traffic. In order to reach this state most of the time, we advise usage of semi-synchronous replication that enables to delay transaction commit until the transactional event reaches at least one replica. The "In Sync" status will be lost only when a tunable replication delay is attained. This Sync status is checked by **replication-manager** to compute the last SLA metrics, the time we may auto-failover without losing data and when we can reintroduce the dead leader without re-provisioning it.

The MariaDB recommended settings for semi-sync are the following:

```
plugin_load = "semisync_master.so;semisync_slave.so"  
rpl_semi_sync_master = ON  
rpl_semi_sync_slave = ON  
loose_rpl_semi_sync_master_enabled = ON  
loose_rpl_semi_sync_slave_enabled = ON
```

**replication-manager** can still auto failover when replication is delayed up to a reasonable time, in such case we will lose data, giving to HA a bigger priority compared to the quantity of possible data lost. This is the second SLA display. This SLA tracks the time we can failover under the conditions that were predefined in the **replication-manager** parameters, number of possible failovers exceeded, all slave delays exceeded, time before next failover not yet reached, no slave available to failover.

The first SLA is the one that tracks the presence of a valid topology from  **replication-manager**, when a leader is reachable.                        

Consistency during switchover and in case of split brain on active active routers:
**replication-manager** has no other way on the long run to prevent additional writes to set READ_ONLY flag on the old leader, if routers are still sending Write Transactions, they can pile-up until timeout, despite being killed by **replication-manager**, additional caution to make sure that piled writes do not happen is that **replication-manager** will decrease max_connections to the server to 1 and use the last one connection by not killing himself. This works but in yet unknown scenarios we would not let a node in a state where it cannot be connected to anymore, so we advise using extra port provided with MariaDB pool of threads feature :

```
thread_handling = pool-of-threads  
extra_port = 3307   
extra_max_connections = 10
```   

Also, to better protect consistency it is strongly advised to disable *SUPER* privilege to users that perform writes, such as the MaxScale user when the Read-Write split module is instructed to check for replication lag.  

```
[Splitter Service]
type=service
router=readwritesplit
max_slave_replication_lag=30
```

## Procedural command line examples

Run replication-manager in switchover mode with master host db1 and slaves db2 and db3:

`replication-manager switchover --hosts=db1,db2,db3 --user=root --rpluser=replicator --interactive`

Run replication-manager in non-interactive failover mode, using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`replication-manager failover --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" --verbose --maxdelay=15`

## Monitoring

Start replication-manager in console mode to monitor the cluster

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass`

![mrmconsole](https://cloud.githubusercontent.com/assets/971260/16738035/45f2bbf2-4794-11e6-8286-65f9a3179e31.png)

The console mode accepts several commands:

```
Ctrl-D  Print debug information
Ctrl-F  Manual Failover
Ctrl-R  Set slaves read-only
Ctrl-S  Switchover
Ctrl-Q  Quit
Ctrl-W  Set slaves read-write
```

Start replication-manager in background to monitor the cluster, using the http server to control the daemon

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --daemon --http-server`

The http server is accessible on http://localhost:10001 by default, and looks like this:

![mrmdash](https://cloud.githubusercontent.com/assets/971260/16737848/807d6106-4793-11e6-9e65-cd86fdca3b68.png)

The http dashboard is an experimental angularjs application, please don't use it in production as it has no protected access for now (or use creativity to restrict access to it).

## Available commands

```
  agent       Starts replication monitoring agent
  bootstrap   Bootstrap a replication environment
  failover    Failover a dead master
  monitor     Start the interactive replication monitor
  provision   Provision a replica server
  switchover  Perform a master switch
  topology    Print replication topology
  version     Print the replication manager version number
```

## Options

At a minimum, required options are: a list of hosts (replication-manager can autodetect topologies), user with privileges (SUPER, REPLICATION CLIENT, RELOAD), and replication user (with REPLICATION SLAVE privileges)

  * --autorejoin `bool`

    Automatically rejoin a failed server to the current master. (default true)

  * --connect-timeout `secs`

    Database connection timeout in seconds (default 5)

  * --failover `<state>`

    Start the replication manager in failover mode. `state` can be either `monitor` or `force`, whether the manager should run in monitoring or command line mode. The action will result in removing the master of the current replication topology.

  * --failcount `secs`

    Trigger failover after N failures (interval 1s) (default 5)

  * --failover-limit `count`

    In auto-monitor mode, quit after N failovers (0: unlimited)

  * --failover-time-limit `secs`

    In auto-monitor mode, wait N seconds before attempting next failover (0: do not wait)

  * --gtidcheck `<boolean>`

    _DEPRECATED_ Check that GTID sequence numbers are identical before initiating failover. Default false. This must be used if you want your servers to be perfectly in sync before initiating master switchover. If false, mariadb-repmgr will wait for the slaves to be in sync before initiating.

  * --hosts `<address>:[port],`

    List of MariaDB hosts IP and port (optional), specified in the `host:[port]` format and comma-separated.

  * --ignore-servers `<address>:[port],`

    List of servers in the `host:[port]` format to be ignored for slave promotion operations.

  * --interactive `<boolean>`

    Runs the MariaDB monitor in interactive mode (default), asking for user interaction when failures are detected. A value of false also allows mariadb-repmgr to invoke switchover without displaying the interactive monitor.

  * --logfile `path`

    Write MRM messages to a log file.

  * --maxdelay `<seconds>`

    Maximum slave replication delay allowed for initiating switchover, in seconds.

  * --post-failover-script `<path>`

    Path of post-failover script, to be invoked after new master promotion.

  * --pre-failover-script `<path>`

    Path of pre-failover script to be invoked before master election.

  * --prefmaster `<address>`

    Preferred candidate server for master failover, in `host:[port]` format.

  * --readonly `<boolean>`

    Set slaves as read-only when performing switchover. Default true.

  * --rpluser `<user>:[password]`

    Replication user and password. This user must have REPLICATION SLAVE privileges and is used to setup the old master as a new slave.

  * --switchover `<action>`

    Starts the replication manager in switchover mode. Action can be either `keep` to degrade the old master as a new slave, or `kill` to remove the old master from the replication topology.

  * --socket `<path>`

    Path of MariaDB unix socket. Default is "/var/run/mysqld/mysqld.sock"

  * --user `<user>:[password]`

    User for MariaDB login, specified in the `user:[password]` format. Must have administrative privileges. This user is used to perform switchover.

  * --verbose

    Print detailed execution information.

  * --version

    Return softawre version.

  * --wait-kill `<msecs>`

    Wait this many milliseconds before killing threads on demoted master. Default 5000 ms.

## System Requirements

`replication-manager` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use GTID for replication. Old-style positional replication is not supported (yet).

## Bugs

Check https://github.com/mariadb-corporation/replication-manager/issues for a list of issues.

## Roadmap

 * High availability support with leader election

 * Semi-sync replication support

 * Provisioning

## Authors

Guillaume Lefranc <guillaume@mariadb.com>
Stephane Varoqui <stephane@mariadb.com>

## License

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## Version

**replication-manager** 0.7.0
