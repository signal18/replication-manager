# Implementation Documentation

This directory contains detailed implementation documentation for various features and components of replication-manager.

## Build & Release

- **CI_RELEASE_PIPELINE.md** - GitHub Actions CI/release pipeline map (Jenkins is retired): package/repo publication, docker images, release assets, tag naming rules
- **BUILD_PLUGIN_PUBLISHING.md** - Log-plugin publishing ownership (single publisher, `PLUGIN_PUSH` gate)

## Directory Structure

### `/cluster/`
Cluster monitoring, backup, and resilience.

- **BACKUP_DEAD_VOLUME_STALL.md** - Why a lost backup volume must not stall the monitor: the write-stall watchdog (`backup-write-stall-timeout`), the monitoring-hot-path sleep fix, and the controllable-mount reproduction

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
