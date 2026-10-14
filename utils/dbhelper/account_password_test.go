// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"database/sql"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/signal18/replication-manager/utils/version"
)

// #2024: on MariaDB an account statement carries the hash, never the password (MariaDB
// writes it verbatim to the binary log); a strict password validation that refuses the
// hash (1290) gets the password; MySQL rewrites its binlog itself and gets the password.
func TestAccountStatementsSendTheHash(t *testing.T) {
	if got := passwordHashNative("password"); got != "*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19" {
		t.Fatalf("mysql_native_password hash of 'password': %s", got)
	}
	mariadb, _ := version.NewVersion("MariaDB", 13, 0, 2)
	mysql, _ := version.NewVersion("MySQL", 8, 0, 36)
	hashed := "IDENTIFIED BY PASSWORD '*2470C0C06DEE42FD1618BB99005ADCA2EC9D1E19'"

	db, mock := mockDB(t, "mysql")
	mock.ExpectExec(regexp.QuoteMeta("CREATE USER IF NOT EXISTS `app1`@`%` " + hashed)).WillReturnResult(sqlmock.NewResult(0, 0))
	logged, err := CreateUser(db, mariadb, "%", "app1", "password")
	if err != nil || strings.Contains(logged, "password'") || !strings.Contains(logged, "'*****'") {
		t.Fatalf("MariaDB CREATE USER: %v %q", err, logged)
	}
	mock.ExpectExec(regexp.QuoteMeta("ALTER USER `repman`@`%` " + hashed)).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := SetUserPassword(db, mariadb, "%", "repman", "password"); err != nil {
		t.Fatalf("MariaDB ALTER USER: %v", err)
	}
	// strict password validation refuses the hash: the password, as before
	mock.ExpectExec(regexp.QuoteMeta("ALTER USER `repman`@`%` " + hashed)).WillReturnError(&mysqldriver.MySQLError{Number: 1290, Message: "--strict-password-validation"})
	mock.ExpectExec(regexp.QuoteMeta("ALTER USER `repman`@`%` IDENTIFIED BY 'password'")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := SetUserPassword(db, mariadb, "%", "repman", "password"); err != nil {
		t.Fatalf("MariaDB strict validation fallback: %v", err)
	}
	// another error is returned as is, no second statement
	mock.ExpectExec(regexp.QuoteMeta("ALTER USER `repman`@`%` " + hashed)).WillReturnError(sql.ErrConnDone)
	if _, err := SetUserPassword(db, mariadb, "%", "repman", "password"); err != sql.ErrConnDone {
		t.Fatalf("other error: %v", err)
	}
	// MySQL rewrites its binary log itself
	mock.ExpectExec(regexp.QuoteMeta("CREATE USER IF NOT EXISTS `app1`@`%` IDENTIFIED BY 'password'")).WillReturnResult(sqlmock.NewResult(0, 0))
	if _, err := CreateUser(db, mysql, "%", "app1", "password"); err != nil {
		t.Fatalf("MySQL CREATE USER: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// #2025: a replica's errors are masked as they are read
func TestSlaveStatusMaskSecrets(t *testing.T) {
	s := SlaveStatus{
		LastSQLError: sql.NullString{Valid: true, String: "Query: 'CREATE USER `forgejo1`@`%` IDENTIFIED BY 'Xq7kIn9ZpL''"},
		LastIOError:  sql.NullString{Valid: true, String: "error connecting to master 'repl@db2:3306'"},
	}
	s.MaskSecrets()
	if strings.Contains(s.LastSQLError.String, "Xq7kIn9ZpL") || !strings.Contains(s.LastSQLError.String, "IDENTIFIED BY '*****'") {
		t.Fatalf("Last_SQL_Error: %q", s.LastSQLError.String)
	}
	if s.LastIOError.String != "error connecting to master 'repl@db2:3306'" {
		t.Fatalf("no secret, unchanged: %q", s.LastIOError.String)
	}
}
