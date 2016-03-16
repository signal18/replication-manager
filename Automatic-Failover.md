#MariaDB automatic failover with MaxScale and MariaDB Replication Manager

**!!! Mandatory disclaimer !!! The technique described in this documentation is highly experimental, so use at your own risk. Neither me nor MariaDB Corporation will be held responsible if anything bad happens to your servers.**

## Context
MaxScale 1.3.0 and above can call external scripts on monitor events. In the case of a classic Master-Slave setup, this can be used for automatic failover and promotion using MariaDB Replication Manager. The following use case is exposed using three MariaDB servers (one master, two slaves) and a MaxScale server. Please refer to my [Vagrant files](https://github.com/tanji/maria-vagrant-ms) if you want to jumpstart such a testing platform.

## Requirements
* A replication-manager binary, version 0.6.0 or above. Grab it from the github Releases page, and extract in /usr/local/bin/ on your MaxScale server.
* A working MaxScale installation with MySQL Monitor setup and whatever router you like. Please refer to the MaxScale docs for more information on how to configure it correctly.

## MaxScale installation and configuration
The MySQL Monitor has to be configured to send scripts. Add the following three lines to your [MySQL Monitor] section:

    monitor_interval=1000
    script=/usr/local/bin/replication-manager --user root:admin --rpluser repluser:replpass --hosts $INITIATOR,$NODELIST --failover=force --interactive=false
    events=master_down

### Failover script
Make sure to configure user and repluser script variables to whatever your user:password pairs are for administrative user and replication user.

User must have ALL privileges, e.g. root, and Repluser must have at least REPLICATION SLAVE privileges.

### Testing that failover works

Let's check the current status, where I have configured server3 as a master and server1-2 as slaves:

    $ maxadmin -pmariadb "show servers"
    Server 0x1b1f440 (server1)
    	Server:				192.168.56.111
    	Status:               		Slave, Running
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			1
    	Master Id:			3
    	Slave Ids:			
    	Repl Depth:			1
    	Number of connections:		0
    	Current no. of conns:		0
    	Current no. of operations:	0
    Server 0x1b1f330 (server2)
    	Server:				192.168.56.112
    	Status:               		Slave, Running
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			2
    	Master Id:			3
    	Slave Ids:			
    	Repl Depth:			1
    	Number of connections:		8
    	Current no. of conns:		1
    	Current no. of operations:	0
    Server 0x1a7b2c0 (server3)
    	Server:				192.168.56.113
    	Status:               		Master, Running
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			3
    	Master Id:			-1
    	Slave Ids:			1, 2
    	Repl Depth:			0
    	Number of connections:		2
    	Current no. of conns:		0
    	Current no. of operations:	0

Everything looks normal. Let's try failover by shutting down server3.

    server3# service mysql stop
         * Stopping MariaDB database server mysqld                                                                                                                                                                                                                                                                      [ OK ]

Let's check the server status again:

    $ maxadmin -pmariadb "show servers"
    Server 0x1b1f440 (server1)
    	Server:				192.168.56.111
    	Status:               		Slave, Running
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			1
    	Master Id:			2
    	Slave Ids:			
    	Repl Depth:			1
    	Number of connections:		0
    	Current no. of conns:		0
    	Current no. of operations:	0
    Server 0x1b1f330 (server2)
    	Server:				192.168.56.112
    	Status:               		Master, Running
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			2
    	Master Id:			-1
    	Slave Ids:			1
    	Repl Depth:			0
    	Number of connections:		8
    	Current no. of conns:		1
    	Current no. of operations:	0
    Server 0x1a7b2c0 (server3)
    	Server:				192.168.56.113
    	Status:               		Down
    	Protocol:			MySQLBackend
    	Port:				3306
    	Server Version:			10.0.19-MariaDB-1~trusty-log
    	Node Id:			3
    	Master Id:			-1
    	Slave Ids:			
    	Repl Depth:			0
    	Number of connections:		2
    	Current no. of conns:		0
    	Current no. of operations:	0

MariaDB Replication Manager has promoted server2 to be the new master, and server1 has been reslaved to server2. server3 is now marked as down. If you restart server3, MaxScale should pick it up because Replication Manager has the autorejoin feature which is turned on by default.

### Tuning Replication Manager options
Replication Manager comes with a comprehensive option set that can help for tuning behavior. Two options come to mind:

`--ignore-servers` allows to explicitely ignore servers for promotion purposes, if you have a slave in the topology that should not become a master.

`--prefmaster` indicates a preferred candidate master for failover, so if this server is present in the list of slaves, it will be picked up ahead of others.

### Going further
The Replication Manager can also be used without modifying the MaxScale configuration, for example to perform Master Switchover.

In this case, it will autodetect the topology itself, and MaxScale will pick up the changes.

Just make sure that you set monitor_interval to a low value (e.g. `monitor_interval=500`) so that MaxScale picks up the topology changes quickly.
