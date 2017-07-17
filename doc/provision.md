* [Overview](#overview)
* [Install](#install)

## Overview

Since version 1.1 replication-manager can use agent base cluster provisioning using the OpenSVC provisioning framework.

As of today following software provisioning are possible for:
  - [x] MariaDB
  - [x] MaxScale proxy

We enable those type of micro-services:
  - [x] Docker
  - [x] Package

Each micro-service define some collection of resources:
  - [x] An existing disk device if none a loopback device for service data
  - [x] An existing disk device if none a loopback device for the docker db
  - [x] A file system of type zfs|ext4|xfs|hfs|aufs
  - [x] A file system pool of type lvm|zpool|none
  - [x] IP address that need to be unused in the network

OpenSVC drivers can provision disk device on all kine of external SAN arrays, contact them if you need other type of disk provisioning on your architecture.

Provisioning resources should be defined in the configuration file cluster section and as of today are uniform over the full cluster.
```
prov-db-service-type = "docker"
prov-db-disk-fs = "zfs"
prov-db-disk-pool = "zpool"
prov-db-disk-type = "loopback"
prov-db-disk-device = "/srv"
prov-db-net-iface = "br0"
prov0-db-net-gateway = "192.168.1.254"
prov-db-net-mask = "255.255.255.0"
```

Database Micro Service bootstrap some configurations files adapted to the replication-manager cluster parameters:
```
 prov-db-memory                           
  Memory in M for micro service VM (default "256")
 prov-db-disk-iops                        
  Rnd IO/s in for micro service VM (default "300")
 prov-db-disk-size                        
  Disk in g for micro service VM (default "20g")
 prov-db-tags                             
  Tags for configuration  (todo)
 prov-proxy-disk-size                    
   Disk in g for micro service VM (default "20g")
```

We will provide on the long run the following scope of extra database tags:

aria,audit,compress,innodb,logs,myrocks,network,noquerycache,optimizer,security,semisync,sharedpool,spider,threadpool,tokudb

## Install

OpenSVC collector is a commercial software that collect all the informations send from the OpenSVC open source agents.  

replication-manager have a secure client API that talk to the collector and use the collector for posting actions to the agents, to collect cluster nodes metrics and load replication-manager own set of playbook for provisioning.

We will need  to install the evaluation version of the collector. It can be install local to replication-manager or install on a remote hardware this is settable in the replication-manager config files via following parameters

```
prov-host = "127.0.0.1:443"
prov-admin-user = "root@localhost.localdomain:opensvc"
```    

### First step

Pre requirements
```  
apt-get install -y python
apt-get install -y net-tools
apt-get install -y psmisc
```

To install a collector first step is to install the OpenSVC agent that will also be used later on the other cluster nodes. This first agent will be in charge to manage the deployment of the collector as a docker service.

Follow the instructions to install the agent and the collector
https://docs.opensvc.com/collector.evaluation.html

When done a fist run of replication-manager is needed to configure the collector and load the playbook

```
./replication-manager monitor --opensvc
```

This run give similar output
```
2017/07/13 11:45:43 {
	"info": "change group replication-manager: privilege: False => F",
	"data": [
		{
			"privilege": false,
			"role": "replication-manager",
			"id": 317,
			"description": null
		}
	]
}

2017/07/13 11:45:43 INFO  https://192.168.1.101:443/init/rest/api/groups?props=role,id&filters[]=privilege T&filters[]=role !manager&limit=0
2017/07/13 11:45:43 INFO  https://192.168.1.101:443/init/rest/api/users/122/groups/23
2017/07/13 11:45:44 {
```

At startup it create a replication-manager user with password mariadb , similar named group and application group and affecting the correct roles and grants to it.

It load the playbook call compliance in OpenSVC from the replication share/opensvc directory.

Some compliance hardcoded rules are also serve by replication-manager to the agents. They are compile in a tar.gz file name current in share/opensvc including all the modules of the directory share/opensvc/compliance

Those rules can be recompile via the publish_modude.sh script

If you need to adapt the modules, each agent will have to collect the rules via this command
```
nodemgr updatecomp
```

### Next step

Install the agents on the nodes of your cluster a minimum set of packages are needed to make a good provisioning agent.
```  
apt-get install -y python
apt-get install -y net-tools
apt-get install -y psmisc
apt-get install -y zfsutils-linux
apt-get install -y system-config-lvm
apt-get install -y xfsprogs
apt-get install -y wget
```

Docker advices:

Usage of ubuntu server is preferred because better support for ZFS and docker in that distribution. This is not a requirement if you feel more comfortable with other distributions.

It's a loose of time to try some Docker deployments on OSX and may be Windows(not experimented myself) for  deployments, docker is not mature enough on those distributions. It looks it can work but you will quickly hit some network and performance degradations.   

You will need to instruct you cluster agents where to found your fresh collector and replication-manager modules

```
nodemgr set --param node.dbopensvc --value https://collector-host
nodemgr register --user=replication-manager@localhost.localdomain --password=mariadb
nodemgr set --param node.repocomp --value https:/replication-manager:10001/repocomp
```

You can verify that the agent is discovered by going the web interface of replicaton-manager and check the agents tab.

### Last step

Depends on the agent version and the type of security you would like to implement for provisioning.

You should login to your collector and instruct the type of required deployment pull or push for each agent, prior to 1.8 and also tell the ip of the agent for the collector to send information to agent.


The pull mode need some extra setting on the agent node explain here:
https://docs.opensvc.com/agent.architecture.html#the-inetd-entry-point

The push mode need some extra ssh setting on the agent node  

On Unix systems, if the root account has no rsa key, a 2048 bits rsa key is generated by the package post-install. A production node key must be trusted on all nodes of its cluster (PRD and DRP), whereas the keys of disaster recovery servers must not be trusted by any production nodes. This setup is used for rsync file transfers and remote command execution.


# Bootstrapping clusters

Micro services placement will follow a round robin mode against the agents listed for a cluster  

A bootstap, and unprovision command is printed in the web interface
