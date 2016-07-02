## replication-manager [![Build Status](https://travis-ci.org/mariadb-corporation/replication-manager.svg?branch=master)](https://travis-ci.org/mariadb-corporation/replication-manager)

## NAME

replication-manager -- MariaDB replication manager utility

## SYNOPSIS

`replication-manager [OPTIONS]`

## DESCRIPTION

**replication-manager** is an hight availabilty solution to manage MariaDB 10.x GTID replication.  
It detect topology and monitor health to trigger slave to master promotion (aka switchover), or elect a new master on failure detection (aka switchover).

To perform switchover, preserving data consistancy, replication-manager uses a mechanism similar to common MySQL failover tools such as MHA:

  * Verify replication settings
  * Check (configurable) replication on the slaves
  * Check for long running queries on master
  * Elect a new master (usually the most up to date, but it could also be a designated candidate)
  * Put down the IP address on master by calling an optional script
  * Reject writes on master by calling FLUSH TABLES WITH READ LOCK
  * Reject writes on master by setting READ_ONLY FLAG
  * Reject writes on master by decreasing MAX_CONNECTIONS
  * Kill prnding connections on master if any remaining
  * Watching for all slaves to catch up to the current GTID position
  * Promote the candidate slave to be a new master
  * Put up the IP address on new master by calling an optional script
  * Switch other slaves and old master to be slaves of the new master and set them as read-only

When **replication-manager** is used as an arbitrator it will have to drive a proxy that route the database traffic to the leader database node (aka the MASTER). We can advice usage of:

- A layer 7 proxy as MariaDB MaxScale that can transparently follow a newly elected topology. 

- Some Monitor less proxies, **replication-manager**  call scripts that set and reload the new configuration of the leader route. A common scenrio is an VRRP Actif Passif HaProxy sharing configuration via a network disk with the **replication-manager** scripts           

- Use **replication-manager** as an API component of a group communication cluster. MRM can be call as a corossync ressource that move along side a VIP, the monitoring of the cluster is in this case already in charge of the GCC   

## ADVANTAGES

A **replication-manager** Leader Election Cluster is best to use in such scenario:

   * Node diss fonctionning do not impact the Availlability and Performance  
   * Node heterogenous in configuration and ressources do not impact the Availlability and Performance  
   * Leader Pick Performance is not impacted by the data replication 
   * Read scalability do not impact write scalability 
   * Network inter connect quality fluctuation
   * Can benefit human expertise on false positive failure detection

This is achive via folowing drawback:

   * Over loading the leader can lead to data lost during failover  
   * Replicat READ is eventualy consistant  
   * ACID can be preserve via route to leader always 
   * Replicat READ can be quaranty COMMITED READ under monitoring of semiync no slave behind status 
    
The history of MariaDB replicaton as reach a point that replication can almost in any case catch with the master. It can be ensure using new features like Group Commit improvement, optimistic in order parrallel replication and semi syncrhonus replication. 

Giving the available hardware to ensure that, a synchronus cluster and an asynchonus cluster will always deliver. But Leader Asyncronus Cluster can garanty continuity of service at close to zero cost for the leader under no data lost SLA.      

## PROCEDURAL COMMAND LINE EXAMPLES 

Run replication-manager for a switchover interactive mode with master host db1 and slaves db2 and db3:

`replication-manager --hosts=db1,db2,db3 --user=root --rpluser=replicator --interactive --switchover=keep`

Run replication-manager for a failover in interactive mode using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`replication-manager --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" --verbose --maxdelay 15 --failover=monitor`

Run replication-manager for a failover non-interactively of a dead master (similar setup as above):

`replication-manager --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" --post-failover-script="/usr/local/bin/vipup.sh" --failover=force --interactive=false`

## MONITORING 

Start replication-manager in terminal to monitor the cluster 

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass`

Start replication-manager in background to monitor the cluster

`replication-manager monitor --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --daemon`


## OPTIONS

At a minimum, required options are: a list of hosts (replication-manager can autodetect topologies), user with privileges (ALL), and replication user (with REPLICATION SLAVE privileges)

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

## SYSTEM REQUIREMENTS

`replication-manager` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use GTID for replication. Old-style positional replication is not supported (yet).

## BUGS

Check https://github.com/mariadb-corporation/replication-manager/issues for a list of issues.

## ROADMAP

 * High availability support with etcd

 * Semi-sync replication support

 * Provisioning

## AUTHOR

Guillaume Lefranc <guillaume@mariadb.com>

## COPYRIGHT

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 3.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## VERSION

**replication-manager** 0.6.0
