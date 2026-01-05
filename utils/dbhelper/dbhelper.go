// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

/*
Package dbhelper provides database helper functions for MariaDB, MySQL, PostgreSQL, and Percona Server.

This package has been refactored into well-organized, security-hardened modules:

# Core Modules

  - types.go: Data structures (SlaveStatus, MasterStatus, Table, Grant, etc.)
  - connection.go: Database connection helpers and address resolution
  - status.go: Server status queries, variables, and monitoring
  - replication.go: Replication control (GTID, slave/master management)
  - binlog.go: Binary log operations and configuration
  - transaction.go: Transaction control, locking, and InnoDB settings
  - performance.go: Performance Schema queries and analysis
  - mysql.go: MySQL-specific utilities (errant transactions)
  - arbitration.go: Split-brain arbitration support

# Advanced Modules

  - schema.go: Table/schema/user/event management and Group Replication
  - security.go: SQL injection prevention (validation, quoting, safe builders)
  - vendor.go: Database vendor abstraction layer (MySQL/MariaDB/PostgreSQL)
  - bench.go: Benchmarking and testing utilities
  - map.go: Map utility functions

# Security Features

All user-controllable inputs are validated or parameterized to prevent SQL injection:
  - ValidateIdentifier, ValidateGTIDMode, ValidateBinlogFormat, etc.
  - QuoteIdentifier with vendor-specific quoting (backticks/double quotes)
  - SafeQueryBuilder for complex parameterized queries
  - 25+ functions refactored with parameterized queries
  - Risk reduced from Critical to Low

# Vendor Abstraction

DatabaseVendor interface provides clean database abstraction:
  - MySQL, MariaDB, PostgreSQL implementations
  - Eliminates scattered version checks
  - Easy to extend for new databases
*/

package dbhelper
