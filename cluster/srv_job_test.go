package cluster

import (
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
)

func TestDumpStreamParserExtractsBinlogAndGTID(t *testing.T) {
	binlogRegex := regexp.MustCompile(`CHANGE MASTER TO MASTER_LOG_FILE='(.+)', MASTER_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`SET GLOBAL gtid_slave_pos='(.+)'`)

	var gotFile string
	var gotPos uint64
	var gotGTID string

	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		true,
		true,
		func(file string, pos uint64) {
			gotFile = file
			gotPos = pos
		},
		func(gtid string) {
			gotGTID = gtid
		},
	)

	chunks := [][]byte{
		[]byte("header line\nCHANGE MASTER TO MASTER_LOG_FILE='mysql-bin.000123', MASTER_LOG_POS=45"),
		[]byte("6\nother line\nSET GLOBAL gtid_slave_pos='0-1-2'\ntrailer"),
	}

	for _, chunk := range chunks {
		parser.Consume(chunk)
	}
	parser.Flush()

	if gotFile != "mysql-bin.000123" {
		t.Fatalf("binlog file = %q, want %q", gotFile, "mysql-bin.000123")
	}
	if gotPos != 456 {
		t.Fatalf("binlog pos = %d, want %d", gotPos, 456)
	}
	if gotGTID != "0-1-2" {
		t.Fatalf("gtid = %q, want %q", gotGTID, "0-1-2")
	}
	if parser.Enabled() {
		t.Fatalf("parser should be disabled after extraction")
	}
}

func TestDumpStreamParserExtractsMySQL8(t *testing.T) {
	binlogRegex := regexp.MustCompile(`CHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='(.+)', SOURCE_LOG_POS=(\d+)`)
	gtidRegex := regexp.MustCompile(`GTID_PURGED\s*=\s*(?:/\*![0-9]*\s*'([^']+)'\*/\s*|'([^']+)')`)

	var gotFile string
	var gotPos uint64
	var gotGTID string

	parser := newDumpStreamParser(
		binlogRegex,
		gtidRegex,
		true,
		true,
		func(file string, pos uint64) {
			gotFile = file
			gotPos = pos
		},
		func(gtid string) {
			gotGTID = gtid
		},
	)

	chunks := [][]byte{
		[]byte("header\nCHANGE REPLICATION SOURCE TO SOURCE_LOG_FILE='mysql-bin.000777', SOURCE_LOG_POS=9"),
		[]byte("87\nSET @@GLOBAL.GTID_PURGED=/*!80000 '3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19'*/;\n"),
	}

	for _, chunk := range chunks {
		parser.Consume(chunk)
	}
	parser.Flush()

	if gotFile != "mysql-bin.000777" {
		t.Fatalf("binlog file = %q, want %q", gotFile, "mysql-bin.000777")
	}
	if gotPos != 987 {
		t.Fatalf("binlog pos = %d, want %d", gotPos, 987)
	}
	want := "3E11FA47-71CA-11E1-9E33-C80AA9429562:1-19"
	if gotGTID != want {
		t.Fatalf("gtid = %q, want %q", gotGTID, want)
	}
	if parser.Enabled() {
		t.Fatalf("parser should be disabled after extraction")
	}
}

func TestShouldParseDumpBinlogGTIDRespectsExistingMetadata(t *testing.T) {
	cluster := &Cluster{
		Name: "test-cluster",
		Conf: &config.Config{WorkingDir: t.TempDir()},
	}
	server := &ServerMonitor{
		Host:         "127.0.0.1",
		Port:         "3306",
		ClusterGroup: cluster,
	}
	server.LastBackupMeta.Logical = &backupmgr.BackupMetadata{
		BinLogFileName: "mysql-bin.000001",
		BinLogGtid:     "0-1-2",
	}

	parseBinlog, parseGTID := server.shouldParseDumpBinlogGTID()
	if parseBinlog {
		t.Fatalf("expected parseBinlog to be false when binlog file already set")
	}
	if parseGTID {
		t.Fatalf("expected parseGTID to be false when GTID already set")
	}
}

func newTestServerForAPIJobs(t *testing.T) *ServerMonitor {
	t.Helper()
	tmp := t.TempDir()
	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir:        tmp,
			SchedulerJobsMode: "api",
		},
		Name:         "testcluster",
		StateMachine: &state.StateMachine{},
	}
	server := &ServerMonitor{
		ClusterGroup: cluster,
		Datadir:      tmp + "/node1_3306",
		Host:         "node1",
		Port:         "3306",
	}
	server.JobResults = config.NewTasksMap()
	if err := os.MkdirAll(server.Datadir, 0755); err != nil {
		t.Fatalf("failed to create server datadir: %v", err)
	}
	return server
}

// TestJobInsertTask_APIMode_RemoteTask_CookieFailureDoesNotBlockRetry guards
// against a stale in-progress entry: if the cookie write fails, jobInsertTask
// must not have stored a Done=0 snapshot, or IsInTask would block every retry
// forever since nothing (no dbjobs dispatch happened) would ever move it out
// of Done=0.
func TestJobInsertTask_APIMode_RemoteTask_CookieFailureDoesNotBlockRetry(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskError)

	// Force the cookie write to fail deterministically: os.Create does not
	// create parent directories, so pointing Datadir at a path whose parent
	// doesn't exist makes createCookie fail with ENOENT every time.
	server.Datadir = server.Datadir + "/missing/nested"

	if _, err := server.JobInsertTask(task, "0", "monitor-host"); err == nil {
		t.Fatal("expected JobInsertTask to fail when the cookie write fails")
	}

	if server.IsInTask(task) {
		t.Fatal("expected no stale in-progress entry after a failed cookie write")
	}
	if got := server.JobResults.Get(task); got != nil {
		t.Fatalf("expected no JobResults entry after a failed cookie write, got %+v", got)
	}
}

func assertFreshAPITask(t *testing.T, task *config.Task, previousStart int64) {
	t.Helper()
	if task == nil {
		t.Fatal("expected task entry")
	}
	if task.Start <= previousStart {
		t.Fatalf("expected refreshed Start > %d, got %d", previousStart, task.Start)
	}
	if task.End != 0 {
		t.Fatalf("expected End to be cleared, got %d", task.End)
	}
	if task.Done != 0 {
		t.Fatalf("expected Done=0, got %d", task.Done)
	}
	if task.State != JobStateAvailable {
		t.Fatalf("expected State=JobStateAvailable, got %d", task.State)
	}
}

func TestJobInsertTask_APIMode_RemoteTask_ResetsTimestamps(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskError)
	server.JobResults.Store(task, &config.Task{Task: task, Start: 100, End: 200, Done: 1, State: JobStateSuccess, Result: "old", Payload: "old payload"})

	if _, err := server.JobInsertTaskWithPayload(task, "0", "monitor-host", "new payload"); err != nil {
		t.Fatalf("JobInsertTaskWithPayload returned unexpected error: %v", err)
	}

	got := server.JobResults.Get(task)
	assertFreshAPITask(t, got, 100)
	if got.Payload != "new payload" {
		t.Fatalf("expected Payload=new payload, got %q", got.Payload)
	}
}

func TestJobInsertTask_APIMode_LocalTask_ResetsTimestamps(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskDump)
	server.JobResults.Store(task, &config.Task{Task: task, Start: 100, End: 200, Done: 1, State: JobStateSuccess, Result: "old"})

	if _, err := server.JobInsertTask(task, "0", "monitor-host"); err != nil {
		t.Fatalf("JobInsertTask returned unexpected error: %v", err)
	}

	got := server.JobResults.Get(task)
	assertFreshAPITask(t, got, 100)
	if got.Payload != "" {
		t.Fatalf("expected Payload to be cleared, got %q", got.Payload)
	}
}

func TestJobsUpdateState_APIMode_RunningPreservesRunStartAndClearsEnd(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := "errorlog"

	if err := server.JobsUpdateState(task, "processing", JobStateRunning, 0); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	first := server.JobResults.Get(task)
	if first == nil || first.Start == 0 {
		t.Fatal("expected Start to be set on first processing update")
	}
	firstStart := first.Start

	if err := server.JobsUpdateState(task, "processing", JobStateRunning, 0); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	second := server.JobResults.Get(task)
	if second.Start != firstStart {
		t.Fatalf("expected repeated processing update to preserve Start=%d, got %d", firstStart, second.Start)
	}
	if second.End != 0 {
		t.Fatalf("expected End=0 while running, got %d", second.End)
	}

	if err := server.JobsUpdateState(task, "completed", JobStateSuccess, 1); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	finished := server.JobResults.Get(task)
	if finished.End == 0 {
		t.Fatal("expected End to be set on terminal update")
	}

	time.Sleep(1100 * time.Millisecond)
	if err := server.JobsUpdateState(task, "processing", JobStateRunning, 0); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	rerun := server.JobResults.Get(task)
	if rerun.Start <= finished.End {
		t.Fatalf("expected rerun Start (%d) to be refreshed after previous End (%d)", rerun.Start, finished.End)
	}
	if rerun.End != 0 {
		t.Fatalf("expected rerun End to be cleared, got %d", rerun.End)
	}
}

func TestJobsUpdateState_APIMode_TerminalStatesSetEnd(t *testing.T) {
	server := newTestServerForAPIJobs(t)

	if err := server.JobsUpdateState("optimize", "completed", JobStateSuccess, 1); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	if got := server.JobResults.Get("optimize"); got.End == 0 || got.State != JobStateSuccess || got.Done != 1 {
		t.Fatalf("expected success end timestamp, got %+v", got)
	}

	if err := server.JobsUpdateState("restart", "boom", JobStateErrorExec, 1); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	if got := server.JobResults.Get("restart"); got.End == 0 || got.State != JobStateErrorExec || got.Done != 1 || got.Result != "boom" {
		t.Fatalf("expected error end timestamp, got %+v", got)
	}
}

func TestJobInsertTask_APIMode_RejectsRescheduleWhileInProgress(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	for _, task := range []string{string(config.ConstTaskOptimize), string(config.ConstTaskDump)} {
		if _, err := server.JobInsertTask(task, "0", "monitor-host"); err != nil {
			t.Fatalf("first JobInsertTask(%s) returned unexpected error: %v", task, err)
		}
		first := server.JobResults.Get(task)
		if first == nil || first.Done != 0 {
			t.Fatalf("expected task %s to be in progress after first schedule, got %+v", task, first)
		}
		firstStart := first.Start

		if _, err := server.JobInsertTask(task, "0", "monitor-host"); err == nil {
			t.Fatalf("expected second JobInsertTask(%s) while in progress to return an error", task)
		}

		second := server.JobResults.Get(task)
		if second.Start != firstStart || second.Done != 0 {
			t.Fatalf("expected task %s to remain unchanged in progress, got %+v", task, second)
		}

		if err := server.JobsUpdateState(task, "completed", JobStateSuccess, 1); err != nil {
			t.Fatalf("JobsUpdateState(%s) returned unexpected error: %v", task, err)
		}
	}
}
