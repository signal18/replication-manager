package cluster

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
	"github.com/signal18/replication-manager/utils/state"
)

// a child cluster (a standby: no plugin process starts) with one feed into db1.child:3306
func cdcFixture(t *testing.T) (*Cluster, *ServerMonitor, string) {
	t.Helper()
	c := &Cluster{Name: "child", Conf: &config.Config{WorkingDir: t.TempDir()}, Log: s18log.NewHttpLog(10)}
	c.StateMachine = new(state.StateMachine)
	c.StateMachine.Init()
	sv := &ServerMonitor{Host: "db1.child", Port: "3306", URL: "db1.child:3306", ClusterGroup: c}
	path := filepath.Join(c.replPluginDir(), "analytic.toml")
	cfg := wire.Config{Plugin: "innodb", Enabled: true,
		Source: wire.Endpoint{Host: "db1.parent", Port: 3306, User: "repman"},
		Target: wire.Endpoint{Host: "db1.child", Port: 3306, User: "repman"}}
	if err := writeReplPluginConfig(path, cfg); err != nil {
		t.Fatal(err)
	}
	// a feed into another server is not this server's link
	other := cfg
	other.Target.Host = "db2.child"
	if err := writeReplPluginConfig(filepath.Join(c.replPluginDir(), "other.toml"), other); err != nil {
		t.Fatal(err)
	}
	return c, sv, path
}

func TestCDCFeedIsALinkOfItsTarget(t *testing.T) {
	c, sv, path := cdcFixture(t)
	feeds := c.cdcFeedsOf(sv)
	if len(feeds) != 1 || feeds[0].Name() != "cdc:analytic" || KindOf(feeds[0]) != ReplicationKindCDC || CanFailover(feeds[0]) {
		t.Fatalf("one feed, cdc:analytic, never moved by a failover: %+v", feeds)
	}
	if feeds[0].TargetServer() != sv || feeds[0].TargetCluster() != c || feeds[0].SourceServer().Host != "db1.parent" || feeds[0].SourceServer().Server != nil {
		t.Fatalf("its ends: into db1.child, from an unmonitored db1.parent: %+v", feeds[0].SourceServer())
	}
	// one list, as Refresh stores it: the native channels, then the feeds
	sv.Replications = append(sv.replicationsOfRows(channel("parent", "db1.parent", "Yes", "Yes")), feeds...)
	if len(sv.NativeReplications()) != 1 || KindOf(sv.ReplicationChannelLink("cdc:analytic")) != ReplicationKindNative {
		t.Fatal("the native channels are the native entries only, a channel link never a feed")
	}
	all := sv.ReplicationLinkStatus()
	if len(all) != 2 || all[1].Kind != "cdc" || all[1].MasterHost.String != "db1.parent" {
		t.Fatalf("the native channel then the feed: %+v", all)
	}
	// enabled, no process (a standby): not running, nothing invented
	row := all[1]
	if row.SlaveIORunning.String != "No" || row.SecondsBehindMaster.Valid || row.UsingGtid.Valid || row.MasterLogFile.Valid {
		t.Fatalf("a feed without a process: stopped, no lag, no binlog field: %+v", row)
	}

	// a running process's status line: capture = read, apply = apply
	r := &replPluginRunner{path: path, done: make(chan struct{})}
	r.status.Store(&wire.Status{State: wire.StateRunning, GTID: "0-1-20", AppliedGTID: "0-1-18", LagSeconds: 3, ApplyError: "Lock wait timeout"})
	c.replPlugins = map[string]*replPluginRunner{path: r}
	row = cdcRow(feeds[0].Name(), feeds[0].(*cdcReplication).feed)
	if row.SlaveIORunning.String != "Yes" || row.SlaveSQLRunning.String != "No" || row.LastSQLError.String != "Lock wait timeout" {
		t.Fatalf("capture runs, the apply fails: %+v", row)
	}
	if row.GtidIOPos.String != "0-1-20" || row.GtidSlavePos.String != "0-1-18" || row.SecondsBehindMaster.Int64 != 3 {
		t.Fatalf("GTIDs and lag: %+v", row)
	}
	r.stopped.Store(&replPluginStop{Reason: "exited", Restart: time.Now()})
	if row = cdcRow(feeds[0].Name(), feeds[0].(*cdcReplication).feed); row.SlaveIORunning.String != "No" || row.LastIOError.String != "exited" {
		t.Fatalf("a stopped process: %+v", row)
	}
}

