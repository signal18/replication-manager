package cluster

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/signal18/replication-manager/config"
)

func TestInjectTrafficIsNotReentrant(t *testing.T) {
	c := &Cluster{Name: "c"}
	t0 := time.Date(2026, 10, 5, 9, 4, 19, 0, time.UTC)
	if !c.tryStartInjectTraffic(t0) {
		t.Fatal("first injection must start")
	}
	if c.tryStartInjectTraffic(t0.Add(2 * time.Second)) {
		t.Fatal("a second injection must be refused while the first is in flight (the belair dump case)")
	}
	if got := time.Unix(c.injectTrafficSince.Load(), 0); !got.Equal(t0) {
		t.Fatalf("since must stay the FIRST start, got %v", got)
	}
	c.endInjectTraffic()
	if !c.tryStartInjectTraffic(t0.Add(time.Minute)) {
		t.Fatal("after the first returns the next tick starts one again")
	}
}

func TestInjectTrafficNeverDDLOnGalera(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{InjectTrafficMode: "ddl", ForceSlaveNoGtid: true}}
	if !c.injectTrafficUsesDDL() {
		t.Fatal("asynchronous replication honours inject-traffic-mode=ddl / force-slave-no-gtid-mode")
	}
	c.Conf.MultiMasterWsrep = true
	if c.injectTrafficUsesDDL() {
		t.Fatal("a Galera target must get the DML row, never a view replaced on every tick (#2195)")
	}
	c.Conf.MultiMasterWsrep = false
	c.Topology = config.TopoMultiMasterWsrep
	if c.injectTrafficUsesDDL() {
		t.Fatal("a discovered Galera cluster must get the DML row (#2195)")
	}
}

func TestMarkerTableMissing(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{nil, false},
		{&mysql.MySQLError{Number: 1146}, true},
		{fmt.Errorf("proxy: %w", &mysql.MySQLError{Number: 1146}), true},
		{&mysql.MySQLError{Number: 1045}, false},
		{&pq.Error{Code: "42P01"}, true},
		{&pq.Error{Code: "42501"}, false},
		{errors.New("Table 'replication_manager_schema.pseudo_gtid_hist' doesn't exist"), false},
	}
	for _, tc := range cases {
		if got := markerTableMissing(tc.err); got != tc.want {
			t.Errorf("markerTableMissing(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

// The schema dropped under a running repman: the REPLACE gets 1146, the next injection
// creates the table again instead of failing until a restart (#2195).
func TestInjectTrafficRecreatesDroppedTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sdb := sqlx.NewDb(db, "sqlmock")
	c := &Cluster{Name: "c", Conf: &config.Config{InjectTrafficMode: "dml"}}
	c.injectTrafficTableReady = map[string]bool{"prx1": true}

	mock.ExpectExec("REPLACE INTO replication_manager_schema.pseudo_gtid_hist").WillReturnError(&mysql.MySQLError{Number: 1146, Message: "doesn't exist"})
	if err := c.injectTrafficMarker(sdb, "", "prx1"); !markerTableMissing(err) {
		t.Fatalf("want the 1146 back for ERR00050, got %v", err)
	}
	if c.injectTrafficTableReady["prx1"] {
		t.Fatal("a missing table must clear the ready flag")
	}

	mock.ExpectExec("CREATE DATABASE IF NOT EXISTS replication_manager_schema").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("CREATE TABLE IF NOT EXISTS replication_manager_schema.pseudo_gtid_hist").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("INSERT IGNORE INTO replication_manager_schema.pseudo_gtid_hist").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("CREATE OR REPLACE +VIEW replication_manager_schema.pseudo_gtid_v").WillReturnResult(sqlmock.NewResult(0, 0))
	if err := c.injectTrafficMarker(sdb, "", "prx1"); err != nil {
		t.Fatalf("the next injection must create the table again: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
	if !c.injectTrafficTableReady["prx1"] {
		t.Fatal("ready again once the table is created")
	}
}

// A traffic stop makes the next start run the CREATE ... IF NOT EXISTS setup again, and
// the last error is kept for the tick until an injection succeeds (no ERR00050 flapping).
func TestInjectTrafficStopAndErrorKept(t *testing.T) {
	c := &Cluster{Name: "c", Conf: &config.Config{}}
	c.injectTrafficTableReady = map[string]bool{"prx1": true}
	c.recordInjectTrafficResult(errors.New("boom"))
	if msg := c.injectTrafficErr.Load(); msg == nil || *msg != "boom" {
		t.Fatalf("the last injection error must be kept for the tick, got %v", msg)
	}
	c.stopInjectProxiesTraffic()
	if c.injectTrafficErr.Load() != nil {
		t.Fatal("no traffic, no ERR00050")
	}
	c.InjectProxiesTraffic() // no proxy, no master: only applies the stop
	if c.injectTrafficTableReady != nil {
		t.Fatal("after a stop the next start must create the table again")
	}
	if c.injectTrafficStopped.Load() {
		t.Fatal("the stop is consumed by the injection")
	}
	c.recordInjectTrafficResult(nil)
	if c.injectTrafficErr.Load() != nil {
		t.Fatal("a successful injection clears the error")
	}
}
