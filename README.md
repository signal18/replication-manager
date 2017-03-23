## replication-manager [![Build Status](https://travis-ci.org/tanji/replication-manager.svg?branch=develop)](https://travis-ci.org/tanji/replication-manager) [![Stories in Ready](https://badge.waffle.io/tanji/replication-manager.svg?label=ready&title=Ready)](http://waffle.io/tanji/replication-manager)

__replication-manager__ is an high availability solution to manage MariaDB 10.x GTID replication.  

Product goals are topology detection and topology monitoring, enable on-demand slave to master promotion (aka switchover), or electing a new master on failure detection (aka failover). It enforces best practices to get at a minimum up to zero loss in most failure cases.

- [Overview](#overview)
- [Why replication-manager](#why-replication-manager)
- [Staying in sync](#howto-stay-in-sync)
- [Using parallel replication](#using-parallel-replication)
- [Using semi-synchronous replication](#using-semi-synchronous-replication)
- [Switchover workflow](#switchover-workflow)
- [Failover workflow](#failover-workflow)
- [Usage](#usage)
- [Command line switchover](#command-line-switchover)
- [Command line failover](#command-line-failover)
- [Command line monitor](#Command-line-monitor)
- [Command line bootstrap](#Command-line-bootstrap)
- [Using monitor in daemon mode](#daemon-monitoring)
- [Using configuration files](#using-configuration-files)
- [Using external scripts](#using-external-scripts)
- [Using Maxscale](#using-maxscale)
- [Using Haproxy](#using-haproxy)
- [Using Multi Master](#using-multi-master)
- [Using Multi Tier Slave](#using-multi-tier-slave)
- [Force best practices](#force-best-practices)
- [Active standby and external arbitrator](#active-standby-and-external-arbitrator)
- [Metrics](#metrics)
- [Non-regression tests](#non-regression-tests)
- [System requirements](#system-requirements)
- [Bugs](#bugs)
- [Features](#features)
- [Faq](FAQ.md)
- [Downloads](#downloads)
- [Contributors](#contributors)
- [Authors](#authors)

## Overview
To perform switchover, preserving data consistency, replication-manager uses an improved workflow similar to common MySQL failover tools such as MHA:

  * Verify replication settings
  * Check (configurable) replication on the slaves
  * Check for long running queries and transactions on master
  * Elect a new master (default to most up to date, but it could also be a designated candidate)
  * Put down the IP address on master by calling an optional script
  * Reject writes on master by calling FLUSH TABLES WITH READ LOCK
  * Reject writes on master by setting READ_ONLY flag
  * Reject writes on master by decreasing MAX_CONNECTIONS
  * Kill pending connections on master if any remaining
  * Watching for all slaves to catch up to the current GTID position
  * Promote the candidate slave to be a new master
  * Put up the IP address on new master by calling an optional script
  * Switch other slaves and old master to be slaves of the new master and set them read-only

__replication-manager__ is commonly used as an arbitrator and drive a proxy that routes the database traffic to the leader database node (aka the MASTER). We can advise usage of:

- A layer 7 proxy as MariaDB MaxScale that can transparently follow a newly elected topology
- With monitor-less proxies, __replication-manager__ can call scripts that set and reload the new configuration of the leader route. A common scenario is an VRRP Active Passive HAProxy sharing configuration via a network disk with the __replication-manager__ scripts           
- Using __replication-manager__ as an API component of a group communication cluster. MRM can be called as a Pacemaker resource that moves alongside a VIP, the monitoring of the cluster is in this case already in charge of the GCC.

## Why replication-manager

Leader Election Cluster is best used in such scenarios:

   * Dysfunctional node does not impact leader performance
   * Heterogeneous node in configuration and resources does not impact leader performance
   * Leader peak performance is not impacted by data replication
   * Read scalability does not impact write scalability
   * Network interconnect quality fluctuation
   * Can benefit of human expertise on false positive failure detection
   * Can benefit a minimum cluster size of two data nodes
   * Can benefit having different storage engines

This is achieved via the following drawbacks:

   * Overloading the leader can lead to data loss during failover or no failover depending of setup   
   * READ on replica is eventually consistent  
   * ACID can be preserved via route to leader always
   * READ on replica can be COMMITTED READ under usage of the 10.2 semi-sync no slave behind feature


Leader Election Asynchronous Cluster can guarantee continuity of service at no cost for the leader and in some conditions with "No Data Loss", __replication-manager__ will track failover SLA (Service Level Availability).


Because it is not always desirable to perform an automatic failover in an asynchronous cluster, __replication-manager__ enforces some tunable settings to constraint the architecture state in which the failover can happen.

In the field, a regular scenario is to have long periods of time between hardware crashes: what was the state of the replication when crash happens?

We can classify SLA and failover scenario into 3 cases:

  * Replica stream in sync   
  * Replica stream not sync but state allows failover      
  * Replica stream not sync but state does not allow failover

## Staying in sync

If the replication can be monitored in sync, the failover can be done without loss of data, provided that __replication-manager__ waits for all replicated events to be applied to the elected replica, before re-opening traffic.

In order to reach this state most of the time, we advise following settings:

### Using parallel replication

The history of MariaDB replication has reached a point where replication can almost in any case catch up with the master. It can be ensured using new features like Group Commit improvement, optimistic in-order parallel replication and semi-synchronous replication.

MariaDB 10.1 settings for in-order optimistic parallel replication:

```
slave_parallel_mode = optimistic  
slave_domain_parallel_threads = %%ENV:CORES%%  
slave_parallel_threads = %%ENV:CORES%%  
expire_logs_days = 5  
sync_binlog = 1  
log_slave_updates = ON
```

### Using semi-synchronous replication

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

__Important Note__: semisync SYNC status does not guarantee that the old leader is replication consistent with the cluster in case of crash [![MDEV-11855](https://jira.mariadb.org/browse/MDEV-11855)]  or shutdown [![MDEV-11853](https://jira.mariadb.org/browse/MDEV-11853)] of the master,the failure can leave more data in the binary log but it guarantees that no client applications have seen those pending transactions if they have not touched a replica. This leads to a situation where semisync is used to slowdown the workload to the speed of the network until it reaches a timeout where it is not possible to catch up anymore. A crash or shutdown will lead to the requirement of re-provisioning the old leader from another node in most heavy write scenarios.  
Setting rpl_semi_sync_master_wait_point to AFTER_SYNC may limit the number of extra transactions inside the binlog after a crash but those transactions would have been made visible to the clients and may have been lost during failover to an other node. It is highly recommended to keep AFTER_COMMIT to make sure the workload is safer than the state of the old master.    

### State: Not in-sync & failable

__replication-manager__ can still auto failover when replication is delayed up to a reasonable time, in such case we will possibly lose data, because we are giving to HA a bigger priority compared to the quantity of possible data lost.


This is the second SLA display. This SLA tracks the time we can failover under the conditions that were predefined in the __replication-manager__ parameters, all slave delays not yet exceeded.


Probability to lose data is increased with a single slave topology, when the slave is delayed by a long running transaction or was stopped for maintenance, catching on replication events, with heavy single threaded writes process, network performance can't catch up with the leader performance.


To limit such cases we advise usage of a 3 nodes cluster that removes such scenarios as losing a slave.

### State: Not in-sync & unfailable

The first SLA is the one that tracks the presence of a valid topology from  __replication-manager__, when a leader is reachable but number of possible failovers exceeded, time before next failover not yet reached, no slave available to failover.


This is the opportunity to work on long running WRITE transactions and split them in smaller chunks. Preferably we should minimize time in this state as failover would not be possible without big impact that  __replication-manager__ can force in interactive mode.  

A good practice is to enable slow query detection on slaves using in slow query log:
```
log_slow_slave_statements = 1
```

## Switchover workflow

__replication-manager__ prevents additional writes to set READ_ONLY flag on the old leader, if routers are still sending Write Transactions, they can pile-up until timeout, despite being killed by __replication-manager__.

Some additional caution to make sure that piled writes do not happen is that __replication-manager__ will decrease max_connections to the server to 1 and consume last possible connection by not killing himself. This works but to avoid a scenario where a node is left in a state where it cannot be connected anymore (crashing replication-manager in this critical section), we advise using extra port provided with MariaDB pool of threads feature:

```
thread_handling = pool-of-threads  
extra_port = 3307   
extra_max_connections = 10
```   
Also, to protect consistency it is strongly advised to disable *SUPER* privilege to users that perform writes, such as the The MaxScale user used with Read-Write split module is instructed to check for replication lag via writing in the leader, privileges should be lower as describe in Maxscale settings   

## Failover workflow

After checking the leader N times (failcount=5), replication-manager default behavior is to send an alert email and put itself in waiting mode until a user completes the failover or master self-heals.This is know as the On-call mode or configured via interactive = true.

When manual failover is triggered, conditions for a possible failover are checked. Per default a slave is available and up and running.

Per default following checks are disabled but are defined in the configuration template and advised to set:
- Exceeding a given replication delay (maxdelay=0)
- Failover did not happen previously in less than a given time interval (failover-time-limit=0)  
- Failover limit was not reached (failover-limit=0)

A user can force switchover or failover by ignoring those checks via the (rplchecks=false) flag or via the console "Replication Checks Change" button.

Per default Semi-Sync replication status is not checked during failover, but this check can be enforced with semi- sync replication to enable to preserve OLD LEADER recovery at all costs, and do not failover if none of the slaves are in SYNC status.

- Last semi sync status was SYNC  (failsync=false)  

A user can change this check based on what is reported by SLA in sync, and decide that most of the time the replication is in sync and when it's not, that the failover should be manual. Via http console, use "Failover Sync" button

### False positive detection

Since version 1.1 all replicas and Maxscale can be questioned for consensus detection of leader death:

The default configuration is to check only for replication heartbeat


```
failover-falsepositive-heartbeat = true
failover-falsepositive-heartbeat-timeout = 3
failover-falsepositive-maxscale = true
failover-falsepositive-maxscale-timeout = 14
```

It possible to check the death master status via some additional inetd or xinet or any http agent.

```
failover-falsepositive-external = true
failover-falsepositive-external-port = 80
```

The agent should return header of style in case he think the master is still alive
```
HTTP/1.1 200 OK\r\n
Content-Type: text/plain\r\n
Connection: close\r\n
Content-Length: 40\r\n
\r\n
```

### Rejoining old leader

Since replication-manager 1.1, rejoin of dead leader has been improved to cover more cases.

MariaDB 10.2 binary package can be colocated with replication-manager via the config option mariadb-binary-path, binaries are used to backup binlogs from remote node via mysqlbinlog --read-from-remote-server into the system tmp directory and possibly to flashback those extra binlogs

Note that the server-id to backup binlog used by replication-manager is 1000 so please don't use it on your cluster nodes

replication-manager gets 4 different cases for rejoin:

1. If GTID of the new leader at time of election is equal to GTID of the joiner, we proceed with rejoin.

2. If GTID is ahead on joiner, we backup extra events, if semisync replication was in sync status, we must do flashback to come back to a physical state that client connections have never seen.  

3. If GTID is ahead but semisync replication status at election was desynced, we flashback if replication-manager settings use the rejoin-flashback flag, lost events are saved in a crash directory in the working directory path.

4. If GTID is ahead but semisync replication status at election was desynced, we restore the joiner via mysqldump from the new leader if replication-manager settings use the rejoin-mysqldump flag.

```
autorejoin = true
autorejoin-semisync = true
autorejoin-flashback = true
autorejoin-mysqldump = false
```

## Using Maxscale

Replication-Manager can operate with MaxScale in 3 modes,  

### Mode 1
passive mode MaxScale auto-discovers the new topology after failover or switchover. Replication Manager will set the new master in MaxScale to reduce the time where it might block clients. This setup best works in 3 nodes in Master-Slaves cluster, one slave should always be available for re-discovering new topologies.

Example settings:

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

### Important note

In case all slaves are down, MaxScale can still operate on the Master with the following maxscale monitoring setup :
https://github.com/mariadb-corporation/MaxScale/blob/2.1/Documentation/Monitors/MySQL-Monitor.md#failover

```
detect_stale_master
```
In Maxscale 2.2
Failover to last node have been introduce so that transparent support of 2 nodes cluster is transaparent [![Doc]](https://github.com/mariadb-corporation/MaxScale/blob/2.1/Documentation/Monitors/MySQL-Monitor.md#failover)

Use the following example grant for your MaxScale user:

```
CREATE USER 'maxadmin'@'%' IDENTIFIED BY 'maxpwd';
GRANT SELECT ON mysql.user TO 'maxadmin'@'%';
GRANT SELECT ON mysql.db TO 'maxadmin'@'%';
GRANT SELECT ON mysql.tables_priv TO 'maxadmin'@'%';
GRANT SHOW DATABASES, REPLICATION CLIENT ON *.* TO 'maxadmin'@'%';
GRANT ALL ON maxscale_schema.* TO 'maxadmin'@'%';
```
Also, to protect consistency it is strongly advised to disable *SUPER* privilege to users that perform writes, such as the MaxScale user when the Read-Write split module is instructed to check for replication lag:

```
[Splitter Service]
type=service
router=readwritesplit
max_slave_replication_lag=30
```
### Mode 2

Operating MaxScale without monitoring is the second Replication-Manager mode via:
This mode was introduce in version 1.1 and is control via
```
maxscale-monitor = false
```
replication-manager will assign server status flags to the nodes of the cluster via MaxScale admin port. This is a good mode of operation similar to HAProxy, but it can lead to a unusable cluster if replication can't contact the proxy, so it is strongly advised to colocate the 2 services.   

### Mode 3

Driving replication-manager from Maxscale

[Automatic Failover](Automatic-Failover.md)

### Daemon mode to print maxscale status  

In version 1.1 one can see maxscale servers state in a new tab this is done and control via new parameters, default is to use maxadmin tcp row protocol via maxscale-get-info-method = "maxadmin"
A more robust configuration can be enable via load ing the maxinfo plugin in maxscale that provide a JSON REST service to replication-manager

```
maxscale = true
# maxinfo|maxadmin
maxscale-get-info-method = "maxadmin"
maxscale-maxinfo-port = 4002
maxscale-host = "192.168.0.201"
maxscale-port = 4003
```

### Maxscale Binlog Server and Slave Relay

All MariaDB Nodes should have same binlog prefix
```
bin_log='mariadb-bin'
```

Maxscale settings
```
router_options=mariadb10-compatibility=1,server-id=999,user=skysql,password=skyvodka,send_slave_heartbeat=on,transaction_safety=on,semisync=1
```

replication-manager
Add the binlog server and port in the list of hosts

```
force-slave-gtid-mode = false
maxscale-binlog = true
maxscale-binlog-port = 3306
```
Note that maxscale 2.2 can support MariaDB GTID so force-gtid-mode=false is not needed anymore
part of task https://github.com/mariadb-corporation/MaxScale/tree/MXS-1075

```
  transaction_safety=On,mariadb10-compatibility=On,mariadb_gtid=On
```  

## Using Haproxy

Haproxy can be used but only in same server as replication-manager, replication-manager will prepare a configuration file for haproxy for every cluster that it manage, this template is located in the share directory used by replication-manager. For safety haproxy is not stopped when replication-manager is stopped

```
haproxy = true
haproxy-binary-path = "/usr/sbin/haproxy"

# Read write traffic
# Read only load balance least connection traffic
haproxy-write-port = 3306
haproxy-read-port = 3307
```

## Usage

```
  agent       Starts replication monitoring agent
  bootstrap   Bootstrap a replication environment
  failover    Failover a dead master
  keygen      Generate a new encryption key
  monitor     Start the interactive replication monitor
  password    Encrypt a clear text password
  provision   Provision a replica server
  switchover  Perform a master switch
  topology    Print replication topology
  version     Print the replication manager version number
  test        Run non regression tests
```

To print the help and option flags for each command, use `replication-manager [command] --help`

Flags help for the monitor command is given below.

### Monitor options

```
Flags:
      --autorejoin                    Automatically rejoin a failed server to the current master (default true)
      --check-type string             Type of server health check (tcp, agent) (default "tcp")
      --connect-timeout int           Database connection timeout in seconds (default 5)
      --daemon                        Daemon mode. Do not start the Termbox console
      --failcount int                 Trigger failover after N failures (interval 1s) (default 5)
      --failover-at-sync              Only failover when state semisync is sync for last status
      --failover-limit int            Quit monitor after N failovers (0: unlimited)
      --failover-time-limit int       In automatic mode, Wait N seconds before attempting next failover (0: do not wait)
      --gtidcheck                     Do not initiate switchover unless one of the slaves is fully synced
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
      --rplchecks                     Failover to ignore replications checks (default true)
      --spider                        Turn on spider detection
      --wait-kill int                 Wait this many milliseconds before killing threads on demoted master (default 5000)

Global Flags:
      --hosts string     List of MariaDB hosts IP and port (optional), specified in the host:[port] format and separated by commas
      --keypath string   Encryption key file path (default "/etc/replication-manager/.replication-manager.key")
      --interactive      Ask for user interaction when failures are detected (default true)
      --log-level int    Log verbosity level
      --rpluser string   Replication user in the [user]:[password] format
      --user string      User for MariaDB login, specified in the [user]:[password] format
      --verbose          Print detailed execution info
```

## Command line switchover

Run replication-manager in switchover mode with master host db1 and slaves db2 and db3:

`replication-manager switchover --hosts=db1,db2,db3 --user=root --rpluser=replicator --interactive`

## Command line failover

Run replication-manager in non-interactive failover mode, using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`replication-manager failover --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" --verbose --maxdelay=15`

## Command line bootstrap

With some already exiting database nodes but no replication  setup replication-manager enable you to init the replication on various topology
master-slave | master-slave-no-gtid | maxscale-binlog | multi-master | multi-tier-slave

`replication-manager --config-group=cluster_test_3_nodes bootstrap --clean-all --topology="multi-tier-slave"`

## Command line monitor

Start replication-manager in console mode to monitor the cluster:

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass`

![mrmconsole](https://cloud.githubusercontent.com/assets/971260/16738035/45f2bbf2-4794-11e6-8286-65f9a3179e31.png)

The console mode accepts several commands:

```
Ctrl-D  Print debug information
Ctrl-F  Manual Failover
Ctrl-I  Toggle automatic/manual failover mode
Ctrl-R  Set slaves read-only
Ctrl-S  Switchover
Ctrl-Q  Quit
Ctrl-W  Set slaves read-write
```

## Using monitor in daemon mode

Start replication-manager in background to monitor the cluster, using the http server to control the daemon

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --daemon --http-server`

The http server is accessible on http://localhost:10001 by default, and looks like this:

![mrmdash](https://cloud.githubusercontent.com/assets/971260/16737848/807d6106-4793-11e6-9e65-cd86fdca3b68.png)

The http dashboard is an experimental angularjs application, please don't use it in production as it has no protected access for now (or use creativity to restrict access to it).

Start replication-manager in automatic daemon mode:

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --daemon --interactive=false`

This mode is similar to the normal console mode with the exception of automated master failovers. With this mode, it is possible to run the replication-manager as a daemon process that manages a database cluster. Note that the `--interactive=false` option is required with the `--daemon` option to make the failovers automatic. Without it, the daemon only passively monitors the cluster.

## Using configuration files

All the options above are settable in a configuration file that must be located in `/etc/replication-manager/config.toml`. Check `etc/config.toml.sample` in the repository for syntax examples.

It is strongly advice to create a dedicated user for the management user !  
Management user (given by the --user option) and Replication user (given by the --repluser option) need to be given privileges to the host from which `replication-manager` runs. Users with wildcards are accepted as well.

The management user needs at least the following privileges: `SUPER`, `REPLICATION CLIENT` and `RELOAD`

The replication user needs the following privilege: `REPLICATION SLAVE`

## Using external scripts

Replication-Manager calls external scripts and provides following parameters in this order: Old leader host and new elected leader.

## Using multi master

`replication-manager` supports 2-node multi-master topology detection. It is required to specify it explicitely in `replication-manager` configuration, you just need to set one preferred master and one very important parameter in MariaDB configuration file.  

```
read_only = 1
```

This flag ensures that in case of split brain + leader crash, when old leader is reintroduced it will not show up as a possible leader for WRITES.

MaxScale can follow multi=master setting by tracking the read-only flag and route queries to the writable node.

```    
[Multi-Master Monitor]
type=monitor
module=mmmon
servers=server1,server2,server3
user=myuser
passwd=mypwd
detect_stale_master=true
```

## Using Muti Tier slaves

Replication-Manager have support for replication tree or relay slaves architecture, in case of master death one of the slaves under the relay is promoted as a master.   
Add following parameter to your cluster section
```
multi-tier-slave=true
```

## Force best practices

Since version 1.1 replication can enforce the best practices about the replication usage. It dynamically configure the MariaDB it does monitor. Note that such enforcement will be lost if replication manager monitoring is shutdown and the MariaDB restarted. The command line usage do not enforce but default config file do, so disable what may not be possible in your custom production setup.   

```
force-slave-heartbeat= true
force-slave-heartbeat-retry = 5
force-slave-heartbeat-time = 3
force-slave-gtid-mode = true
force-slave-semisync = true
force-slave-readonly = true
force-binlog-row = true
force-binlog-annotate = true
force-binlog-slowqueries = true
force-inmemory-binlog-cache-size = true
force-disk-relaylog-size-limit = true
force-sync-binlog = true
force-sync-innodb = true
force-binlog-checksum = true
```
## Active standby and external arbitrator

When inside a single zone we would flavor single replication-manager to failover  using keepalived or corosync or etcd but if you run on 2 DC it is possible to run two replication-manager in the same infrastructure. Both replication-manager will start pinging each others via the http mode so make sure you activate the web mode of replication-manager

To enable standby replication-manager activate the following setting on both replication-manager

```
# Enterpise SAS identity
arbitration-external = true
arbitration-external-secret = "1378793252.mariadb.com"
arbitration-external-hosts = "88.191.151.84:80"
arbitration-peer-hosts ="127.0.0.1:10002"
# Unique value on each replication-manager
arbitration-external-unique-id = 0
```

Give each arbitration-external-unique-id some different value, this define the unique replication-manager instance

Also define one secret arbitration-external-secret it should be unique across all users of replication-manager, it is use to identify your cluster, organization name and random alpha-numeric is very welcome, declare this name to our team. If you wan't to enforce unicity.

Give each instance it's peer replication-manager node

On instance "127.0.0.1:10001"
arbitration-peer-hosts ="127.0.0.1:10002"

On instance "127.0.0.1:10002"
arbitration-peer-hosts ="127.0.0.1:10001"    

Once done start one replication-manager.

```
INFO[2017-03-20T09:48:38+01:00] [cluster_test_2_nodes] ERROR :Get http://127.0.0.1:10001/heartbeat: dial tcp 127.0.0.1:10001: getsockopt: connection refused
INFO[2017-03-20T09:48:38+01:00] [cluster_test_2_nodes] INFO : Splitbrain     
INFO[2017-03-20T09:48:38+01:00] [cluster_test_3_nodes] CHECK: External Abitration
INFO[2017-03-20T09:48:38+01:00] [cluster_test_3_nodes] INFO :Arbitrator say winner
INFO[2017-03-20T09:48:40+01:00] [cluster_test_2_nodes] ERROR :Get http://127.0.0.1:10001/heartbeat: dial tcp 127.0.0.1:10001: getsockopt: connection refused
INFO[2017-03-20T09:48:40+01:00] [cluster_test_2_nodes] INFO : Splitbrain     
INFO[2017-03-20T09:48:40+01:00] [cluster_test_3_nodes] CHECK: External Abitration
INFO[2017-03-20T09:48:40+01:00] [cluster_test_3_nodes] INFO Arbitrator say :winner
```

What can be observe is the split brain detection. Because your are the first instance to start, the peer replication-manager is not joinable so it ask for an arbitration to arbitration-external-hosts = "88.191.151.84:80", provided to you as a SAS deployment of the arbitrator daemon. The arbitrator will enable that node to enter Active Mode  

When you start the peer replication-manager, the split brain is resolve and replication-manager will detect an other active instance is running so it will get the Standby mode

Note that failover in such mode is also requesting an arbitration. If arbitrator can't be contacted, you can come back to normal command line mode to failover but make sure you stopped all other replication-manager running .

It's possible to run a private arbitrator via similar configuration

```
[arbitrator]
hosts = "192.168.0.201:3306"
user = "user:password"
title = "arbitrator"     
[default]
```

And start it via
/usr/bin/replication-manager arbitrator --arbitrator-port=80

## Metrics

replication-manager 1.1 embed a graphite server and can serve as a carbon relay server, some graph are display via the giraffe JS library in the internal http server. One can create it's own dashboard via Grafana.

very few metrics are yet push inside carbon, the metrics are pushed with the server-id prefix name. to get unicity against nodes  

Contact the authors for contributions or custom metrics to be added.

## Non-regression tests

A testing framework is available via http or in command line.
Setting the `test` variable in the predefined testing cluster in config file:
```  
[Cluster_Test_2_Nodes]
hosts = "127.0.0.1:3310,127.0.0.1:3311"
user = "root:"
rpluser = "root:"
title = "cluster1"
connect-timeout = 1
prefmaster = "127.0.0.1:3310"
haproxy-write-port=3303
haproxy-read-port=3304
test=true
```  

The tests can be run on am existing cluster but the default is to bootstrap a local replication cluster via the path to some MariaDB server installed locally.  
Some tests are requiring sysbench and haproxy so it's advised to set:    

```  
mariadb-binary-path = "/usr/local/mysql/bin"
sysbench-binary-path = "/usr/sbin/sysbench"
sysbench-threads = 4
sysbench-time = 60
haproxy = true
haproxy-binary-path = "/usr/sbin/haproxy"
```

Command line test printing

```
./replication-manager --config=/etc/replication-manager/mrm.cnf --config-group=cluster_test_2_nodes --show-tests=true test
INFO[2017-02-22T21:40:02+01:00] [testSwitchOverLongTransactionNoRplCheckNoSemiSync testSwitchOverLongQueryNoRplCheckNoSemiSync testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync testSlaReplAllDelay testFailoverReplAllDelayInteractive testFailoverReplAllDelayAutoRejoinFlashback testSwitchoverReplAllDelay testSlaReplAllSlavesStopNoSemiSync testSwitchOverReadOnlyNoRplCheck testSwitchOverNoReadOnlyNoRplCheck testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck testSwitchOverBackPreferedMasterNoRplCheckSemiSync testSwitchOverAllSlavesStopRplCheckNoSemiSync testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck testSwitchOverAllSlavesDelayRplCheckNoSemiSync testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync testFailOverAllSlavesDelayNoRplChecksNoSemiSync testFailOverAllSlavesDelayRplChecksNoSemiSync testFailOverNoRplChecksNoSemiSync testNumberFailOverLimitReach testFailOverTimeNotReach]
```
Command-line running some tests via passing a list of tests in run-tests
ALL is a special test to run all available tests.
```
./replication-manager --config=/etc/replication-manager/mrm.cnf --config-group=cluster_test_2_nodes   --run-tests=testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck test  
```

## System requirements

`replication-manager` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use GTID for replication. Old-style positional replication is not supported (yet).

## Bugs

Check https://github.com/tanji/replication-manager/issues for a list of issues.

## Downloads

As of today we build portable binary tarballs, Debian Jessie, Ubuntu, CentOS 6 & 7 packages.

Check https://github.com/tanji/replication-manager/releases for official releases.

Nightly builds available on https://orient.dragonscale.eu/replication-manager/nightly

## Contributors

[Building from source](BUILD.md)

## Features

### 1.0 Features GA

 * High availability support with leader election
 * Semi-sync replication support
 * Provisioning
 * Bootstrap
 * Http daemon mode
 * Email alerts
 * Configuration file
 * AES Password encryption
 * 2 nodes Multi Master Switchover support
 * On-leave mode
 * Failover SLA tracking
 * Log facilities and verbosity
 * Docker images
 * Docker deployment via OpenSVC in Google Cloud
 * Docker deployment via OpenSVC on premise for Ubuntu and OSX
 * Non regression tests via http
 * Haproxy wrapper

### 1.1 Features Beta

 * Multi cluster support
 * Flashback and dump rejoin  
 * Forced rejoin with lost events, backup lost events  
 * Trends store  
 * Maxscale 2 nodes master-slave driving
 * Replication heartbeat false positive detection
 * Maxscale state server display
 * MaxScale integration to disable traffic on READ_ONLY flag https://jira.mariadb.org/browse/MXS-778
 * Trends display
 * Force replication best practice
 * Non regression tests via command line
 * Maxscale binlog server support
 * Active Standby replication-manager via external arbitrator

### 1.1 Roadmap

 * Load and non regression simulator
 * Etcd integration
 * Agent base server stop leader on switchover   
 * MariaDB integration of no slave left behind https://jira.mariadb.org/browse/MDEV-8112


## Authors

Guillaume Lefranc <guillaume@signal18.io>

Stephane Varoqui <stephane@mariadb.com>

### Special Thanks

Thanks to Markus Mäkelä from the MaxScale team for his valuable time contributions, Willy Tarreau from HaProxy, The fantastic core team at MariaDB, Kristian Nielsen on the GTID and parallel replication feature. Claudio Nanni from MariaDB support on his effort to test SemiSync, All early adopters like Pierre Antoine from Kang, Nicolas Payart and Damien Mangin from CCM, Tristan Auriol from Bettr, Madan Sugumar and Sujatha Challagundla. Community members for inspiration or reviewing: Shlomi Noach for Orchestrator, Yoshinori Matsunobu for MHA, Johan Anderson for S9 Cluster Control.

### License

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## Version

__replication-manager__ 1.1.0
