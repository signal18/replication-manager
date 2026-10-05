// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-mysql-org/go-mysql/replication"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/s18log"
)

type nopModulePrintf struct{}

func (nopModulePrintf) LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int {
	return 0
}

// recordingModulePrintf captures every force-logged ("loud") line so tests
// can assert on visibility, independent of what a downstream caller does
// with the returned error.
type recordingModulePrintf struct {
	forced []string
}

func (p *recordingModulePrintf) LogModulePrintf(forcingLog bool, module int, level string, format string, args ...interface{}) int {
	if forcingLog {
		p.forced = append(p.forced, level+": "+fmt.Sprintf(format, args...))
	}
	return 0
}

// TestNewSafeBinlogSyncer_RecoversFromServerIDZeroFatal is the last-resort
// safety net test: go-mysql's replication.NewBinlogSyncer calls Logger.Fatal
// synchronously when ServerID==0, and BinlogSyncerLogger.Fatal panics with
// s18log.FatalError instead of calling os.Exit(1) precisely so this wrapper
// can recover it. binlogSyncerServerID is expected to prevent ServerID==0
// from ever reaching this call in normal operation, but if that guard is
// ever missed, this must fail with an error, not take down the process.
func TestNewSafeBinlogSyncer_RecoversFromServerIDZeroFatal(t *testing.T) {
	cfg := replication.BinlogSyncerConfig{
		ServerID: 0,
		Flavor:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		Logger:   s18log.NewBinlogSyncerLogger(nopModulePrintf{}, "127.0.0.1:3306", "test", 0),
	}

	syncer, err := newSafeBinlogSyncer(cfg)
	if err == nil {
		t.Fatal("expected an error for ServerID=0; without recovery this would have panicked/exited the test process")
	}
	if syncer != nil {
		t.Fatal("expected a nil syncer alongside the error")
	}
	if !strings.Contains(err.Error(), "server ID") {
		t.Fatalf("expected error to mention the server ID, got: %v", err)
	}
}

// TestNewSafeBinlogSyncer_RecoveryIsLoud ensures a recovered constructor
// panic is always force-logged at Error level with the panic value and a
// stack trace, regardless of what level the caller receiving the returned
// error chooses to log it at (some call sites only log the returned error at
// Debug — see RefreshBinlogMetadata / ScanBinlogQueryEvents).
func TestNewSafeBinlogSyncer_RecoveryIsLoud(t *testing.T) {
	p := &recordingModulePrintf{}
	cfg := replication.BinlogSyncerConfig{
		ServerID: 0,
		Flavor:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		Logger:   s18log.NewBinlogSyncerLogger(p, "127.0.0.1:3306", "test", 0),
	}

	if _, err := newSafeBinlogSyncer(cfg); err == nil {
		t.Fatal("expected an error for ServerID=0")
	}

	var recoveryLog string
	for _, line := range p.forced {
		if strings.Contains(line, "recovered from panic") {
			recoveryLog = line
			break
		}
	}
	if recoveryLog == "" {
		t.Fatalf("expected a force-logged recovery line, got forced logs: %v", p.forced)
	}
	if !strings.HasPrefix(recoveryLog, config.LvlErr+":") {
		t.Fatalf("expected recovery to log at %s level, got: %q", config.LvlErr, recoveryLog)
	}
	if !strings.Contains(recoveryLog, "server ID") {
		t.Fatalf("expected recovery log to include the panic value, got: %q", recoveryLog)
	}
	if !strings.Contains(recoveryLog, "newSafeBinlogSyncer") {
		t.Fatalf("expected recovery log to include a stack trace mentioning newSafeBinlogSyncer, got: %q", recoveryLog)
	}
}

// TestNewSafeBinlogSyncer_Succeeds sanity-checks that a valid config still
// constructs a real syncer through the wrapper (i.e. the recover() doesn't
// swallow the happy path).
func TestNewSafeBinlogSyncer_Succeeds(t *testing.T) {
	cfg := replication.BinlogSyncerConfig{
		ServerID: 12345,
		Flavor:   "mysql",
		Host:     "127.0.0.1",
		Port:     3306,
		Logger:   s18log.NewBinlogSyncerLogger(nopModulePrintf{}, "127.0.0.1:3306", "test", 0),
	}

	syncer, err := newSafeBinlogSyncer(cfg)
	if err != nil {
		t.Fatalf("expected no error for a valid config, got: %v", err)
	}
	if syncer == nil {
		t.Fatal("expected a non-nil syncer")
	}
	syncer.Close()
}

