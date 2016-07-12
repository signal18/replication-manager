## replication-manager [![Build Status](https://travis-ci.org/mariadb-corporation/replication-manager.svg?branch=master)](https://travis-ci.org/mariadb-corporation/replication-manager)

__replication-manager__ is an high availability solution to manage MariaDB 10.x GTID replication.  

Goals are topology detection and its monitoring, to enable on-demand slave to master promotion (aka switchover), or electing a new master on failure detection (aka failover).

To perform switchover, preserving data consistency, replication-manager uses a proven mechanism similar to common MySQL failover tools such as MHA:

  * Verify replication settings
  * Check (configurable) replication on the slaves
  * Check for long running queries on master
  * Elect a new master (default to most up to date, but it could also be a designated candidate)
  * Put down the IP address on master by calling an optional script
  * Reject writes on master by calling FLUSH TABLES WITH READ LOCK
  * Reject writes on master by setting READ_ONLY flag
  * Reject writes on master by decreasing MAX_CONNECTIONS
  * Kill pending connections on master if any remaining
  * Watching for all slaves to catch up to the current GTID position
  * Promote the candidate slave to be a new master
  * Put up the IP address on new master by calling an optional script
  * Switch other slaves and old master to be slaves of the new master and set them as read-only

__replication-manager__ is commonly used as an arbitrator and drive a proxy that routes the database traffic to the leader database node (aka the MASTER). We can advise usage of:

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

- With monitor-less proxies, __replication-manager__ can call scripts that set and reload the new configuration of the leader route. A common scenario is an VRRP Active Passive HAProxy sharing configuration via a network disk with the __replication-manager__ scripts           
- Using __replication-manager__ as an API component of a group communication cluster. MRM can be called as a Pacemaker resource that moves alongside a VIP, the monitoring of the cluster is in this case already in charge of the GCC.

## ADVANTAGES

Leader Election Cluster is best to use in such scenarios:

   * Dysfunctional node does not impact leader performance
   * Heterogeneous node in configuration and resources does not impact leader performance
   * Leader Pick Performance is not impacted by the data replication
   * Read scalability does not impact write scalability
   * Network inter connect quality fluctuation
   * Can benefit of human expertise on false positive failure detection
   * Can benefit a minimum cluster size of two data nodes
   * Can benefit having different storage engine

This is achieved via following drawbacks:

   * Overloading the leader can lead to data loss during failover  
   * READ on replica is eventually consistent  
   * ACID can be preserved via route to leader always

Coming soon in MariaDB 10.2:

   * READ on replica can be COMMITTED READ under using semi-sync no slave behind feature


Leader Election Asynchronous Cluster can guarantee continuity of service at no cost for the leader and in some conditions with "No Data Loss", __replication-manager__ will track failover SLA (Service Level Availability).


Because it is not always desirable to perform an automatic failover in an asynchronous cluster, __replication-manager__ enforces some tunable settings to constraint the architecture state in which the failover can happen.

In the field, a regular scenario is to have long periods of time between hardware crashes: what was the state of the replication when crash happens?

We can classify SLA and failover scenario into 3 cases:

  * Replica stream in sync   
  * Replica stream not sync but state allows failover      
  * Replica stream not sync but state does not allow failover

## CASE 1: IN SYNC

If the replication was in sync, the failover can be done without loss of data, provided that __replication-manager__ waits for all replicated events to be applied to the elected replica, before re-opening traffic.

In order to reach this state most of the time, we advise following settings:

### Running replication at full speed

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

### Usage of semi-synchronous replication

Semi-synchronous replication enables to delay transaction commit until the transactional event reaches at least one replica. The "In Sync" status will be lost only when a tunable replication delay is attained. This Sync status is checked by __replication-manager__ to compute the last SLA metrics, the time we may auto-failover without losing data and when we can reintroduce the dead leader without re-provisioning it.

The MariaDB recommended settings for semi-sync are the following:

```
plugin_load = "semisync_master.so;semisync_slave.so"  
rpl_semi_sync_master = ON  
rpl_semi_sync_slave = ON  
loose_rpl_semi_sync_master_enabled = ON  
loose_rpl_semi_sync_slave_enabled = ON
rpl_semi_sync_master_timeout = 10
```

Such parameters will print an expected warning in error.log on slaves about SemiSyncMaster Status switched OFF. 

## CASE 2: NOT IN SYNC & FAILABLE

__replication-manager__ can still auto failover when replication is delayed up to a reasonable time, in such case we will possibly lose data, because we are giving to HA a bigger priority compared to the quantity of possible data lost.


This is the second SLA display. This SLA tracks the time we can failover under the conditions that were predefined in the __replication-manager__ parameters, all slave delays not yet exceeded.


