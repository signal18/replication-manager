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
