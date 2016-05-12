mariadb-tools
=============

Tools for MariaDB

Usage: mariadb-[command] [options]

List of available commands:

**report**	Generates a summary of MariaDB server configuration and runtime

**status**	sysstat-like MariaDB server activity

**repmgr** 	GTID replication switchover and monitor utility

**top**	A simple mytop clone

**msm** Multi-source replication monitoring

## Binary releases

Grab the latest binary release of the tool you need at https://github.com/tanji/mariadb-tools/releases and copy it in your `/usr/local/bin` directory. That's all which needs to be done.

## Building from source

If you'd like to run the latest version of MariaDB Tools you have to compile those from source.
First of all, install the golang runtime on your distribution: `yum install golang` (CentOS) or `apt-get install golang-go` (debian, ubuntu)

Create a go source folder and set the go path environment variable to this folder:

```
mkdir ~/go
export GOPATH=~/go
cd ~/go
```

Let's say that you want to build mariadb-repmgr, just do the following and go will do everything for you:

```
go get github.com/tanji/mariadb-tools/mariadb-repmgr
go install github.com/tanji/mariadb-tools/mariadb-repmgr
```

You will find your newly compiled binary under the ~/go/bin/ directory.
