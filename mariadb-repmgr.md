mariadb-repmgr(1) -- MariaDB replication manager
===========================================

## NAME

mariadb-repmgr -- MariaDB replication manager utility

## SYNOPSIS

`mariadb-repmgr [OPTIONS]`

## DESCRIPTION

**mariadb-repmgr** allows users to monitor interactively MariaDB 10.x GTID replication health and trigger slave to master promotion (aka switchover).

At a minimum, `mariadb-repmgr` requires a master server host to be provided on the command line as well as a list of slaves. Topology auto-detection is not yet supported due to some shortcomings in how mysqld slaves reports hosts to master.

To perform switchover, mariadb-repmgr uses a mechanism similar to common mysql failover tools such as MHA:

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
  * Switch other slaves and old master to be slaves of the new master

## EXAMPLES

Start mariadb-repmgr in interactive mode with master host db1 and slaves db2 and db3:

`mariadb-repmgr -host:db1 -slaves db2,db3`

Start mariadb-repmgr in interactive mode using full host and port syntax, using root login for management and repl login for replication switchover, with failover scripts and added verbosity. Accept a maximum slave delay of 15 seconds before performing switchover:

`mariadb-repmgr -host=db1:3306 -slaves=db2:3306,db2:3306 -user=root:pass -rpluser=repl:pass -pre-failover-script="/usr/local/bin/vipdown.sh" -post-failover-script="/usr/local/bin/vipup.sh" -verbose -maxdelay 15`

## OPTIONS

  * -gtidcheck `<boolean>`

    Check that GTID sequence numbers are identical before initiating failover. Default false. This must be used if you want your servers to be perfectly in sync before initiating master switchover. If false, mariadb-repmgr will wait for the slaves to be in sync before initiating.
  
  * -host `<address>:[port]`

    MariaDB master host IP and port (optional), specified in the `host:[port]` format.

  * -interactive `<boolean>`

    Runs the MariaDB monitor in interactive mode (default). A value of false allows mariadb-repmgr to invoke switchover without displaying the interactive monitor.

  * -maxdelay `<seconds>`

    Maximum slave replication delay allowed for initiating switchover, in seconds.

  * -post-failover-script `<path>`

    Path of post-failover script, to be invoked after new master promotion.
  
  * -pre-failover-script `<path>`
    Path of pre-failover script to be invoked before master election.

  * -prefmaster `<address>`

    Preferred candidate server for master failover, in `host:[port]` format.
  
  * -readonly `<boolean>`

    Set slaves as read-only when performing switchover. Default true.

  * -rpluser `<user>:[password]`

    Replication user and password. This user must have REPLICATION SLAVE privileges and is used to setup the old master as a new slave.

  * -slaves `<address>`

    List of slaves connected to the current MariaDB master, separated by a comma.

  * -socket `<path>`

    Path of MariaDB unix socket. Default is "/var/run/mysqld/mysqld.sock"

  * -user `<user>:[password]`

    User for MariaDB login, specified in the `user:[password]` format. Must have administrative privileges. This user is used to perform switchover.

  * -verbose

    Print detailed execution information.

  * -version

    Return softawre version.

  * -wait-kill `<msecs>`

    Wait this many milliseconds before killing threads on demoted master. Default 5000 ms.

## SYSTEM REQUIREMENTS

`mariadb-repmgr` is a self-contained binary, which means that no dependencies are needed at the operating system level.
On the MariaDB side, slaves need to use GTID for replication. Old-style positional replication is not supported.

## BUGS

Check https://github.com/tanji/mariadb-tools/issues for a list of issues.

## AUTHOR

Guillaume Lefranc <guillaume@mariadb.com>

## COPYRIGHT

THIS PROGRAM IS PROVIDED “AS IS” AND WITHOUT ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, WITHOUT LIMITATION, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE.

This program is free software; you can redistribute it and/or modify it under the terms of the GNU General Public License as published by the Free Software Foundation, version 2; OR the Perl Artistic License. On UNIX and similar systems, you can issue `man perlgpl` or `man perlartistic` to read these licenses.

You should have received a copy of the GNU General Public License along with this program; if not, write to the Free Software Foundation, Inc., 59 Temple Place, Suite 330, Boston, MA 02111-1307 USA.

## VERSION

**mariadb-repmgr** 0.2.2
