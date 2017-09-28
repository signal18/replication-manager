## replication-manager [![Build Status](https://travis-ci.org/signal18/replication-manager.svg?branch=develop)](https://travis-ci.org/signal18/replication-manager) [![Stories in Ready](https://badge.waffle.io/signal18/replication-manager.svg?label=ready&title=Ready)](http://waffle.io/signal18/replication-manager) [![Gitter](https://img.shields.io/gitter/room/nwjs/nw.js.svg)](https://gitter.im/replication-manager)

![replication-manager](https://github.com/signal18/replication-manager/raw/develop/dashboard/static/logo.png)

__replication-manager__ is an high availability solution to manage MariaDB 10.x and MySQL & Percona Server 5.7 GTID replication topologies.  

Product goals are topology detection and topology monitoring, enable on-demand slave to master promotion _(also known as switchover)_, or electing a new master on failure detection _(also known as failover)_. It enforces best practices to get at a minimum up to zero loss in most failure cases. Multiple clusters management is the foundation to define shard groups and replication-manager can be used to deploy some MariaDB sharding solutions.

### [Documentation](https://docs.signal18.io)

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL 18 SARL](https://signal18.io/products). 