// TestBinlogSyncerConstructionOnlyThroughSafeWrapper guards against a future
// call site bypassing newSafeBinlogSyncer and calling
// replication.NewBinlogSyncer directly again, which would reopen the
// unrecoverable-process-exit risk this file exists to close. The only
// allowed direct call is the one inside newSafeBinlogSyncer itself.
func TestBinlogSyncerConstructionOnlyThroughSafeWrapper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	const needle = "replication.NewBinlogSyncer("
	total := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		total += strings.Count(string(data), needle)
	}

	if total != 1 {
		t.Fatalf("expected exactly 1 direct call to replication.NewBinlogSyncer across the cluster package "+
			"(inside newSafeBinlogSyncer in srv_binlog.go), found %d. All binlog syncer construction must go "+
			"through newSafeBinlogSyncer so a go-mysql Logger.Fatal (e.g. ServerID==0) can be recovered as an "+
			"error instead of exiting the whole replication-manager process", total)
	}
}

func TestBinlogServerIDPoolLeasesDistinctIDs(t *testing.T) {
	p := newBinlogServerIDPool(10000)
	id1, rel1, err := p.Acquire("event-scanner")
	if err != nil || id1 != 10000 {
		t.Fatalf("first lease must be the pool base: %d %v", id1, err)
	}
	id2, rel2, err := p.Acquire("binlog-meta")
	if err != nil || id2 != 10001 {
		t.Fatalf("second concurrent lease takes the next id: %d %v", id2, err)
	}
	rel1()
	rel1() // idempotent
	id3, rel3, err := p.Acquire("binlog-backup")
	if err != nil || id3 != 10000 {
		t.Fatalf("a released id is reused: %d %v", id3, err)
	}
	if lo, hi := p.Range(); lo != 10000 || hi != 10010 {
		t.Fatalf("pool is 10000..10010, got %d..%d", lo, hi)
	}
	rel2()
	rel3()
	if len(p.Leases()) != 0 {
		t.Fatalf("every lease released, got %+v", p.Leases())
	}
}

func TestBinlogServerIDPoolRefusesWhenExhausted(t *testing.T) {
	p := newBinlogServerIDPool(10000)
	releases := []func(){}
	for i := 0; i < binlogServerIDPoolSize; i++ {
		_, rel, err := p.Acquire("x")
		if err != nil {
			t.Fatalf("lease %d must succeed: %v", i, err)
		}
		releases = append(releases, rel)
	}
	if _, _, err := p.Acquire("one-too-many"); err == nil {
		t.Fatal("an exhausted pool must refuse, never steal an id in use")
	}
	releases[5]()
	if id, _, err := p.Acquire("again"); err != nil || id != 10005 {
		t.Fatalf("the released id comes back: %d %v", id, err)
	}
	if _, _, err := newBinlogServerIDPool(0).Acquire("x"); err == nil {
		t.Fatal("a zero base (check-binlog-server-id 0) refuses: go-mysql aborts on server-id 0")
	}
}

func TestBinlogServerIDInstanceBlockKeepsInstancesApart(t *testing.T) {
	for _, h := range []string{"repman.s18.svc.cloud18", "repman-dr.s18.svc.cloud18", "repman-dev3", ""} {
		b := binlogServerIDBlockFor(h)
		if b%binlogServerIDPoolSize != 0 || b < 0 || b >= binlogServerIDBlockCount*binlogServerIDPoolSize {
			t.Fatalf("block for %q = %d, want a multiple of %d below %d", h, b, binlogServerIDPoolSize, binlogServerIDBlockCount*binlogServerIDPoolSize)
		}
	}
	if binlogServerIDBlockFor("repman.s18.svc.cloud18") == binlogServerIDBlockFor("repman-dr.s18.svc.cloud18") {
		t.Fatal("the active and the standby must not share a block")
	}
}

func TestBinlogScanStartPositionUsesMasterPosition(t *testing.T) {
	s := &ServerMonitor{}
	s.MasterStatus.File, s.MasterStatus.Position = "binlog.000022", 65371756
	if p := s.binlogScanStartPosition("binlog.000022"); p.Pos != 65371756 || p.Name != "binlog.000022" {
		t.Fatalf("expected the master's current position, got %+v", p)
	}
	// Master status about another file (rotation seen by one path, not the other yet):
	// fall back to the head of the file.
	if p := s.binlogScanStartPosition("binlog.000023"); p.Pos != 4 {
		t.Fatalf("expected position 4 on a file mismatch, got %+v", p)
	}
}

