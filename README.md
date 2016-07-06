## replication-manager [![Build Status](https://travis-ci.org/mariadb-corporation/replication-manager.svg?branch=master)](https://travis-ci.org/mariadb-corporation/replication-manager)

[![Join the chat at https://gitter.im/mariadb-corporation/replication-manager](https://badges.gitter.im/mariadb-corporation/replication-manager.svg)](https://gitter.im/mariadb-corporation/replication-manager?utm_source=badge&utm_medium=badge&utm_campaign=pr-badge&utm_content=badge)

## NAME

replication-manager -- MariaDB replication manager utility

## SYNOPSIS

`replication-manager [OPTIONS]`

## DESCRIPTION

**replication-manager** allows users to monitor interactively MariaDB 10.x GTID replication health and trigger slave to master promotion (aka switchover), or elect a new master in case of failure (aka switchover).

At a minimum, required options are: a list of hosts (replication-manager can autodetect topologies), user with privileges (ALL), and replication user (with REPLICATION SLAVE privileges)

To perform switchover, replication-manager uses a mechanism similar to common mysql failover tools such as MHA:

  * Verify replication settings
  * Check (configurable) replication on the slaves
  * Check for long running queries on master
  * Elect a new master (usually the most up to date, but it could also be a designated candidate)
  * Put down the IP address on master by calling an optional script
  * Reject writes on master by calling FLUSH TABLES WITH READ LOCK
  * Kill long running threads on master if any remaining
  * Watching for all slaves to catch up to the current GTID position
  * Promote the candidate slave to be a new master
  * Put up the IP address on new master by calling an optional script
  * Switch other slaves and old master to be slaves of the new master and set them as read-only

## EXAMPLES

Start mariadb-repmgr in switchover interactive mode with master host db1 and slaves db2 and db3:

`replication-manager --hosts=db1,db2,db3 --user=root --rpluser=replicator --interactive --switchover=keep`

Start mariadb-repmgr in interactive failover mode using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`replication-manager --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" --verbose --maxdelay=15 --failover=monitor`

Failover non-interactively a dead master (similar setup as above):

`replication-manager --hosts=db1:3306,db2:3306,db2:3306 --user=root:pass --rpluser=repl:pass --pre-failover-script="/usr/local/bin/vipdown.sh" --post-failover-script="/usr/local/bin/vipup.sh" --failover=force --interactive=false`

## OPTIONS

  * --autorejoin `<boolean>`

    Automatically rejoin a failed server to the current master (default true)

  * --connect-timeout `<seconds>`

    Database connection timeout in seconds (default 5)

  * --failover `<state>`

    Start the replication manager in failover mode. `state` can be either `monitor` or `force`, whether the manager should run in monitoring or command line mode. The action will result in removing the master of the current replication topology.

  * --failcount `<count>`

    Trigger failover after `count` failures (interval 1s) (default 5)

  * --failover-limit `<count>`

    In auto-monitor mode, quit after `count` failovers (0: unlimited)

  * --failover-time-limit `<seconds>`

    In auto-monitor mode, wait `seconds` seconds before attempting next failover (0: do not wait)

  * --gtidcheck `<boolean>`

    Check that GTID sequence numbers are identical before initiating failover. This must be used if you want your servers to be perfectly in sync before initiating master switchover. If true, mariadb-repmgr will wait for the slaves to be in sync before initiating. (default false)

  * --hosts `<address>`

    List of MariaDB hosts IP and port (optional), specified in the `host[:port],` format and comma-separated.

  * --ignore-servers `<address>`

    List of servers in the `host[:port]` format to be ignored for slave promotion operations.

  * --interactive `<boolean>`

    Runs the MariaDB monitor in interactive mode, asking for user interaction when failures are detected. A value of false also allows mariadb-repmgr to invoke switchover without displaying the interactive monitor. (default true)

  * --logfile `<path>`

    Write MRM messages to a log file.

  * --maxdelay `<seconds>`

    Maximum slave replication delay allowed for initiating switchover, in seconds.

  * --post-failover-script `<path>`

    Path of post-failover script, to be invoked after new master promotion.

  * --pre-failover-script `<path>`

    Path of pre-failover script to be invoked before master election.

  * --prefmaster `<address>`

    Preferred candidate server for master failover, in `host[:port]` format.

  * --readonly `<boolean>`

    Set slaves as read-only when performing switchover. (default true)

  * --rpluser `<user>[:password]`

    Replication user and password. This user must have REPLICATION SLAVE privileges and is used to setup the old master as a new slave.

  * --switchover `<action>`

    Starts the replication manager in switchover mode. Action can be either `keep` to degrade the old master as a new slave, or `kill` to remove the old master from the replication topology.

  * --socket `<path>`

    Path of MariaDB unix socket. (default "/var/run/mysqld/mysqld.sock")

  * --user `<user>[:password]`

    User for MariaDB login, specified in the `user[:password]` format. Must have administrative privileges. This user is used to perform switchover.

  * --verbose

    Print detailed execution information.

  * --version

    Return software version.

  * --wait-kill `<milliseconds>`

    Wait this many milliseconds before killing threads on demoted master. (default 5000)

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
