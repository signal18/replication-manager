package cluster

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/version"
)

// freshTestServer is a MariaDB node answering the fresh-node reads: install age vs uptime,
// the monitoring account's global SELECT, its schemas.
func freshTestServer(t *testing.T, install, up int64, selectPriv string, schemas []string, ageErr error) (*ServerMonitor, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	v, _ := version.NewVersionFromString("MariaDB", "10.11.19-MariaDB")
	c := &Cluster{Name: "c1", Conf: &config.Config{Secrets: map[string]config.Secret{"db-servers-credential": {Value: "root:pw"}}}}
	c.Log = s18log.NewHttpLog(20)
	q := mock.ExpectQuery("TIMESTAMPDIFF").WithArgs("global_priv")
	if ageErr != nil {
		q.WillReturnError(ageErr)
	} else {
		q.WillReturnRows(sqlmock.NewRows([]string{"a", "u"}).AddRow(install, strconv.FormatInt(up, 10)))
		mock.ExpectQuery("select user").WillReturnRows(sqlmock.NewRows([]string{"u"}).AddRow("root@127.0.0.1"))
		mock.ExpectQuery("FROM mysql.user").WithArgs("root").WillReturnRows(sqlmock.NewRows([]string{"Host", "Select_priv", "Process_priv", "Super_priv", "Repl_slave_priv", "Repl_client_priv", "Reload_priv"}).AddRow("%", selectPriv, "Y", "Y", "Y", "Y", "Y"))
		if selectPriv == "Y" {
			rows := sqlmock.NewRows([]string{"SCHEMA_NAME"})
			for _, sc := range schemas {
				rows.AddRow(sc)
			}
			mock.ExpectQuery("SCHEMATA").WillReturnRows(rows)
		}
	}
	return &ServerMonitor{URL: "db2:3306", ClusterGroup: c, Conn: sqlx.NewDb(db, "sqlmock"), DBVersion: v}, mock
}

// Fresh is proven: installed with this start AND the monitoring account sees every schema
// AND none is a user schema; anything else, or a read that fails, is not fresh (#1979).
func TestCheckFreshNode(t *testing.T) {
	prov := []string{"replication_manager_schema"}
	cases := []struct {
		name        string
		install, up int64
		selectPriv  string
		schemas     []string
		ageErr      error
		fresh       bool
		reason      string
	}{
		{"new node: installed with this start, only the provisioning schema", 70, 60, "Y", prov, nil, true, "installed with this start"},
		{"old node never restarted holding data", 259201, 259200, "Y", append(prov, "app"), nil, false, "user data"},
		{"an empty user schema is user data", 70, 60, "Y", append(prov, "app"), nil, false, "user data"},
		{"old datadir restarted, empty", 777600, 3600, "Y", prov, nil, false, "before this start"},
		{"monitoring account without global SELECT", 70, 60, "N", nil, nil, false, "no global SELECT"},
		{"unreadable", 0, 0, "Y", nil, errors.New("boom"), false, "not readable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, _ := freshTestServer(t, c.install, c.up, c.selectPriv, c.schemas, c.ageErr)
			fn := s.CheckFreshNode()
			if fn.Fresh != c.fresh || !strings.Contains(fn.Reason, c.reason) {
				t.Fatalf("fresh %v reason %q", fn.Fresh, fn.Reason)
			}
		})
	}
}

// The reseed function itself refuses a node not proven fresh, before any restore step: no
// caller can reach a node holding data (F1, #1979).
func TestReseedMasterSSTRefusesNodeWithData(t *testing.T) {
	s, mock := freshTestServer(t, 259201, 259200, "Y", []string{"replication_manager_schema", "app"}, nil)
	s.ClusterGroup.Conf.AutorejoinMysqldump = true
	if err := s.ReseedMasterSST(); !errors.Is(err, errRestoreRefused) {
		t.Fatalf("a node with data must be refused, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("only the fresh-node reads, no restore step: %v", err)
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