Probability to lose data is increased with a single slave topology, when the slave is delayed by a long running transaction or was stopped for maintenance, catching on replication events, with heavy single threaded writes process, network performance can't catch up with the leader performance.


To limit such cases we advise usage of a 3 nodes cluster that removes some of such scenarios like losing a slave.

## CASE 3: NOT IN SYNC & UNFAILABLE

The first SLA is the one that tracks the presence of a valid topology from  __replication-manager__, when a leader is reachable but number of possible failovers exceeded, time before next failover not yet reached, no slave available to failover.


This is the opportunity to work on long running WRITE transactions and split them in smaller chunks. Preferably we should minimize time in this state as failover would not be possible without big impact that  __replication-manager__ can force in interactive mode.     

## DATA CONSISTENCY INSIDE SWITCHOVER

__replication-manager__ prevents additional writes to set READ_ONLY flag on the old leader, if routers are still sending Write Transactions, they can pile-up until timeout, despite being killed by __replication-manager__.

Some additional caution to make sure that piled writes do not happen is that __replication-manager__ will decrease max_connections to the server to 1 and consume last possible connection by not killing himself. This works but to avoid a scenario where a node is left in a state where it cannot be connected anymore (crashing replication-manager in this critical section), we advise using extra port provided with MariaDB pool of threads feature:

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

Start replication-manager in console mode to monitor the cluster:

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

To print the help and option flags for each command, use `replication-manager [command] --help`

Flags help for the monitor command is given below.

## Monitor Options

```
Flags:
      --autorejoin                    Automatically rejoin a failed server to the current master (default true)
      --check-type string             Type of server health check (tcp, agent) (default "tcp")
      --connect-timeout int           Database connection timeout in seconds (default 5)
      --daemon                        Daemon mode. Do not start the Termbox console
      --failcount int                 Trigger failover after N failures (interval 1s) (default 5)
      --failover-limit int            Quit monitor after N failovers (0: unlimited)
      --failover-time-limit int       In automatic mode, Wait N seconds before attempting next failover (0: do not wait)
      --gtidcheck                     Do not initiate failover unless slaves are fully in sync
      --http-bind-address string      Bind HTTP monitor to this IP address (default "localhost")
      --http-port string              HTTP monitor to listen on this port (default "10001")
      --http-root string              Path to HTTP monitor files (default "/usr/share/replication-manager/dashboard")
      --http-server                   Start the HTTP monitor
      --ignore-servers string         List of servers to ignore in slave promotion operations
      --logfile string                Write MRM messages to a log file
      --mail-from string              Alert email sender (default "mrm@localhost")
      --mail-smtp-addr string         Alert email SMTP server address, in host:[port] format (default "localhost:25")
      --mail-to string                Alert email recipients, separated by commas
      --master-connect-retry int      Specifies how many seconds to wait between slave connect retries to master (default 10)
      --master-connection string      Connection name to use for multisource replication
      --maxdelay int                  Maximum replication delay before initiating failover
      --multimaster                   Turn on multi-master detection
      --post-failover-script string   Path of post-failover script
      --pre-failover-script string    Path of pre-failover script
      --prefmaster string             Preferred candidate server for master failover, in host:[port] format
      --readonly                      Set slaves as read-only after switchover (default true)
      --wait-kill int                 Wait this many milliseconds before killing threads on demoted master (default 5000)

Global Flags:
      --hosts string     List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas
      --interactive      Ask for user interaction when failures are detected (default true)
      --log-level int    Log verbosity level
      --rpluser string   Replication user in the [user]:[password] format
      --user string      User for MariaDB login, specified in the [user]:[password] format
      --verbose          Print detailed execution info
```

## System Requirements

`replication-manager` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use GTID for replication. Old-style positional replication is not supported (yet).

## Bugs

Check https://github.com/mariadb-corporation/replication-manager/issues for a list of issues.

## Features

 * High availability support with leader election

 * Semi-sync replication support

 * Provisioning

 * Bootstrap

 * Http daemon mode

 * Email alerts

 * Configuration file

 * 2 nodes Multi Master Switchover support

 * On live mode

 * Failover SLA tracking

 * Log facilities and verbosity

 * Docker images

 * Docker deployment via OpenSVC in Google Cloud

 * Docker deployment via OpenSVC on premise for Ubuntu and OSX


## Roadmap

 * Maxscale binlog server support

 * Maxscale state display

 * Trends display

 * Load and non regression simulator  

 * Agent base server stop leader on switchover   

 * MariaDB integration of no slave left behind https://jira.mariadb.org/browse/MDEV-8112

 * MaxScale integration to disable traffic on READ_ONLY flag https://jira.mariadb.org/browse/MXS-778

## Authors

Guillaume Lefranc <guillaume@mariadb.com>

Stephane Varoqui <stephane@mariadb.com>

## License

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## Version

__replication-manager__ 0.7.0
