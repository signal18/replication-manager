## Replication Manager for MariaDB, MySQL and PostgreSQL

Replication Manager is an open-source database monitoring, backup, and high-availability tool. It provides automated failover, scheduled maintenance, and full observability for MariaDB, MySQL, and Percona Server replication topologies, and, since 3.1.43, for PostgreSQL clusters.

### Key features

**MariaDB & MySQL Monitoring**
 * Real-time replication monitoring with support for GTID, multi-source, and delayed replication
 * Topology detection across async, semi-sync, multi-master, mesh, wsrep, group replication, and relay topologies
 * Performance schema digest analysis and workload tagging
 * Processlist and long-running transaction monitoring
 * NOC slideshow — full-screen auto-rotating display of all monitored clusters
 * Multi-cluster management from a single instance

**MariaDB & MySQL Backup and Recovery**
 * Scheduled logical and physical backups via mysqldump, mydumper, mariabackup, and xtrabackup
 * Snapshot backups and point-in-time recovery via Restic
 * S3-compatible object storage backend for backup repositories
 * Backup retention policies with automatic purge

**MariaDB & MySQL Maintenance**
 * Scheduled table optimization and analysis
 * Automated log archiving and table defragmentation
 * Rolling restart and reprovisioning
 * Schema monitoring and checksum verification
 * SLA tracking

**MariaDB & MySQL Failover and Switchover**
 * Leader election and automatic primary election on failure detection (failover)
 * Controlled replica-to-primary promotion (switchover)
 * Replication best-practice enforcement
 * Near-zero data loss targeting across most failure scenarios
 * Automatic database rejoining and reseeding

**PostgreSQL**
 * Clusters monitored like MariaDB and MySQL, with their own switchover, failover, and rejoin path
 * Three topologies: active-passive, WAL streaming (primary and physical standbys), and logical replication (publisher and subscribers, DDL through pglogical)
 * Switchover and failover with the routes moved on HAProxy and ProxySQL 3; rejoin of the former primary by `pg_rewind`
 * Logical and physical backups via `pg_dumpall` and `pg_basebackup`, restore and reseed from them, WAL archive shipped to replication-manager
 * Processlist, variables, table sizes, workload, WAL, and vacuum graphs from `pg_stat_*`; scheduled `vacuumdb --analyze`
 * See [PostgreSQL Topologies](https://docs.signal18.io/architecture/topologies/postgresql) for what is available compared with MariaDB

**Proxy Integration**
 * Automated backend management for ProxySQL, MaxScale, HAProxy, and Spider
 * ProxySQL 3 PostgreSQL backends, with read/write split

**Observability & Alerting**
 * Metrics history exposed via the Carbon/Graphite API
 * Alerting via email, Pushover, Slack, Teams, and Mattermost
 * Extensible log plugin system for security, compliance, and workload analysis
 * Per-module log level configuration

**Operations & Security**
 * Built-in database and proxy configurator with tag-based config tracking
 * Credential rotation for replication and monitoring accounts, with HashiCorp Vault integration
 * Encrypted secrets in configuration files, with support for multi-layer config inheritance
 * OAuth2 SSO compatible with GitLab, GitHub, and any OIDC provider
 * REST and gRPC APIs with role-based access control
 * Integrated WebTTY terminal sessions

**Provisioning**
 * Service deployment on OpenSVC and Kubernetes, including init container support


### [Documentation](https://docs.signal18.io)

### License

__replication-manager__ is released under the GPLv3 license. ([complete license text](https://github.com/signal18/replication-manager/blob/master/LICENSE))

It includes third-party libraries released under their own licenses. Please refer to the `vendor` directory for more information.

It also includes derivative work from the `go-carbon` library by Roman Lomonosov, released under the MIT licence and found under the `graphite` directory. The original library can be found here: https://github.com/lomik/go-carbon

## Copyright and support

Replication Manager for MariaDB, MySQL and PostgreSQL is developed and supported by [SIGNAL18 CLOUD SAS](https://signal18.io/products).
