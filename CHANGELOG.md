# Changelog

All notable changes to replication-manager will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [3.1.18] - 2026-03-04

### Added
- Splitdump backup/restore pipeline integrated into cluster backup workflows (#1358)
- Configurable splitdump shard size with UI control and per-cluster defaults (#1366, #1367)
- Splitdump restore: GTID handling, missing table resilience, and binlog suppression (#1363, #1364)
- Splitdump preamble helpers and schema file identification (#1363)
- Logical reseed async flow with hardened restore workflows (#1370)
- pgzip tuning exposure and improved setting logs (#1370)
- Functions to retrieve absolute path of replication-manager executable and CLI script (#1369)
- Table checksum scheduling with schema cache requirement and bounded polling (#1371, #1372)
- Local backup deletion to reclaim disk space (#1373)
- Backup GUI: display columns for action cells and improved delete guards (#1373)

### Fixed
- Restic purge handler deadlock (#1374)
- Backup delete guards hardened to block deletion during active jobs (#1373)
- Orphaned backup metadata warnings suppressed per-file (#1372)
- Checksum race condition resolved; explicit HTTP 202 on deferral; reduced log noise (#1371, #1372)
- Splitdump line parser: statement ending handling, empty USE statement prevention (#1367)
- Splitdump shard naming stabilized (#1366)
- Splitdump stream size option validation (#1366)
- Splitdump CLI path test aligned with lookup fallbacks (#1370)
- Change log decoding and backup logging limited to avoid noise (#1370)
- Logical reseed workflows hardened for safer restores (#1370)
- macOS builds disabled due to syslog dependency (#1358)

## [3.1.17] - 2026-02-24

### Added
- Schema monitoring for table/column/index differences between master and slaves with shardproxy integration (#1305, #1306)
- Manual database variable override and preservation system with three-tier preserved variables and UI indicators
- Config drift detection with HasConfigDiff field and visual indicators navigating to Variables tab
- Restic FUSE mount operations with strict path resolution and auto-disable on unavailability (#1343)
- Restic reseed with metadata cache, safer API defaults, and UI updates (#1342)
- Restic SFTP backend support via backup-restic-local-repository configuration
- Restic backup-restic-host configuration and new configurable options (#1338)
- Restic keep-tag handling with strict validation and improved error reporting (#1338)
- Restic permission validation with unit tests (#1332, #1333)
- Configurable compression level and parallel blocks for backup and SST/restore operations (#1336)
- `splitdump` CLI command: pipe dump.sql to replication-manager-cli with safe filename sanitization (#1339, #1340, #1341)
- Docker rootless image variants with fixed UID/GID 10001:10001 for production deployments (#1334)
- App provision and unprovision grants with enhanced error handling and monitoring status updates (#1348, #1349)
- Service plan reload for clusters (#1349)
- Replication master retry count configuration and UI (#1282)
- `HasRunningDBJobs` method to defer restart container cookies when jobs are active
- Config fetch cookie checks on server init and on prov-db-start-fetch-config change (#1307)
- OpenSVC overwrite support for existing config and secret objects
- OpenSVC certificate loading failure handling
- ClearOldCookies method to clean up stale cookies during cluster initialization
- Pushover integration in shared logging mechanism
- Error 2002 handling in log parsing with level-based error log control (#1290, #1291)
- Provision check in ResticPurgeRepo to prevent operation on unprovisioned clusters
- Sample deployment configuration for environment variables
- GitHub Actions workflow for release binary builds
- One-liner installation script for embedded binary
- SQL error log level configuration method for cluster settings

### Fixed
- MyDumper version-aware flag handling: restore legacy flags for older versions, use --trx-tables for newer (#1351, #1356, #1357)
- Smart/curly quote replacement in VariablesMap loadFromSection and LoadFromTempConfigFile
- Restic mount directory uses working-dir/cluster/mount to prevent permission issues (#1343)
- Restic critical security and reliability issues in core (#1332)
- Restic worker processing when task queue is empty
- Restic nil pointer check before accessing paused state
- OpenSVC config/secret object creation using new KeysExists method (#1346)
- Proxy tracking in query rules monitoring (#1326)
- Backup validation and state warning logic (#1324)
- Schema string delimiter in columnDefQuery function
- Job version check skipped for non-OpenSVC orchestrators
- Job scheduler table check includes error details in warning state
- Job run: script existence check and error propagation in CheckJobsVersion
- Job run: job check cookie setting for outdated job files
- JobsCheckFinished extended to include sqlerrorlog and auditlog tasks
- JobsCreateTable column type check for MEDIUMTEXT
- User populated from Username (login) field, not Name (display name)
- MasterSlaveTimeDiff JSON tag casing
- StaticCheck linting issues: ST1013, S1002, QF1011, and context leaks (#1327)
- Arbitrator startup failure with missing config backup directory (#1309, #1313)
- Docker verbosity set to normal for images
- Docker maxscale repo skip
- Replication event time display in DBServerGrid

### Changed
- dbhelper package split from monolithic 4406-line file into organized modules (#1314)
- dbhelper SQL injection vulnerabilities eliminated via parameterized queries
- dbhelper database vendor abstraction layer introduced
- Preserved variables config streamlined with server exclusions and file copying refactor
- VariablesMap deployed values tracking with mutex protection
- MasterRetryCount renamed from ReplicationMasterRetryCount for consistency
- ChangeMaster options streamlined with helper functions
- Mattermost client upgraded to public model (#1308)
- Environment config handling and runtime defaults improved (#1316)
- OpenSVC API response logging changed from Info to Debug level
- Global clusters slice refactored to use guarded async thunks
- Ignored server handling improved across Variables and ClusterDBTabContent UI components
- Version extraction improved: extractVersionFromOutput handles multi-line output and moved to version package
- Restart cookie management refactored with new naming and state-based logic
- Bootstrap script cleanup refined to preserve custom configurations
- Go builders upgraded from 1.23 to 1.24 in Docker images

### Security
- Backup repository value encoded to base64 in BackupSettings to prevent unsafe values
- SQL injection prevention in dbhelper parameterized queries
- File path injection prevented in splitdump using sanitizefilename library
- Mattermost CVE-2025-12421 account takeover vulnerability fix (#1308)

## [3.1.16] - 2025-12-03

### Added
- Restic task queue management with cancel, pause, and resume operations (#1283)
- Active-passive topology support for failover and replication processes
- Audit log fetch level configuration and tailer support (#1273)
- API endpoints for Restic task management and queue modifications
- FetchTask invocation in backup operations for improved task tracking

### Fixed
- Improve backup server retrieval and add nil checks (#1281)
- Update optimizer configuration for MariaDB and MySQL (#1271)
- Change terminal session default to disabled for non-OpenSVC orchestrators
- Add 'terminal' grant to API user ACL configuration

### Changed
- Refactor ResticRepo to ResticManager for better naming consistency (#1279)
- Rename log archive references to log restic throughout codebase
- Add deprecated configuration key detection and logging standardization (#1275)
- Rename SendStats to FetchStats for improved metrics handling

## [3.1.15] - 2025-11-14

### Fixed
- MySQL 8.4 compatibility: mysqlbinlog option issue after rejoin (#1267, #1259)
- Update regex to correctly capture numeric version format in version parsing
- Update SSL mode checks to reflect MySQL 8.0 changes and remove deprecated checks
- Simplify SSL mode handling for client-dump and client-binlog tools

## [3.1.14] - 2025-11-14

### Added
- Post-backup script support for automated backup management (#1261)
- Web log retrieval endpoints with Redux integration
- SBOM (Software Bill of Materials) generation for GitHub
- Customizable post-backup scripts with configurable execution
- GetCompactJson method for improved cluster JSON responses

### Fixed
- Ensure backup server is set and handle missing server cases
- Handle nil disk usage in backup free space checks
- Disable free space check before backup processing
- Fix flooding when decrypt fails due to credentials change (#1255)
- Handle path resolve on storage update (#1250)
- Update variable indexing and mask S3 secret keys in JSON responses
- Adjust MySQL version compatibility in backupBinlog function

### Changed
- Comment out failover check for provisioning in OnPremiseConnect (#1264)
- Refactor MySQL version handling functions for consistency
- Simplify Docker command handling by removing variable parsing
- Enhanced password rotation logic to handle immutable credentials
- Jobs script check and upgrade improvements (#1248)

### Security
- Add immutability checks for ProxySQL and MDBS Proxy password rotation
- Enhanced credential management with immutability validation

## [3.1.13] - 2025-10-07

### Added
- OpenSVC workload component integration (#1244)
- OpenSVC stats retrieval in Home, Top, and cluster views
- ACL check for OpenSVC stats API endpoint
- Conditional authentication for GetDaemonNodeStats based on UseAPI flag

### Fixed
- Change warning to error level for duplicate ServerID check
- Update Ping function to clarify read-only and staging server conditions
- Add DB credentials check to prevent invalid configuration
- Add OpenSVC thresholds checks and enhance error handling
- Fix CPU load color calculation in OpenSVCNodeCard

### Changed
- Refactor staging server management and configuration retrieval
- Enhance Ping function to handle staging servers in read-only and rejoin logic
- Optimize OpenSVC stats reload frequency for improved performance

## [3.1.12] - 2025-10-02

### Added
- Log optimize level configuration and UI support
- API endpoints to check task necessity and cluster log access (#1234)
- API endpoint to check if a task needs to be performed on a server
- API endpoint to check if a cluster can fetch logs

### Fixed
- Fix admin check in global tabs to use optional chaining (#1233)
- Update log fetch response to return boolean values
- Improve error messages in GetSlowLogTable for better debugging

### Changed
- Refactor job entry handling with new API endpoints for server tasks
- Refactor CheckTaskNeeded to use constants for task names
- Remove redundant 'can-run-jobs' endpoint
- Update useEffect dependency to include loggedUser for admin tab visibility

## [3.1.11] - 2025-10-01

### Added
- Restic password management functionality with API endpoints (#1230)
- Support for backing up and restoring Restic config files
- Benchmark regression tests (#1213)

### Fixed
- Fix incorrect variable name for relay log file in SlaveStatus import (#1229)
- Fix typo in error log filename
- Fix warning message in ResticFetchRepo for repo initialization error
- Fix table creation queries to ensure they only create if not exists

### Changed
- Switch to Viper-based solution for environment-based settings (#1213)
- Implement environment variable check for username and password in CLI
- Refactor login error handling to improve credential validation
- Fix camel case number of proxies in JSON cluster response

## [3.1.10] - 2025-09-25

### Added
- Cookie management for wait error log and slow query log (#1222)
- Fetch error log and slow query log level settings to configuration and API
- Logging levels in dbjobs_new.sh script
- API endpoints for app service management: start, stop, and restart actions

### Fixed
- Fix nil pointer dereference in Refresh function (#1219)
- Fix log task type in handlerMuxServersWriteLog
- Handle API call errors gracefully in needRefreshConfig function
- Add configuration handling for rolling restart in StartDatabaseWaitRejoin
- Fix hostname not found due to missing variable
- Fix redirect URL in GetMeetToken function

### Changed
- Refactor HAProxy logging to use consistent logging module (#1222)
- Refactor HAProxy logging levels from Info to Debug to reduce noise
- Add Svname field to Backend struct and update Refresh method
- Refactor SetMaster and SetMasterFQDN functions to use pool parameter
- Enhanced logging for FetchErrorlog and FetchSlowquery modules
- Refactor measurement configuration and disable for OSC

## [3.1.9] - 2025-09-11

### Changed
- Refactor error logging in arbitrator handlers to use LogModulePrintf method

## [3.1.8] - 2025-09-11

### Added
- Sponsor user creation in Bootstrap process when sponsor email provided (#1200)
- User data assignment in authSlice login action
- Enhanced Slack integration and alert logging levels

### Fixed
- Remove redundant dispatch of whoami in PageContainer
- Remove redundant user object assignment in authSlice login action
- Remove redundant retrieval of logged user from localStorage

### Changed
- Refactor user credential management: consolidate creation and grant handling
- Enhance cookie management for database users
- Add execution timeout configuration for user operations

## [3.1.7] - 2025-09-09

### Added
- /api/whoami endpoint to retrieve user information from JWT token (#1198)
- Application provisioning framework with template support
- Drop app functionality in cluster management API and UI
- S3 mount support and storage path configuration
- Application template repository management
- Provisioning app HA topology setting with credit adjustments
- Application credit management features
- Reset from template functionality for apps
- Save as template functionality for apps
- Git Clone functionality with volume and subpath support
- Docker command support in app configuration and UI
- Script for managing database jobs with encryption and API integration

### Fixed
- Fix API route for app drop action to use plural 'clusters'
- Fix app monitor removal handler to use correct appId
- Fix cluster list handling and improve error management
- Multiple UI/form rendering fixes across app components

### Changed
- Refactor authentication flow with Redux state management
- Refactor user authentication to use Redux instead of localStorage
- Enhanced error handling and logging throughout application
- Add base64 encoding for alert and backup script settings

## [3.1.6] - 2025-06-24

### Security
- Fix masking of script credentials

### Added
- Enhanced cluster subscription logic using JWT instead of username

### Fixed
- Specify version for gopls installation in Dockerfile
- Add file existence check before sed operation in scripts

## [3.1.5] - 2025-06-12

### Fixed
- Remove unused log import and comment out logging in ParseLine function
- Ensure proper timestamp extraction in ErrorLogWatcher

### Changed
- Extract token rotation logic into separate function for clarity

## [3.1.4] - 2025-06-04

### Added
- Epoch field to YAML configuration files (#1149)
- RunTaskV2 to OpenSVC API

### Fixed
- Remove 'epoch' key from YAML configuration files for packaging compatibility
- Correct 'dependencies' key to 'depends' in YAML configuration files (#1149)
- Add dependencies section for replication-manager-client in OSC and provisioning YAML
- Increase context timeout for heartbeat check to reduce false positives
- Fix log level for file by reloading hooks after log file level change

### Changed
- Refactor heartbeat check for slaves using goroutines and context timeout
- Improve auto failover false positive state detection

## [3.1.3] - 2025-05-30

### Added
- 'cluster-analyze' grant and ACL checks integration
- DataTable component with unique keys and grouping/expanding features
- Persistent analysis option and related cluster settings
- analyzeUsePersistent field to Swagger documentation

### Fixed
- Process monitoring regression using IS on MySQL <8 (#1143, #1141)
- Execute bash script when server set to maintenance mode (#1139)
- Update isDownList and isFailableList for non-200 response status
- Update searchData function to handle undefined data
- Update fetching conditions in clusterSlice to use correct state structure
- Increase timeout in CreateMeetUserClient for better reliability

### Changed
- Improved filtering and sorting in cluster sharing

## [3.1.2] - 2025-05-20

### Fixed
- Update mailer JSON field names to camelCase for consistency

## [3.1.1] - 2025-05-20

### Fixed
- Mailer TLS handling improvements

---

## [3.0.32] - 2025-12-18

### Fixed
- Improve error handling in CheckBackupFreeSpace function for missing backup server and disk usage
- Add table creation for replication_manager_schema in MoveLogsToDailyTable

## [3.0.31] - 2025-10-07

### Changed
- Refactor staging server management with improved configuration retrieval
- Enhance Ping function to handle staging servers in read-only and rejoin logic
- Change warning level to error for duplicate ServerID check in CheckSameServerID

## [3.0.30] - 2025-07-05

### Security
- Add base64 encoding for alert and backup script settings

### Fixed
- TLS handling in mailer (#1064, #1061)
- Add error handling for file opening in whitelist regexp operations

## [3.0.29] - 2025-04-28

### Added
- Email notification enhancements with expanded recipient handling (#1127)
- Mattermost/Meet integration for alerts
- Use of global mailer config for all clusters

### Changed
- Refactor cloud18 fields for better alert configuration flexibility
- Improved error handling in EmailMessage function

## [3.0.28] - 2025-04-24

### Added
- Comprehensive GTID handling enhancements for MariaDB replication (#1124, #1123)
- Reseed from parent cluster functionality with GTID merging logic
- Backup on successful reseed implementation
- Support for replication multisource head clusters

### Changed
- Improved GTID parsing and metadata management for mydumper jobs
- Enhanced logging for slave startup with detailed GTID information

### Fixed
- Fix GTID position setting for MariaDB

## [3.0.27] - 2025-04-18

### Changed
- Simplify conditional rendering of proxy start/stop options
- Enhanced logging throughout proxy management

## [3.0.26] - 2025-04-18

### Added
- Process kill/query thread functionality (#1113)
- Web terminal functionality (initial implementation)
- Support for SSL client parameter handling for MySQL versions

### Fixed
- GTID binlog state reset on reseed for strict GTID compliance (#1112, #1111)

### Changed
- Increase gzip buffer size for improved performance (#1115)
- Enhanced reset slave operations with extended timeouts
- Refactor mysqldump user backup handling

## [3.0.25] - 2025-04-11

### Added
- Disk space validation before logical/physical backups (#1097, fixes #1095)
- DiskStatManager for real-time disk statistics
- Backup size estimation with free space checks
- Add backup-mysqlclient-options configuration
- Docker image handling for provisioning

### Changed
- Improved error logging throughout backup operations
- Reduce backup growth percentage threshold for reliability

## [3.0.24] - 2025-04-03

### Added
- Web terminal access to servers and proxies (#1089)
- Terminal session management via WebSocket
- JWT parsing for WebSocket authentication
- Terminal access control via ACL grants
- SSL mode configuration for MySQL client connections

### Fixed
- Fix myloader table index issue causing unusable nodes (#1088)

## [3.0.23] - 2025-03-31

### Changed
- Improve failover determination logic (false positive fixes)
- Disable errant transaction checking when master state is failed
- Better reporting of auto-failover events
- Add status checks for slave error, late, and standalone states

## [3.0.22] - 2025-03-27

### Added
- Git user login support for authentication (#1077)
- Mattermost/Meet integration for chat and support (#1074)
- Cloud18 alert configuration and Slack integration

### Changed
- Staging refresh script refinements
- Improved replica selection for standalone configuration
- Add super-read-only in workload freezing (#1076)
- Lock users during backup workload freeze

## [3.0.21] - 2025-03-22

### Added
- Health check endpoints for cluster status monitoring (#1070)
- Peer cluster health status retrieval with interval-based updates
- Cloud18 alert configuration for Slack/Mattermost
- API documentation with Swagger UI
- Support log level configuration

### Changed
- Email recipient filtering improvements

## [3.0.20] - 2025-03-21

### Added
- Mattermost alert posting support with advanced messaging (#1068)
- Cloud18 SSH integration for support chat
- Git token refresh mechanism
- Mailer pool management with ReinitPool method

### Changed
- TLS handling and secure mode support in mailer (#1067, #1066)
- Rate limiting for health status updates

## [3.0.19] - 2025-03-07

### Added
- Complete web terminal implementation with SSH/MySQL sessions (#1055, #1054, #1053)
- Client TLS certificate support with extended validity (100 years)
- Docker run arguments for database and proxy configurations
- Password handling for mysql/mytop commands
- Dark mode support for terminal UI

### Fixed
- HAProxy backend recovery fixes

## [3.0.18] - 2025-02-28

### Added
- Full terminal access to databases and proxies
- Session management with credentials based on user role
- Terminal UI with improved layout and navigation
- File download from terminal sessions
- Web terminal routing and endpoint management

## [3.0.16] - 2025-02-06

### Added
- MySQL 8.4+ version support with Executed_Gtid_Set field (#1038, #1036)
- Measurement parsing and unit handling for configuration values
- Partner info addition to cluster management
- Restic backup system enhancements

## [3.0.15] - 2025-01-21

### Added
- Staging refresh script improvements (#1035, #1034, #1033, #1032, #1031)
- Restic repository initialization and validation
- Automatic repository initialization if missing
- Restic purge strategy configuration
- Restic task queue management with unlock functionality
- Backup retention options for Restic

## [3.0.14] - 2025-01-17

### Added
- HTTP error suppression with custom log adapters (#1030, #1029, #1025)
- Tmpfs volume configuration for database containers
- Restic job queue processing
- Archive-level log settings

### Changed
- Improved backup stats retrieval

## [3.0.13] - 2025-01-14

### Added
- External role subscription management (extdbops, extsysops) (#1024, #1022, #1020)
- Cloud18 configuration enhancements
- Discovered state management when topology known
- Autoseed and autorejoin action toggles

## [3.0.12] - 2025-01-09

### Added
- Cluster rename functionality with unprovision validation (#1018, #1016, #1015, #1014)
- Staging configuration options and refresh endpoints
- ACL switch settings with on/off states
- Git user configuration for Cloud18

### Changed
- Provisioned cluster rename prevention

## [3.0.11] - 2025-01-07

### Added
- Cloud18 DBA and sponsor user credentials support (#1010)
- User creation for (ext)sysops and (ext)dbops roles
- Role-based credential management
- Sponsor credential revocation on subscription end
- Sales subscription scripts

## [3.0.10] - 2025-01-06

### Added
- Swagger documentation enabled by default (#977, #971)
- MyDumper regex configuration for schema exclusion

### Changed
- Improved backup script functionality
- Better server state management

## [3.0.9] - 2025-01-03

### Fixed
- Fix credential management in cluster operations
- Improve email handling for Cloud18 extops

## [3.0.8] - 2025-01-03

### Added
- Galera support enhancements (#1000, #998)
- Switchover/failover behavior for extra slaves
- Virtual master handling
- WSREP UUID comparison improvements

## [3.0.7] - 2024-12-21

### Added
- Multi-master concurrent write support (#994, #993, #992)
- ProxySQL read-on-writer configuration
- Multi-master failover handling
- Errant transaction detection improvements

## [3.0.6] - 2024-12-20

### Added
- Cloud18 peer-to-peer cluster support (#964)
- GitLab-based cluster registration
- Cloud18 DBA/sponsor credentials
- Service plan application
- HAProxy IPv6 support
- Cloud18 cost tracking fields

## [3.0.5-rc] - 2024-12-13

### Added
- Role-based user GUI and user management (#964, #948, #942)
- Full Restic AWS S3 integration for backups
- User credential management with role separation
- Angular UI improvements for user management and backup options

## [3.0.4-rc] - 2024-11-15

### Fixed
- ProxySQL multimaster setup samples
- Topology state handling improvements
- Read-only state management on master

## [3.0.3-rc] - 2024-11-15

### Added
- User GUI implementation in React
- OS user scoping improvements
- Certificate validity period extended (100 years)
- Angular backup options integration

## [3.0.2-rc] - 2024-10-25

### Added
- Restore dump functionality with crash trace logging (#956, #955, #954, #953, #951, #949)
- OpenSVC proxy service auto-start on server restart
- ProxySQL multi-master group replication support
- Multi-master read-on-writer toggle functionality
- OS user scoping with proper file permissions

## [3.0.1-rc] - 2024-10-23

### Added
- React dashboard UI enhancements
- Topology target setting with multi-master support
- Backup configuration UI improvements
- Read-only prevention on master in multi-master

## [3.0.0-rc] - 2024-10-22

### Added
- Core replication monitoring and orchestration framework
- Failover/switchover logic with state machine
- Proxy integration (MaxScale, ProxySQL, HAProxy)
- gRPC API implementation (v3 protocol)
- REST API with HTTP server
- Configuration management with TOML support
- Backup orchestration (logical/physical)
- Alert system with multiple notification channels
- Multi-cluster support with peer-to-peer capabilities
- Staging topology support
- Cloud18 integration for SaaS deployments
- Replace obsolete fpm by nfpm packager (#924)

---

## Release Summary

### 3.1.x Series
The 3.1.x series represents significant evolution of replication-manager with major improvements across:

- **Backup & Restore**: Restic task queue, FUSE mount, SFTP backend, reseed with metadata cache, configurable compression, post-backup scripts, disk reclaim, and splitdump streaming pipeline with configurable shard sizing and GTID-aware restore
- **Schema & Data Integrity**: Master-replica table/column/index comparison, table checksum scheduling with schema cache, and shardproxy integration
- **Configuration Management**: Manual variable override and preservation, config drift detection with UI indicators, and three-tier preserved variables
- **Application Management**: Provisioning framework with template support, S3 integration, credit management, and provision/unprovision grants
- **Database Compatibility**: MySQL 8.4 support, dbhelper refactor with vendor abstraction, parameterized queries, and improved version handling
- **Monitoring & Logging**: Modular logging system, OpenSVC integration, audit log support, Pushover integration
- **Security**: Credential masking, JWT authentication, password rotation with immutability checks, and SQL injection prevention
- **Topology**: Active-passive topology support, staging server improvements, replication master retry count configuration
- **Proxy Integration**: HAProxy logging improvements, enhanced backend management
- **Docker**: Rootless image variants with fixed UID/GID for production deployments

### 3.0.x Series
The 3.0.x series established the foundation for modern replication-manager with enterprise-grade features:

- **Cloud18 SaaS Platform**: Complete peer-to-peer cluster support, GitLab-based registration, subscription management, and external operator roles
- **Web Terminal**: Full terminal access to databases and proxies with WebSocket-based sessions, JWT authentication, and dark mode support
- **Staging Topology**: Comprehensive staging server support with refresh scripts, GTID handling, and configuration management
- **Restic Backup System**: Full S3 integration, repository initialization, task queue management, and retention policies
- **Reseeding & GTID**: Advanced GTID merging logic, parent cluster reseeding, and MariaDB multisource replication support
- **Communication Integration**: Mattermost/Meet integration for alerts, chat support, and team collaboration
- **Multi-Master Support**: ProxySQL multi-master configuration, Galera enhancements, and concurrent write handling
- **MySQL 8 & MariaDB**: Comprehensive version support including MySQL 8.4+, improved SSL/TLS handling, and version-specific features
- **User Management**: Role-based access control with GUI, external operator support, and credential management
- **Disk Monitoring**: Real-time disk statistics, backup size estimation, and free space validation

[3.1.18]: https://github.com/signal18/replication-manager/releases/tag/v3.1.18
[3.1.17]: https://github.com/signal18/replication-manager/releases/tag/v3.1.17
[3.1.16]: https://github.com/signal18/replication-manager/releases/tag/v3.1.16
[3.1.15]: https://github.com/signal18/replication-manager/releases/tag/v3.1.15
[3.1.14]: https://github.com/signal18/replication-manager/releases/tag/v3.1.14
[3.1.13]: https://github.com/signal18/replication-manager/releases/tag/v3.1.13
[3.1.12]: https://github.com/signal18/replication-manager/releases/tag/v3.1.12
[3.1.11]: https://github.com/signal18/replication-manager/releases/tag/v3.1.11
[3.1.10]: https://github.com/signal18/replication-manager/releases/tag/v3.1.10
[3.1.9]: https://github.com/signal18/replication-manager/releases/tag/v3.1.9
[3.1.8]: https://github.com/signal18/replication-manager/releases/tag/v3.1.8
[3.1.7]: https://github.com/signal18/replication-manager/releases/tag/v3.1.7
[3.1.6]: https://github.com/signal18/replication-manager/releases/tag/v3.1.6
[3.1.5]: https://github.com/signal18/replication-manager/releases/tag/v3.1.5
[3.1.4]: https://github.com/signal18/replication-manager/releases/tag/v3.1.4
[3.1.3]: https://github.com/signal18/replication-manager/releases/tag/v3.1.3
[3.1.2]: https://github.com/signal18/replication-manager/releases/tag/v3.1.2
[3.1.1]: https://github.com/signal18/replication-manager/releases/tag/v3.1.1
[3.0.32]: https://github.com/signal18/replication-manager/releases/tag/v3.0.32
[3.0.31]: https://github.com/signal18/replication-manager/releases/tag/v3.0.31
[3.0.30]: https://github.com/signal18/replication-manager/releases/tag/v3.0.30
[3.0.29]: https://github.com/signal18/replication-manager/releases/tag/v3.0.29
[3.0.28]: https://github.com/signal18/replication-manager/releases/tag/v3.0.28
[3.0.27]: https://github.com/signal18/replication-manager/releases/tag/v3.0.27
[3.0.26]: https://github.com/signal18/replication-manager/releases/tag/v3.0.26
[3.0.25]: https://github.com/signal18/replication-manager/releases/tag/v3.0.25
[3.0.24]: https://github.com/signal18/replication-manager/releases/tag/v3.0.24
[3.0.23]: https://github.com/signal18/replication-manager/releases/tag/v3.0.23
[3.0.22]: https://github.com/signal18/replication-manager/releases/tag/v3.0.22
[3.0.21]: https://github.com/signal18/replication-manager/releases/tag/v3.0.21
[3.0.20]: https://github.com/signal18/replication-manager/releases/tag/v3.0.20
[3.0.19]: https://github.com/signal18/replication-manager/releases/tag/v3.0.19
[3.0.18]: https://github.com/signal18/replication-manager/releases/tag/v3.0.18
[3.0.16]: https://github.com/signal18/replication-manager/releases/tag/v3.0.16
[3.0.15]: https://github.com/signal18/replication-manager/releases/tag/v3.0.15
[3.0.14]: https://github.com/signal18/replication-manager/releases/tag/v3.0.14
[3.0.13]: https://github.com/signal18/replication-manager/releases/tag/v3.0.13
[3.0.12]: https://github.com/signal18/replication-manager/releases/tag/v3.0.12
[3.0.11]: https://github.com/signal18/replication-manager/releases/tag/v3.0.11
[3.0.10]: https://github.com/signal18/replication-manager/releases/tag/v3.0.10
[3.0.9]: https://github.com/signal18/replication-manager/releases/tag/v3.0.9
[3.0.8]: https://github.com/signal18/replication-manager/releases/tag/v3.0.8
[3.0.7]: https://github.com/signal18/replication-manager/releases/tag/v3.0.7
[3.0.6]: https://github.com/signal18/replication-manager/releases/tag/v3.0.6
[3.0.5-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.5-rc
[3.0.4-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.4-rc
[3.0.3-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.3-rc
[3.0.2-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.2-rc
[3.0.1-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.1-rc
[3.0.0-rc]: https://github.com/signal18/replication-manager/releases/tag/v3.0.0-rc
