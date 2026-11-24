// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package dbhelper

import (
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"github.com/signal18/replication-manager/utils/version"
)

// The error of a failed CHANGE REPLICATION SOURCE keeps its message, with the password redacted,
// and the server's error stays reachable for errors.As: a caller that needs the error number reads
// it from the error, not from its text.
func TestChangeMasterErrorKeepsMessageAndServerError(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("CHANGE REPLICATION SOURCE").WillReturnError(&mysql.MySQLError{Number: 3079, SQLState: [5]byte{'H', 'Y', '0', '0', '0'}, Message: "Multiple channels exist on the replica. Please provide channel name as an argument."})

	opt := ChangeMasterOpt{Host: "primary", Port: "3306", User: "repl", Password: "s3cr3t", Mode: "MASTER_AUTO_POSITION"}
	logs, err := ChangeMaster(sqlx.NewDb(db, "sqlmock"), opt, &version.Version{Flavor: "MySQL", Major: 8, Minor: 4})
	if err == nil {
		t.Fatal("expected the statement error")
	}
	want := "Error 3079 (HY000): Multiple channels exist on the replica. Please provide channel name as an argument."
	if !strings.HasSuffix(err.Error(), "failed, reason: "+want) || !strings.HasPrefix(err.Error(), "Change SOURCE statement ") {
		t.Errorf("the formatted message changed: %q", err.Error())
	}
	if strings.Contains(err.Error(), "s3cr3t") || strings.Contains(logs, "s3cr3t") {
		t.Errorf("the password is not redacted: %q / %q", err.Error(), logs)
	}
	var me *mysql.MySQLError
	if !errors.As(err, &me) || me.Number != 3079 {
		t.Errorf("the server's error must stay reachable with errors.As, got %v", err)
	}
}
