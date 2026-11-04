// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
	"github.com/sirupsen/logrus"
)

// The REPLICATION SLAVE check on a replica looks up the replication user, not the monitoring
// user: the monitor account has no grant for the replica's host here, the repl account does,
// so ERR00007 must stay closed (#2245).
func TestCheckPrivilegesLooksUpReplicationUser(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cl := &Cluster{
		Name: "t", SqlErrorLog: logrus.New(), SqlGeneralLog: logrus.New(),
		StateMachine:   new(state.StateMachine),
		RepMgrHostname: "10.1.1.1",
		Conf: &config.Config{
			MonitorCheckGrants: true,
			Secrets: config.NewSecrets(map[string]config.Secret{
				"db-servers-credential":  {Value: "monitor:mpass"},
				"replication-credential": {Value: "repl:rpass"},
			}),
		},
	}
	cl.StateMachine.Init()
	ver := &version.Version{Flavor: "MariaDB", Major: 11, Minor: 4}
	s1 := &ServerMonitor{URL: "10.1.1.2:3306", Host: "10.1.1.2", ClusterGroup: cl, State: stateSlave, DBVersion: ver, Conn: sqlx.NewDb(db, "mysql")}
	s2 := &ServerMonitor{URL: "10.2.2.2:3306", Host: "10.2.2.2", ClusterGroup: cl, State: stateSlave, DBVersion: ver}
	cl.Servers = serverList{s1, s2}

	cols := []string{"Host", "Select_priv", "Process_priv", "Super_priv", "Repl_slave_priv", "Repl_client_priv", "Reload_priv"}
	mock.ExpectQuery("select user()").WillReturnRows(sqlmock.NewRows([]string{"user()"}).AddRow("monitor@10.1.1.1"))
	mock.ExpectQuery("FROM mysql.user").WithArgs("monitor").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("10.1.1.1", "Y", "Y", "Y", "Y", "Y", "Y"))
	// only the replication user has an account for the 10.2.2.x replica
	mock.ExpectQuery("FROM mysql.user").WithArgs("repl").
		WillReturnRows(sqlmock.NewRows(cols).AddRow("10.2.2.%", "N", "N", "N", "Y", "N", "N"))

	s1.CheckPrivileges()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	for _, st := range cl.StateMachine.GetOpenStates() {
		if st.ErrKey == "ERR00007" {
			t.Fatalf("false ERR00007: %+v", st)
		}
	}
}
