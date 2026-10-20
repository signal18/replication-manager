package cluster

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"

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

// A channel gone from the replica's status (removed by the operator) leaves the list
// while the server answers: WARN0252 never stays open forever (F3/F4). A server that does
// not answer keeps the list.
func TestReseedStoppedLinkRemovedChannel(t *testing.T) {
	c, sv := reseedLinkFixture()
	sv.markReseedStoppedLinks([]string{"belair", "crm"}, "restore of /backup")
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "No", "No")}

	sv.checkReseedStoppedLinks()
	if got := sv.reseedStoppedLinkList(); len(got) != 2 {
		t.Fatalf("a server that does not answer keeps the list: %v", got)
	}

	c.StateMachine.ClearState()
	sv.Conn = &sqlx.DB{}
	sv.checkReseedStoppedLinks()
	if got := sv.reseedStoppedLinkList(); len(got) != 1 || got[0] != "belair" {
		t.Fatalf("the removed channel leaves the list, the stopped one stays: %v", got)
	}
	if !warn0252Open(c) {
		t.Fatal("WARN0252 stays for the channel still stopped")
	}
}

// Within two ticks of a recording a running status may predate the STOP: the channel stays
// and WARN0252 opens. Records and checks from two goroutines never lose an update.
func TestReseedStoppedLinksGraceAndConcurrency(t *testing.T) {
	c, sv := reseedLinkFixture()
	c.Conf.MonitoringTicker = 2
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "Yes", "Yes")}
	sv.markReseedStoppedLinks([]string{"belair"}, "restore of /backup")
	sv.checkReseedStoppedLinks()
	if got := sv.reseedStoppedLinkList(); len(got) != 1 || !warn0252Open(c) {
		t.Fatalf("a running status read before the stop does not clear a fresh record: %v", got)
	}

	c.Conf.MonitoringTicker = 0
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			sv.markReseedStoppedLinks([]string{"belair"}, "restore")
		}
		close(done)
	}()
	for i := 0; i < 500; i++ {
		sv.checkReseedStoppedLinks()
	}
	<-done
}

// cinf0020 is the CINF0020 this tick opened, if any (IsInState reads the previous tick).
func cinf0020(c *Cluster) *state.State {
	for _, st := range c.StateMachine.GetOpenStates() {
		if st.ErrKey == "CINF0020" {
			return &st
		}
	}
	return nil
}

// A PITR leaves its server standalone (#2115): CINF0020 names the server, the restore and the
// removed channel while it stays so, and ends once a rejoin or a reseed brings the channel
// back on a server that answers. A status within two ticks of the removal, or from a server
// that does not answer, does not end it.
func TestPITRRemovedLinkState(t *testing.T) {
	c, sv := reseedLinkFixture()
	removed := func(at time.Time) {
		sv.reseedLinks = reseedStoppedLinks{channels: []string{"belair"}, reason: "point-in-time restore from backup 42", at: at, removed: true}
	}
	old := time.Now().Add(-time.Hour)

	removed(old)
	sv.checkReseedStoppedLinks()
	st := cinf0020(c)
	if st == nil || st.ErrType != config.LvlInfo || st.ServerUrl != sv.URL {
		t.Fatalf("CINF0020 while the server stays standalone: %+v", st)
	}
	if warn0252Open(c) {
		t.Fatal("a removed link is not a stopped one: no WARN0252")
	}
	for _, want := range []string{sv.URL, "point-in-time restore from backup 42", "'belair'"} {
		if !strings.Contains(st.ErrDesc, want) {
			t.Errorf("CINF0020 %q does not name %q", st.ErrDesc, want)
		}
	}

	c.StateMachine.ClearState()
	sv.Replications = []dbhelper.SlaveStatus{channel("belair", "db2.belair", "Yes", "Yes")}
	sv.checkReseedStoppedLinks()
	if cinf0020(c) == nil {
		t.Fatal("a server that does not answer keeps CINF0020")
	}

	c.StateMachine.ClearState()
	sv.Conn = &sqlx.DB{}
	c.Conf.MonitoringTicker = 2
	removed(time.Now())
	sv.checkReseedStoppedLinks()
	if cinf0020(c) == nil {
		t.Fatal("a status read before the removal does not end a fresh record")
	}

	c.StateMachine.ClearState()
	removed(old)
	sv.checkReseedStoppedLinks()
	if cinf0020(c) != nil || len(sv.reseedStoppedLinkList()) != 0 {
		t.Fatal("the channel back on a server that answers ends CINF0020")
	}
	sv.checkReseedStoppedLinks()
	if cinf0020(c) != nil {
		t.Fatal("CINF0020 stays closed once ended")
	}
}

func TestChannelList(t *testing.T) {
	if got := channelList([]string{"", "crm"}); got != "'default', 'crm'" {
		t.Fatalf("channelList: %s", got)
	}
}
