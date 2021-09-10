## replication-manager [![Build Status](https://travis-ci.org/signal18/replication-manager.svg?branch=2.1)](https://travis-ci.org/signal18/replication-manager)

![replication-manager](https://github.com/signal18/replication-manager/raw/2.0/dashboard/static/img/logo.png)

__replication-manager__ is an high availability solution to manage MariaDB 10.x and MySQL & Percona Server 5.7 GTID replication topologies.  

The main features are:
 * Replication monitoring
 * Topology detection
 * Slave to master promotion (switchover)
 * Master election on failure detection (failover)
 * Replication best practice enforcement
 * Target to up to zero loss in most failure scenarios
 * Multiple cluster management
 * Proxy integration (ProxySQL, MaxScale, HAProxy, Spider)

### [Documentation](https://docs.signal18.io)

### License

__replication-manager__ is released under the GPLv3 license. ([complete licence text](https://github.com/signal18/replication-manager/blob/master/LICENSE))

It includes third-party libraries released under their own licences. Please refer to the `vendor` directory for more information.

It also includes derivative work from the `go-carbon` library by Roman Lomonosov, released under the MIT licence and found under the `graphite` directory. The original library can be found here: https://github.com/lomik/go-carbon

## Copyright and Support

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL18 CLOUD SAS](https://signal18.io/products).
