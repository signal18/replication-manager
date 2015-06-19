#MariaDB automatic failover with MaxScale and MariaDB Replication Manager

**!!! Mandatory disclaimer !!! The technique described in this documentation is highly experimental, so use at your own risk. Neither me nor MariaDB Corporation will be held responsible if anything bad happens to your servers.**

## Context
MaxScale 1.2.0 and above can call external scripts on monitor events. In the case of a classic Master-Slave setup, this can be used for automatic failover and promotion using MariaDB Replication Manager. The following use case is exposed using three MariaDB servers (one master, two slaves) and a MaxScale server. Please refer to my [Vagrant files](https://github.com/tanji/maria-vagrant-ms) if you want to jumpstart such a testing platform.

## Requirements
* A mariadb-repmgr binary, version 0.3.0 or above. Grab it from the github Releases page, and extract in /usr/local/bin/ on your MaxScale server.
* A working MaxScale installation with MySQL Monitor setup and whatever router you like. Please refer to the MaxScale docs for more information on how to configure it correctly.

## MaxScale installation and configuration
The MySQL Monitor has to be configured to send scripts. Add the following three lines to your [MySQL Monitor] section:

    monitor_interval=1000
    script=/usr/local/bin/failover.sh
    events=master_down

### Failover script
As of the current MaxScale development branch, custom options are not supported, so we have to use a wrapper script to call MariaDB Replication Manager. Create the following script in `/usr/local/bin/failover.sh`:

    #!/bin/bash
    # failover.sh
    # wrapper script to repmgr
    
    # user:password pair, must have administrative privileges.
    user=root:admin
    # user:password pair, must have REPLICATION SLAVE privileges. 
    repluser=repluser:replpass
    
    ARGS=$(getopt -o '' --long 'event:,initiator:,nodelist:' -- "$@")
    
    eval set -- "$ARGS"
    
    while true; do
    	case "$1" in
    		--event)
    			shift;
    			event=$1
    			shift;
    		;;
    		--initiator)
    			shift;
    			initiator=$1
    			shift;
    		;;
    		--nodelist)
    			shift;
    			nodelist=$1
    			shift;
    		;;
    		--)
    			shift;
    			break;
    		;;
    	esac
    done
    cmd="mariadb-repmgr -host $initiator -user $user -rpluser $repluser -slaves $nodelist -failover=dead"
    eval $cmd

Make sure to configure user and repluser script variables to whatever your user:password pairs are for administrative user and replication user. Also make sure to make the script executable (`chown +x`) as it's very easy to forget that step.

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

MariaDB Replication Manager has promoted server2 to be the new master, and server1 has been reslaved to server2. server3 is now marked as down. If you restart server3, it will be marked as "Running" but not as slave - to put it back in the cluster, you just need to repoint replication with GTID with this command: `CHANGE MASTER TO MASTER_HOST='server1', MASTER_USE_GTID=CURRENT_POS;`
The failover script could handle this case as well, although it remains to be tested.