func TestCDCFeedStartStop(t *testing.T) {
	c, sv, path := cdcFixture(t)
	link := c.cdcFeedsOf(sv)[0]
	if _, err := link.Stop(LinkByOperator); err != nil {
		t.Fatal(err)
	}
	var cfg wire.Config
	if _, err := toml.DecodeFile(path, &cfg); err != nil || cfg.Enabled {
		t.Fatalf("a stop disables the TOML: %v %+v", err, cfg)
	}
	if got := sv.linkChangeList(); len(got) != 1 || got[0].channel != "cdc:analytic" || got[0].reason != LinkByOperator {
		t.Fatalf("the stop is recorded (T27): %+v", got)
	}
	sv.Replications = c.cdcFeedsOf(sv)
	c.Conf.MonitoringTicker = 0
	sv.checkLinkChanges()
	if !stateOpen(c, "WARN0352") {
		t.Fatal("WARN0352 while the feed is stopped")
	}
	if _, err := link.Start(LinkByOperator); err != nil {
		t.Fatal(err)
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil || !cfg.Enabled {
		t.Fatalf("a start enables the TOML: %v %+v", err, cfg)
	}
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("a start drops the record")
	}
}

// what a feed has no concept of is refused, never a silent nil
func TestCDCFeedUnsupported(t *testing.T) {
	c, sv, _ := cdcFixture(t)
	link := c.cdcFeedsOf(sv)[0]
	for name, op := range map[string]func() (string, error){
		"StartCopyEvents":  func() (string, error) { return link.StartCopyEvents(LinkByTest) },
		"StopCopyEvents":   func() (string, error) { return link.StopCopyEvents(LinkByTest) },
		"StartApplyEvents": func() (string, error) { return link.StartApplyEvents(LinkByTest) },
		"StopApplyEvents":  func() (string, error) { return link.StopApplyEvents(LinkByTest) },
		"Promote":          func() (string, error) { return link.Promote(LinkByTest) },
		"SetPosition":      func() (string, error) { return link.SetPosition(Position{GTID: "0-1-1"}, LinkByTest) },
		"WaitCaughtUp":     func() (string, error) { return link.WaitCaughtUp(Position{GTID: "0-1-1"}, time.Second) },
		"Configure":        func() (string, error) { return link.Configure(LinkOptions{ExecMode: "STRICT"}, LinkByTest) },
		"RotateCredential": func() (string, error) { return link.RotateCredential(LinkByTest) },
		"ResetPositions":   func() (string, error) { return link.ResetPositions(LinkByTest) },
		"Remove":           func() (string, error) { return link.Remove(LinkByTest) },
		"ChangeSource":     func() (string, error) { return link.ChangeSource(Endpoint{}, ChangeSourceOpt{}, LinkByTest) },
		"SkipEvent":        func() (string, error) { return link.SkipEvent(LinkByTest) },
	} {
		if _, err := op(); !errors.Is(err, ErrReplicationNotSupported) {
			t.Errorf("%s: want ErrReplicationNotSupported, got %v", name, err)
		}
	}
	if err := c.ReplicationPlugin(ReplicationPluginCDC).Bootstrap(c, "feed", sv); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("Bootstrap: want ErrReplicationNotSupported (step 5), got %v", err)
	}
	if _, err := c.ReplicationPlugin(ReplicationPluginCDC).Create(Endpoint{}, sv, "cdc:x", LinkByTest); !errors.Is(err, ErrReplicationNotSupported) {
		t.Errorf("Create: want ErrReplicationNotSupported (step 5), got %v", err)
	}
	if len(sv.linkChangeList()) != 0 {
		t.Fatal("a refused operation records nothing")
	}
}

func TestCDCMergeLate(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	hour := time.Hour
	if late := cdcMergeLate(now.Add(-30*time.Minute).Format(time.RFC3339), hour, now); late != 0 {
		t.Fatalf("due 30 min ago with a 1 h interval: not late, got %s", late)
	}
	if late := cdcMergeLate(now.Add(-90*time.Minute).Format(time.RFC3339), hour, now); late != 90*time.Minute {
		t.Fatalf("due 90 min ago: late by 90 min, got %s", late)
	}
	if late := cdcMergeLate("", hour, now); late != 0 {
		t.Fatal("no merge due: not late")
	}
}

func TestReadReplPluginApplied(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.applied.json")
	if err := os.WriteFile(p, []byte(`{"seq":4,"gtid":"0-1-9","indexes":["orders@x"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	a, err := readReplPluginApplied(p)
	if err != nil || a.GTID != "0-1-9" || a.Seq != 4 {
		t.Fatalf("%+v %v", a, err)
	}
}
