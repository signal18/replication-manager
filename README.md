## Replication Manager

Replication Manager is a high-availability orchestrator for MariaDB, MySQL, and Percona Server replication topologies.

The main features are:

**Replication & Topology**
 * Replication monitoring with support for GTID, multi-source, and delayed replication
 * Topology detection and leader election across async, semi-sync, multi-master, mesh, wsrep, group replication, and relay topologies
 * Controlled replica-to-primary promotion (switchover)
 * Automatic primary election on failure detection (failover)
 * Replication best-practice enforcement
 * Near-zero data loss targeting across most failure scenarios
 * Automatic database rejoining and reseeding

**Cluster Management**
 * Multi-cluster management from a single instance
 * SLA tracking
 * Traffic capture on high load
 * Staging with multi-source clusters
 * Scriptable states and events
 * Remote script execution via SSH

**Proxy Integration**
 * Automated backend management for ProxySQL, MaxScale, HAProxy, and Spider

**Backup & Recovery**
 * Logical and physical backups via mysqldump, mydumper, mariabackup, and xtrabackup
 * Snapshot backups and point-in-time recovery via Restic
 * S3-compatible object storage backend for backup repositories
 * Automated log archiving and table defragmentation

**Observability & Alerting**
 * Metrics history exposed via the Carbon/Graphite API
 * Alerting via email, Pushover, Slack, Teams, and Mattermost
 * Per-module log level configuration

**Operations & Security**
 * Built-in database and proxy configurator
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

Replication Manager for MySQL and MariaDB is developed and supported by [SIGNAL18 CLOUD SAS](https://signal18.io/products).
