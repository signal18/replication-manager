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

The package is organized into the following modules:

  - connection.go: Connection management and address resolution
  - status.go: Server status and variable queries
  - replication.go: Replication control and monitoring
  - binlog.go: Binary log operations
  - transaction.go: Transaction control and locking
  - performance.go: Performance monitoring and query analysis
  - tables.go: Table and schema operations
  - users.go: User and privilege management
  - events.go: Event scheduler operations
  - benchmarks.go: Benchmark utilities
  - groupreplication.go: MySQL Group Replication
  - spider.go: Spider storage engine operations
  - types.go: All data type definitions

This refactoring improves code organization, testability, and maintainability.
*/
package dbhelper