func TestBinlogScanBackoffPolicy(t *testing.T) {
	if binlogScanBackoffFor(1) != 0 || binlogScanBackoffFor(2) != 0 {
		t.Fatal("below the threshold the scanner reopens on the next tick")
	}
	if binlogScanBackoffFor(3) != 30*time.Second || binlogScanBackoffFor(4) != time.Minute || binlogScanBackoffFor(5) != 2*time.Minute {
		t.Fatalf("doubling from 30 s: %v %v %v", binlogScanBackoffFor(3), binlogScanBackoffFor(4), binlogScanBackoffFor(5))
	}
	if binlogScanBackoffFor(40) != 30*time.Minute {
		t.Fatalf("capped at 30 min, got %v", binlogScanBackoffFor(40))
	}
}

func TestNoteBinlogScanResetWindow(t *testing.T) {
	s := &ServerMonitor{}
	t0 := time.Date(2026, 10, 5, 8, 0, 0, 0, time.UTC)
	// belair pattern: a reset every 5 s -> third one arms 30 s, then doubles
	if b := s.noteBinlogScanReset(t0); b != 0 {
		t.Fatalf("first reset: no backoff, got %v", b)
	}
	s.noteBinlogScanReset(t0.Add(5 * time.Second))
	if b := s.noteBinlogScanReset(t0.Add(10 * time.Second)); b != 30*time.Second || !s.binlogScanBackoffUntil.Equal(t0.Add(40*time.Second)) {
		t.Fatalf("third reset: 30 s backoff from now, got %v until %v", b, s.binlogScanBackoffUntil)
	}
	if b := s.noteBinlogScanReset(t0.Add(50 * time.Second)); b != time.Minute {
		t.Fatalf("fourth reset: 1 min, got %v", b)
	}
	// a quiet window forgets the count
	if b := s.noteBinlogScanReset(t0.Add(50*time.Second + binlogScanResetWindow + time.Second)); b != 0 || s.binlogScanResets != 1 {
		t.Fatalf("after a quiet window the count restarts: got %v resets=%d", b, s.binlogScanResets)
	}
}

func TestCloseBinlogEventSyncerReleasesLease(t *testing.T) {
	p := newBinlogServerIDPool(10000)
	s := &ServerMonitor{}
	id, rel, err := p.Acquire("event-scanner")
	if err != nil {
		t.Fatal(err)
	}
	s.binlogEventServerID, s.binlogEventRelease = id, rel
	s.CloseBinlogEventSyncer()
	if len(p.Leases()) != 0 {
		t.Fatalf("closing the scanner must return its lease, got %+v", p.Leases())
	}
	s.CloseBinlogEventSyncer() // idempotent, no panic without a lease
}

func TestServerRebuildReleasesBinlogLeases(t *testing.T) {
	// newServerList / RemoveServerFromIndex drop the old monitors: what they hold on
	// the primary (stream + lease) must be released or every reload leaks one id (#1886).
	c := &Cluster{Name: "c"}
	p := newBinlogServerIDPool(10000)
	old := make([]*ServerMonitor, 0, 3)
	for i := 0; i < 3; i++ {
		id, rel, err := p.Acquire("event-scanner")
		if err != nil {
			t.Fatal(err)
		}
		old = append(old, &ServerMonitor{binlogEventServerID: id, binlogEventRelease: rel})
	}
	c.closeServersBinlogStreams(old)
	if n := len(p.Leases()); n != 0 {
		t.Fatalf("rebuild must release every lease, %d left", n)
	}
	// a nil entry in the list is tolerated (RemoveServerFromIndex guards the same way)
	c.closeServersBinlogStreams([]*ServerMonitor{nil, old[0]})
}

func TestBinlogScanArmedFollowsTheWindow(t *testing.T) {
	s := &ServerMonitor{}
	t0 := time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC)
	s.noteBinlogScanReset(t0)
	s.noteBinlogScanReset(t0.Add(5 * time.Second))
	if s.binlogScanArmed(t0.Add(6 * time.Second)) {
		t.Fatal("below the threshold nothing is armed")
	}
	s.noteBinlogScanReset(t0.Add(10 * time.Second))
	if !s.binlogScanArmed(t0.Add(11*time.Second)) || !s.binlogScanArmed(t0.Add(5*time.Minute)) {
		t.Fatal("armed while sleeping AND while retrying inside the window: the state must not flap")
	}
	if s.binlogScanArmed(t0.Add(10*time.Second + binlogScanResetWindow + time.Second)) {
		t.Fatal("a quiet window resolves the state")
	}
}
