package cluster

import (
	"database/sql"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// warn0252Open reports whether this tick opened WARN0252 (IsInState reads the previous tick).
func warn0252Open(c *Cluster) bool {
	for _, st := range c.StateMachine.GetOpenStates() {
		if st.ErrKey == "WARN0252" {
			return true
		}
	}
	return false
}

func reseedLinkFixture() (*Cluster, *ServerMonitor) {
	c := &Cluster{Name: "belair", Conf: &config.Config{MasterConn: "belair"}, Log: s18log.NewHttpLog(10)}
	c.StateMachine = new(state.StateMachine)
	c.StateMachine.Init()
	c.master = &ServerMonitor{Host: "db2.belair", IP: "10.0.0.2", Port: "3306", URL: "db2.belair:3306"}
	sv := &ServerMonitor{URL: "db1.child:3306", ClusterGroup: c, ReplicationSourceName: "belair"}
	return c, sv
}

func channel(name, host, io, sqlRunning string) dbhelper.SlaveStatus {
	return dbhelper.SlaveStatus{
		ConnectionName:  sql.NullString{String: name, Valid: true},
		MasterHost:      sql.NullString{String: host, Valid: true},
		MasterPort:      sql.NullString{String: "3306", Valid: true},
		SlaveIORunning:  sql.NullString{String: io, Valid: true},
		SlaveSQLRunning: sql.NullString{String: sqlRunning, Valid: true},
	}
}

// A restore positions a link pointing to the primary (or a new replica's missing link),
// never one pointing elsewhere: moving it is the rejoin's job (law T27, #2093).
func TestReseedLinkPointsToPrimary(t *testing.T) {
	_, sv := reseedLinkFixture()
	if ok, why := sv.reseedLinkPointsToPrimary(); !ok {
		t.Fatalf("no channel yet (a new replica): may be positioned, got %q", why)
	}
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "No", "No")}
	if ok, _ := sv.reseedLinkPointsToPrimary(); !ok {
		t.Fatal("a link to the primary by host is positioned")
	}
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "10.0.0.2", "No", "No")}
	if ok, _ := sv.reseedLinkPointsToPrimary(); !ok {
		t.Fatal("a link to the primary by IP is positioned")
	}
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "external.client", "No", "No")}
	if ok, why := sv.reseedLinkPointsToPrimary(); ok || why == "" {
		t.Fatal("a link to another source is never moved by a restore")
	}
	sv.Replications = []dbhelper.SlaveStatus{channel("other", "external.client", "Yes", "Yes")}
	if ok, _ := sv.reseedLinkPointsToPrimary(); !ok {
		t.Fatal("only the managed channel decides; another channel is not positioned anyway")
	}
}

// The channels a reseed stopped keep WARN0252 open, tick after tick, until each runs
// again, whoever starts it.
func TestReseedStoppedLinksState(t *testing.T) {
	c, sv := reseedLinkFixture()
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "No", "No"), channel("crm", "db1.crm", "No", "No")}
	sv.markReseedStoppedLinks(sv.replicationChannels(), "restore of /backup")

	sv.checkReseedStoppedLinks()
	if !warn0252Open(c) {
		t.Fatal("WARN0252 while the stopped channels do not run")
	}

	c.StateMachine.ClearState()
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "Yes", "Yes"), channel("crm", "db1.crm", "No", "No")}
	sv.checkReseedStoppedLinks()
	if got := sv.reseedStoppedLinkList(); len(got) != 1 || got[0] != "crm" {
		t.Fatalf("the running channel leaves the list: %v", got)
	}
	if !warn0252Open(c) {
		t.Fatal("WARN0252 stays while one stopped channel does not run")
	}

	c.StateMachine.ClearState()
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "Yes", "Yes"), channel("crm", "db1.crm", "Yes", "Yes")}
	sv.checkReseedStoppedLinks()
	if len(sv.reseedStoppedLinkList()) != 0 || warn0252Open(c) {
		t.Fatal("every stopped channel runs again: WARN0252 closes")
	}
}
