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

MariaDB 10.1 setting for in order optimistic parallel replication:

```
slave_parallel_mode = optimistic  
slave_domain_parallel_threads = %%ENV:CORES%%  
slave_parallel_threads = %%ENV:CORES%%  
expire_logs_days = 5  
sync_binlog = 1  
log_slave_updates = ON
```

Leader Election Asynchronous Cluster can guarantee continuity of service at no cost for the leader and possibly with "No Data Loss" under some given SLA (Service Level Availability).  

That is not always preferable to perform automatic failover in an asynchronous cluster **replication-manager** impose tunable parameters to constraint the architecture state on when the failover can happen.

In the field, a regular scenario is to have long period of time between hardware crashes: what was the state of the replication when this happen? Is state was in sync the failover can be done without data lost.

**replication-manager** Wait for all replicated events to be applied to the elected replica, before re opening traffic.

For standing inside this in sync state we advice usage of semisync replication that enable to delay TRX commit until the TRX events reach at least one replica. The "In Sync" status will be lost only passing a tunable replication delay. This Sync status is checked by **replication-manager** to compute the last SLA metrics, the time i can auto failover without loosing data and can reintroduce the dead leader without re re provisioning it.

Without semi-sync **replication-manager** will only know about data lost after the lost leader show up again.  

MariaDB setting for semisync are the following

```
plugin_load = "semisync_master.so;semisync_slave.so"  
rpl_semi_sync_master = ON  
rpl_semi_sync_slave = ON  
loose_rpl_semi_sync_master_enabled = ON  
loose_rpl_semi_sync_slave_enabled = ON
```

**replication-manager** can still auto failover when replication is delay up to reasonable amount, in this case you agree to lose data and give higher priority to HA  compare to acceptable amount of data lost. This is the second SLA display. This SLA track the time i can failover under the conditions that was predefined in the **replication-manager** parameters, number of possible failover exceeded, all slaves delays exceeded, time before next failover not yet reached, no slave to failover.

First SLA is the one that track the presence of a valid topology from  **replication-manager**, when a leader is reachable.                        

Consistency during switchover and in case of split brain on active active routers:
**replication-manager** have no other way on the long run to prevent additional writes to set READ_ONLY flag on the old leader, if routers still sending WRITE TRX, they can pill up until timeout, despite being killed by **replication-manager**, additional caution to make sure that piled writes do not happen is that **replication-manager** will decrease max_connections to the server to 1 and use the last one connection by not killing himself         This works but in yet unknown scenarios we would not let a node in a state it can be connected any more so we advice using extra port provided with MariaDB pool of thread feature :

```
thread_handling = pool-of-threads  
extra_port = 3307   
extra_max_connections = 10
```   

Also to better protect consistency it is strongly advice to not enable SUPER PRIVILEGES to users that performs writes, such as the MaxScale user when read write split module is instructed to check for replication lags  

```
[Splitter Service]
type=service
router=readwritesplit
max_slave_replication_lag=30
```

## Procedural command line examples

Run replication-manager in switchover mode with master host db1 and slaves db2 and db3:

`replication-manager switchover --hosts=db1,db2,db3 --user=root --rpluser=replicator --interactive`

Run replication-manager for a failover in interactive mode using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`replication-manager failover --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" --verbose --maxdelay=15 --interactive=true`

Run replication-manager for a failover non-interactively of a dead master (similar setup as above):

`replication-manager failover --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" --post-failover-script="/usr/local/bin/vipup.sh" --interactive=false`

## Monitoring

Start replication-manager in terminal to monitor the cluster

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass`

Start replication-manager in background to monitor the cluster

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --daemon`

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

    Check that GTID sequence numbers are identical before initiating failover. Default false. This must be used if you want your servers to be perfectly in sync before initiating master switchover. If false, mariadb-repmgr will wait for the slaves to be in sync before initiating.

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
