package cluster

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// A connection set to sql_log_bin=0 is discarded on Close, never returned to the pool:
// the next user of server.Conn would otherwise write without binlogging, out of
// replication (#1978).
func TestNoBinlogConnNotReturnedToPool(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, _ := version.NewVersionFromString("MariaDB", "12.3.3-MariaDB")
	s := &ServerMonitor{URL: "db1:3306", DBVersion: v}
	pool := sqlx.NewDb(db, "sqlmock")

	mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))
	conn, err := s.GetConnNoBinlog(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if st := pool.Stats(); st.Idle != 0 || st.OpenConnections != 0 {
		t.Fatalf("the unlogged session must not go back to the pool: %+v", st)
	}
}
