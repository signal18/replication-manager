## replication-manager

![replication-manager](https://github.com/signal18/replication-manager/raw/2.0/dashboard/static/img/logo.png)

Replication Manager is a high-availability orchestrator for MariaDB, MySQL, and Percona Server replication topologies.

The main features are:
 * Replication monitoring (GTID, multi-source, delayed)
 * Topology detection (leader election for async, semi-sync, multi-master, mesh, wsrep, group-repl, relay)
 * Slave to master promotion (switchover)
 * Master election on failure detection (failover)
 * Replication best-practice enforcement
 * Targeting up to zero loss in most failure scenarios
 * Multi-cluster management
 * Proxy integration (ProxySQL, MaxScale, HAProxy, Spider)
 * Maintenance automation (logical & physical backups, defrag, backup snapshots, log archiving)
 * Metrics history in Carbon/Graphite API
 * Alerting via email, Pushover, Slack, Teams, Mattermost
 * Database rejoining and reseeding policy
 * Scriptable state and events
 * Remote scripting via SSH
 * Database and proxy configurator
 * OpenSVC and Kubernetes service deployment including init containers
 * Encrypted config file secrets, multi-layer configs
 * GitLab SSO
 * API with ACL
 * Capture on high load
 * SLA tracking
 * Replication and monitoring credential rotation or Vault integration
 * Staging multi-source clusters
 * WebTTY
 * Restic backup snapshots and PITR
 * Modular logging levels


### [Documentation](https://docs.signal18.io)

### License

__replication-manager__ is released under the GPLv3 license. ([complete license text](https://github.com/signal18/replication-manager/blob/master/LICENSE))

It includes third-party libraries released under their own licenses. Please refer to the `vendor` directory for more information.

It also includes derivative work from the `go-carbon` library by Roman Lomonosov, released under the MIT licence and found under the `graphite` directory. The original library can be found here: https://github.com/lomik/go-carbon

## Copyright and support

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL18 CLOUD SAS](https://signal18.io/products).
