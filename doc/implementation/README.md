# Implementation Documentation

This directory contains detailed implementation documentation for various features and components of replication-manager.

## Cloud18 (community)

- **cloud18/REGISTRATION.md** - Community onboarding A→Z: GitLab SSO login, first-claimant domain ownership, the config vs `-pull` repos, empty-remote config-repo bootstrap, GWARN002 troubleshooting
- **peer/MARKETPLACE.md** - What registration unlocks: `peer.json` community feed, for-sale listings, delegated cross-repman access and health
- **peer/PEER_JSON_AND_CLIENT.md** - `peer.json` sizes read with the config's unit parser, entry-by-entry decoding (BO normalization too), and the peer client's leading double slash under `SkipClean` (#1948, #1953)

## Build & Release

- **CI_RELEASE_PIPELINE.md** - GitHub Actions CI/release pipeline map (Jenkins is retired): package/repo publication, docker images, release assets, tag naming rules
- **BUILD_PLUGIN_PUBLISHING.md** - Log-plugin publishing ownership (single publisher, `PLUGIN_PUSH` gate)

## Directory Structure

### `/cluster/`
Cluster monitoring, backup, and resilience.

- **BACKUP_DEAD_VOLUME_STALL.md** - Why a lost backup volume must not stall the monitor: the write-stall watchdog (`backup-write-stall-timeout`), the monitoring-hot-path sleep fix, and the controllable-mount reproduction
- **HEARTBEAT_AND_ARBITRATION.md** - Peer heartbeat and arbitration; peer transport (http-port, scheme fallback) and failure states GWARN018-020 (#1940)
- **CONFIGURATOR_BUFFER_ROUNDING.md** - Engine buffer sizing: power of two for every buffer, 128 MB chunks for the InnoDB buffer pool, page cache left for the redo log (#1950, #1951)

### `/restart-cookie/`
Documentation related to the restart cookie mechanism and database restart functionality.

- **RESTART_COOKIE_COMPLETE.md** - Complete implementation guide for the restart cookie feature
- **RESTART_COOKIE_CLEANUP_COMPLETE.md** - Documentation for automatic cleanup of stale restart cookies at startup

### `/testing/`
Test coverage, test suites, and testing documentation.

- **TEST_COVERAGE_DOCUMENTATION.md** - Comprehensive test coverage documentation
- **TEST_COVERAGE_SUMMARY.md** - Summary of test coverage metrics
- **TEST_README.md** - Testing guidelines and instructions
- **TEST_RESULTS_FINAL.md** - Final test execution results

### `/config/`
Configuration management and refactoring documentation.

- **PHASE1_SUMMARY.md** - Phase 1 implementation summary
- **QUICKSTART.md** - Quick start guide for configuration
- **REFACTORING.md** - Refactoring documentation and decisions

### `/ui-components/`
Frontend UI component documentation.

- **ServerMenu.README.md** - ServerMenu component documentation
- **ServerMenu.REVIEW.md** - ServerMenu component review notes
- **ServerMenu.SUMMARY.md** - ServerMenu component summary

### `/server/`
Server API and cross-cluster aggregation documentation.

- **GLOBAL_JOBS_DASHBOARD.md** - Global jobs aggregate endpoint, ACL behavior, and dashboard wiring
- **STANDBY_CLUSTER_IMPORT.md** - A standby imports the clusters created on the active from the shared config repository, throttled and single flight (#1946)

### `/utils/dbhelper/`
Database helper utilities documentation.

- **MIGRATION_STATUS.md** - Migration status tracking
- **SECURITY_AUDIT.md** - Security audit findings
- **VENDOR_USAGE.md** - Vendor library usage documentation

## Other Documentation Locations

- **Main README**: `/README.md`
- **API Documentation**: `/doc/api_latest.md`
- **Contributing Guidelines**: `/CONTRIBUTING.md`
- **Changelog**: `/CHANGELOG.md`

## Notes

- Each subdirectory contains documentation specific to that feature or component
- Implementation docs should be updated as features evolve
- Test documentation should be updated with each test suite change
