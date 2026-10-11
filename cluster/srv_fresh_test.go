package cluster

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// Fresh is proven: installed with this start AND no user table; anything else, or a read
// that fails, is not fresh (#1979).
func TestCheckFreshNode(t *testing.T) {
	v, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	cases := []struct {
		name          string
		install, up   int64
		tables        bool
		ageErr        error
		fresh         bool
		reasonContain string
	}{
		{"new node: installed with this start, empty", 70, 60, false, nil, true, "installed with this start"},
		{"old node never restarted holding data", 259201, 259200, true, nil, false, "user tables"},
		{"old datadir restarted, empty", 777600, 3600, false, nil, false, "before this start"},
		{"unreadable", 0, 0, false, errors.New("boom"), false, "not readable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			q := mock.ExpectQuery("TIMESTAMPDIFF").WithArgs("global_priv")
			if c.ageErr != nil {
				q.WillReturnError(c.ageErr)
			} else {
				q.WillReturnRows(sqlmock.NewRows([]string{"a", "u"}).AddRow(c.install, strconv.FormatInt(c.up, 10)))
				mock.ExpectQuery("replication_manager_schema").WillReturnRows(sqlmock.NewRows([]string{"e"}).AddRow(c.tables))
			}
			s := &ServerMonitor{URL: "db2:3306", Conn: sqlx.NewDb(db, "sqlmock"), DBVersion: v}
			fn := s.CheckFreshNode()
			if fn.Fresh != c.fresh || !strings.Contains(fn.Reason, c.reasonContain) {
				t.Fatalf("fresh %v reason %q", fn.Fresh, fn.Reason)
			}
		})
	}
}

// myloader's CRITICAL lines name the cause in the returned error.
func TestMyloaderCriticalWrap(t *testing.T) {
	var c myloaderCritical
	c.observe("** Message: 15:57:48.913: Thread 1: restoring")
	c.observe("** (myloader:2425680): CRITICAL **: 15:57:48.914: Connection 1074 - ERROR 1805: Column count of mysql.proc is wrong. Expected 21, found 22")
	err := c.wrap(errors.New("signal: trace/breakpoint trap (core dumped)"))
	if !strings.Contains(err.Error(), "ERROR 1805") || !strings.Contains(err.Error(), "trace/breakpoint trap") {
		t.Fatalf("the cause must be in the error: %v", err)
	}
	var none myloaderCritical
	if e := errors.New("x"); none.wrap(e) != e {
		t.Fatal("no CRITICAL line: the error as is")
	}
}
