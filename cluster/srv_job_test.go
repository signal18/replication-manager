package cluster

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
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
	cluster.StateMachine.Init()
	server := &ServerMonitor{
		Id:           "node1",
		ClusterGroup: cluster,
		Datadir:      tmp + "/node1_3306",
		Host:         "node1",
		Port:         "3306",
		URL:          "node1:3306",
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

// TestReconcileRestoredAPIJobs_UnfinishedBecomesTerminal simulates a restart
// mid-job: a restored JobResults entry with Done=0 (as ReloadSaveInfosVariables
// would leave it) must be converted to a terminal error state so the UI stops
// showing it as Running forever.
func TestReconcileRestoredAPIJobs_UnfinishedBecomesTerminal(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskOptimize)
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})

	server.ReconcileRestoredAPIJobs()

	got := server.JobResults.Get(task)
	if got == nil {
		t.Fatal("expected job entry to still exist")
	}
	if got.Done != 1 {
		t.Fatalf("expected Done=1, got %d", got.Done)
	}
	if got.State != JobStateErrorExec {
		t.Fatalf("expected State=JobStateErrorExec, got %d", got.State)
	}
	if got.End == 0 {
		t.Fatal("expected End to be stamped")
	}
	if got.Result == "" {
		t.Fatal("expected a Result message explaining the restart")
	}
}

// TestReconcileRestoredAPIJobs_CompletedUnchanged ensures reconciliation never
// touches a job that already finished before the restart.
func TestReconcileRestoredAPIJobs_CompletedUnchanged(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskOptimize)
	want := &config.Task{Task: task, State: JobStateSuccess, Done: 1, Start: 100, End: 200, Result: "completed"}
	server.JobResults.Set(task, want)

	server.ReconcileRestoredAPIJobs()

	got := server.JobResults.Get(task)
	if got.State != JobStateSuccess || got.Done != 1 || got.End != 200 || got.Result != "completed" {
		t.Fatalf("expected completed job to remain unchanged, got %+v", got)
	}
}

// TestReconcileRestoredAPIJobs_SQLModeNoop ensures this API-mode-only
// reconciliation never mutates jobs when the scheduler runs in SQL mode,
// which already has its own stale-job timeout in JobsCheckPending.
func TestReconcileRestoredAPIJobs_SQLModeNoop(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	server.ClusterGroup.Conf.SchedulerJobsMode = "sql"
	task := string(config.ConstTaskOptimize)
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})

	server.ReconcileRestoredAPIJobs()

	got := server.JobResults.Get(task)
	if got.Done != 0 || got.State != JobStateAvailable {
		t.Fatalf("expected SQL mode job to remain unchanged, got %+v", got)
	}
}

// TestReconcileRestoredAPIJobs_MultipleUnfinished checks every restored
// Done=0 task is reconciled, not just the first one encountered.
func TestReconcileRestoredAPIJobs_MultipleUnfinished(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	tasks := []string{string(config.ConstTaskOptimize), string(config.ConstTaskRestart), string(config.ConstTaskDump)}
	for _, task := range tasks {
		server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})
	}

	server.ReconcileRestoredAPIJobs()

	for _, task := range tasks {
		got := server.JobResults.Get(task)
		if got.Done != 1 || got.State != JobStateErrorExec {
			t.Fatalf("expected task %s to be reconciled, got %+v", task, got)
		}
	}
}

// TestReconcileRestoredAPIJobs_ClearsRemoteTaskCookie guards against a stale
// wait cookie surviving reconciliation: if left in place, dbjobs would still
// consume it via CheckTaskNeeded on its next poll and dispatch a task we
// just recorded as failed, contradicting the reconciled state.
func TestReconcileRestoredAPIJobs_ClearsRemoteTaskCookie(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskOptimize)
	if err := server.SetWaitOptimizeCookie(); err != nil {
		t.Fatalf("failed to seed optimize cookie: %v", err)
	}
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})

	server.ReconcileRestoredAPIJobs()

	if server.HasWaitOptimizeCookie() {
		t.Fatal("expected reconciled remote task's wait cookie to be removed")
	}
}

// TestReconcileRestoredAPIJobs_ClearsCookieDespiteCurrentOverride guards
// against gating cleanup on IsRemoteTask(): SchedulerJobsExecOverrides is
// re-parsed from config on every load and can differ from what was in effect
// when the cookie was originally set (e.g. an admin edited
// scheduler-jobs-exec-remote while repman was down). Whether the cookie
// exists is what actually drives dbjobs dispatch via CheckTaskNeeded(), so
// cleanup must not depend on the current override value.
func TestReconcileRestoredAPIJobs_ClearsCookieDespiteCurrentOverride(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskOptimize)
	if err := server.SetWaitOptimizeCookie(); err != nil {
		t.Fatalf("failed to seed optimize cookie: %v", err)
	}
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})

	// Force IsRemoteTask(optimize, ...) to false under the *current* config,
	// simulating an override change made while repman was restarting.
	server.ClusterGroup.Conf.SchedulerJobsExecOverrides = map[config.TaskName]bool{
		config.ConstTaskOptimize: false,
	}

	server.ReconcileRestoredAPIJobs()

	got := server.JobResults.Get(task)
	if got.Done != 1 || got.State != JobStateErrorExec {
		t.Fatalf("expected job to be reconciled, got %+v", got)
	}
	if server.HasWaitOptimizeCookie() {
		t.Fatal("expected stale cookie to be removed even though current override reports the task as local")
	}
}

// TestReconcileRestoredAPIJobs_LocalTaskCookieUntouched ensures reconciliation
// does not try to manage cookies for local-only tasks, which never have one.
func TestReconcileRestoredAPIJobs_LocalTaskCookieUntouched(t *testing.T) {
	server := newTestServerForAPIJobs(t)
	task := string(config.ConstTaskDump)
	server.JobResults.Set(task, &config.Task{Task: task, State: JobStateAvailable, Done: 0, Start: 100})

	server.ReconcileRestoredAPIJobs()

	got := server.JobResults.Get(task)
	if got.Done != 1 || got.State != JobStateErrorExec {
		t.Fatalf("expected local task to still be reconciled, got %+v", got)
	}
}

// newTestServerForRuntimeOnlyJobs builds a fixture for the non-API,
// scheduler-disabled case: SQL-identity mode, but MonitorScheduler=false, so
// JobsUpdateState is the only thing ever touching this task (JobInsertTask's
// SQL INSERT is skipped by the caller, e.g. cluster/srv_job_backup.go).
func newTestServerForRuntimeOnlyJobs(t *testing.T) *ServerMonitor {
	_, server := newTestRuntimeOnlyClusterServer(t, "testcluster", "node1", "3306")
	return server
}

func newTestRuntimeOnlyClusterServer(t *testing.T, clusterName, host, port string) (*Cluster, *ServerMonitor) {
	t.Helper()
	cluster := &Cluster{
		Conf: &config.Config{
			WorkingDir:        t.TempDir(),
			SchedulerJobsMode: "sql",
			MonitorScheduler:  false,
		},
		Name:         clusterName,
		StateMachine: &state.StateMachine{},
	}
	cluster.StateMachine.Init()
	cluster.StateMachine.Discovered = true
	cluster.VersionsMap = config.NewVersionsMap()
	server := newTestRuntimeOnlyServer(t, cluster, host, port)
	cluster.Servers = serverList{server}
	return cluster, server
}

// newTestRuntimeOnlyServer builds one more ServerMonitor sharing an already
// constructed runtime-only test cluster (see newTestRuntimeOnlyClusterServer),
// for tests that need more than one server on the same cluster (e.g. a direct
// reseed's source+dest pair). It does not append to cluster.Servers -- callers
// that need it in the list do that themselves.
//
// DBVersion is set to a real (non-nil) *version.Version because several
// methods it feeds into transitively (e.g. GetMysqlDumpOptions ->
// ServerMonitor.IsMariaDB -> *version.Version.GreaterEqual) are not
// nil-receiver-safe, unlike the *version.Version methods that do guard nil.
// JobResults/Variables must be the real map constructors, not left as nil:
// both wrap *sync.Map and panic on first use if nil, they don't just behave
// like an empty map.
func newTestRuntimeOnlyServer(t *testing.T, cluster *Cluster, host, port string) *ServerMonitor {
	t.Helper()
	server := &ServerMonitor{
		Id:           host,
		ClusterGroup: cluster,
		Datadir:      cluster.Conf.WorkingDir + "/" + host + "_" + port,
		Host:         host,
		Port:         port,
		URL:          host + ":" + port,
		DBVersion:    &version.Version{Flavor: "MariaDB", Major: 10, Minor: 5},
	}
	server.JobResults = config.NewTasksMap()
	server.Variables = config.NewStringsMap()
	if err := os.MkdirAll(server.Datadir, 0755); err != nil {
		t.Fatalf("failed to create server datadir: %v", err)
	}
	return server
}

// TestJobsUpdatePayloadRuntimeOnly_SchedulerEnabled_NeverTouchesSQL mirrors
// TestJobsUpdateStateRuntimeOnly_SchedulerEnabled_NeverTouchesSQL for the
// payload helper: JobReseedLogicalBackupPrepare calls this right after
// JobsUpdateStateRuntimeOnly, for the same task that never gets a DB row via
// JobInsertTask, so it must never fall through to the SQL UPDATE either.
func TestJobsUpdatePayloadRuntimeOnly_SchedulerEnabled_NeverTouchesSQL(t *testing.T) {
	server := newTestServerForRuntimeOnlyJobs(t)
	server.ClusterGroup.Conf.MonitorScheduler = true
	task := "reseedmysqldump"

	// No connection pool is configured; if this ever fell through to the SQL
	// section it would hit a nil server.Conn there. It doesn't panic or hang,
	// which is the observable proof it stayed fully in-memory.
	server.JobsUpdatePayloadRuntimeOnly(task, "some-payload")
	got := server.JobResults.Get(task)
	if got == nil || got.Payload != "some-payload" {
		t.Fatalf("expected payload to be set in the cache, got %+v", got)
	}
}

// TestJobsUpdateStateRuntimeOnly_SchedulerEnabled_NeverTouchesSQL guards
// against JobsUpdateStateRuntimeOnly falling through to the SQL UPDATE
// section when the scheduler happens to be enabled: its callers (e.g.
// ProcessReseedLogical) never call JobInsertTask, so there is no DB row
// regardless of MonitorScheduler, and reaching the SQL section would either
// run a no-op UPDATE against zero matching rows or, as here with no
// connection pool configured, return "No connection pool" -- a scheduler-only
// error a purely in-memory call must never surface.
func TestJobsUpdateStateRuntimeOnly_SchedulerEnabled_NeverTouchesSQL(t *testing.T) {
	server := newTestServerForRuntimeOnlyJobs(t)
	server.ClusterGroup.Conf.MonitorScheduler = true
	task := "reseedmysqldump"

	// No connection pool is configured; if this ever fell through to the SQL
	// section it would hit a nil server.Conn there. It doesn't panic or hang,
	// which is the observable proof it stayed fully in-memory.
	server.JobsUpdateStateRuntimeOnly(task, "processing", JobStateRunning, 0)
	running := server.JobResults.Get(task)
	if running == nil || running.Start == 0 {
		t.Fatal("expected Start to be stamped")
	}

	server.JobsUpdateStateRuntimeOnly(task, "Reseed completed", JobStateFinished, 1)
	finished := server.JobResults.Get(task)
	if finished.End == 0 {
		t.Fatal("expected End to be stamped on terminal update")
	}
}

// TestJobsUpdateStateRuntimeOnly_SchedulerDisabled_StampsTimestamps guards
// manual backup/reseed working outside the scheduler: cluster/srv_job_backup.go
// call sites that never call JobInsertTask (e.g. ProcessReseedLogical) use
// JobsUpdateStateRuntimeOnly, which must stamp Start/End itself since there is
// no DB row and, outside API mode, the SQL UPDATE never runs while
// MonitorScheduler is false.
func TestJobsUpdateStateRuntimeOnly_SchedulerDisabled_StampsTimestamps(t *testing.T) {
	server := newTestServerForRuntimeOnlyJobs(t)
	task := "mysqldump"

	server.JobsUpdateStateRuntimeOnly(task, "", JobStateRunning, 0)
	running := server.JobResults.Get(task)
	if running == nil || running.Start == 0 {
		t.Fatal("expected Start to be stamped when scheduler is disabled")
	}
	if running.End != 0 {
		t.Fatalf("expected End=0 while running, got %d", running.End)
	}

	server.JobsUpdateStateRuntimeOnly(task, "Backup completed", JobStateFinished, 1)
	finished := server.JobResults.Get(task)
	if finished.End == 0 {
		t.Fatal("expected End to be stamped on terminal update when scheduler is disabled")
	}
}

// TestJobsUpdateState_SchedulerDisabled_DoesNotStampTimestamps locks in the
// narrowed condition: plain JobsUpdateState must NOT infer "no DB row" from
// !MonitorScheduler alone, since some callers (JobServerStop, JobServerRestart,
// JobOptimize) call JobInsertTask unconditionally and do get a real row even
// with the scheduler disabled. Only JobsUpdateStateRuntimeOnly (used by call
// sites that skip JobInsertTask) should stamp in that case.
func TestJobsUpdateState_SchedulerDisabled_DoesNotStampTimestamps(t *testing.T) {
	server := newTestServerForRuntimeOnlyJobs(t)
	task := "restart"

	if err := server.JobsUpdateState(task, "", JobStateRunning, 0); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	running := server.JobResults.Get(task)
	if running == nil {
		t.Fatal("expected a JobResults entry")
	}
	if running.Start != 0 {
		t.Fatalf("expected Start to stay 0 (no DB row to fall back on here), got %d", running.Start)
	}

	if err := server.JobsUpdateState(task, "done", JobStateSuccess, 1); err != nil {
		t.Fatalf("JobsUpdateState returned unexpected error: %v", err)
	}
	finished := server.JobResults.Get(task)
	if finished.End != 0 {
		t.Fatalf("expected End to stay 0, got %d", finished.End)
	}
}

// TestProcessReseedLogical_UnsupportedType_DoesNotCreateRunningTask guards the
// srv_job_backup.go call site where the regression lived: unsupported logical
// restore types must fail before the function stamps an in-memory runtime-only
// task as running, or the jobs view gets stuck at processing forever.
func TestProcessReseedLogical_UnsupportedType_DoesNotCreateRunningTask(t *testing.T) {
	cluster, server := newTestRuntimeOnlyClusterServer(t, "reseed-cluster", "target", "3307")
	cluster.Conf.BackupLogicalType = "unsupported"
	cluster.master = &ServerMonitor{
		Id:           "master",
		Host:         "master",
		Port:         "3306",
		URL:          "master:3306",
		ClusterGroup: cluster,
	}

	task := "reseedunsupported"
	server.SetInReseedBackup(task)

	err := server.ProcessReseedLogical(task)
	if err == nil {
		t.Fatal("expected ProcessReseedLogical to reject unsupported logical reseed type")
	}
	if err.Error() != "Logical reseed backup type unsupported is not supported" {
		t.Fatalf("unexpected ProcessReseedLogical error: %v", err)
	}
	if got := server.JobResults.Get(task); got != nil {
		t.Fatalf("expected no runtime task entry for unsupported logical reseed type, got %+v", got)
	}
	if server.HasAnyReseedingState() {
		t.Fatalf("expected reseeding state to be cleared after failure, got %q", server.IsReseeding)
	}
}

// TestReseedFromParentCluster_UnsupportedType_DoesNotCreateRunningTask guards
// the cluster_staging.go call site where the regression lived: an unsupported
// parent logical backup type must not stamp a runtime-only task as processing
// before the dispatch switch rejects it.
func TestReseedFromParentCluster_UnsupportedType_DoesNotCreateRunningTask(t *testing.T) {
	parentCluster, parentServer := newTestRuntimeOnlyClusterServer(t, "parent", "parent-master", "3306")
	parentCluster.Conf.BackupLogicalType = "unsupported"
	parentCluster.master = parentServer
	if err := parentServer.SetBackupLogicalCookie(config.ConstBackupLogicalTypeMysqldump); err != nil {
		t.Fatalf("failed to set parent logical backup cookie: %v", err)
	}

	targetCluster, target := newTestRuntimeOnlyClusterServer(t, "child", "child-slave", "3307")
	targetCluster.master = nil // keep IsMaster() false so the unsupported-type path stays DB-free in this regression test

	_, err := targetCluster.ReseedFromParentCluster(parentCluster, target, "")
	if err == nil {
		t.Fatal("expected ReseedFromParentCluster to reject unsupported parent logical backup type")
	}
	if err.Error() != "Unknown backup type unsupported" {
		t.Fatalf("unexpected ReseedFromParentCluster error: %v", err)
	}
	if got := target.JobResults.Get("reseedunsupported"); got != nil {
		t.Fatalf("expected no runtime task entry for unsupported parent reseed type, got %+v", got)
	}
	if target.HasAnyReseedingState() {
		t.Fatalf("expected reseeding state to be cleared after failure, got %q", target.IsReseeding)
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

// writeFakeExecutable writes a shell script to dir/name and makes it
// executable, returning its path.
func writeFakeExecutable(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatalf("failed to write fake executable %s: %v", name, err)
	}
	return path
}

// TestJobRejoinMysqldumpFromSource_ClientFailureClearsReseed is the
// regression test for the bug this whole fix chain addresses: a restore-side
// (mysql client) failure must not leave the direct reseed stuck. Before the
// fix, dumpCmd.Wait() then clientCmd.Wait() ran sequentially, so a client that
// died first left mysqldump blocked forever on a full stdout pipe nobody was
// draining -- the function never returned, the deferred SetInReseedBackup("")
// never ran, and IsReseeding="direct" stayed set forever.
//
// The fake mysqldump here writes far more than one pipe buffer's worth of
// output in a tight loop with no pacing, so if nothing drains it (the bug),
// it blocks on write() almost immediately and would run for a very long time
// (bounded only by the loop's line count, effectively "forever" relative to
// this test's timeout). The fake mysql client exits immediately with a
// nonzero status and stderr output, simulating a SQL error abort.
//
// Verified against the pre-fix implementation: reverted to the sequential
// dumpCmd.Wait() then clientCmd.Wait() code, this test fails at the 5s
// timeout with "direct reseed is stuck" instead of passing in well under a
// second.
func TestJobRejoinMysqldumpFromSource_ClientFailureClearsReseed(t *testing.T) {
	cluster, source := newTestRuntimeOnlyClusterServer(t, "direct-reseed-test", "source", "3306")
	dest := newTestRuntimeOnlyServer(t, cluster, "dest", "3307")
	cluster.Servers = append(cluster.Servers, dest)
	binDir := t.TempDir()

	// Keeps writing to stdout past any reasonable OS pipe buffer size (64KiB
	// is typical on Linux) with no sleeps, so an undrained pipe blocks it
	// quickly rather than letting it exit "naturally" before the bug would
	// have a chance to manifest.
	fakeDump := writeFakeExecutable(t, binDir, "fakedump.sh", `#!/bin/sh
echo "-- fake dump header" >&2
i=0
while [ $i -lt 1000000 ]; do
  echo "INSERT INTO t VALUES ($i, 'padding-to-make-this-line-not-tiny');"
  i=$((i+1))
done
echo "-- fake dump finished without being cancelled" >&2
`)

	fakeClient := writeFakeExecutable(t, binDir, "fakeclient.sh", `#!/bin/sh
echo "ERROR 1064 (42000) at line 1: fake SQL syntax error" >&2
exit 1
`)

	cluster.Conf.BackupMysqldumpPath = fakeDump
	cluster.Conf.BackupMysqlclientPath = fakeClient

	dest.SetInReseedBackup("direct")

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- cluster.JobRejoinMysqldumpFromSource(source, dest)
	}()

	const timeout = 5 * time.Second
	var err error
	select {
	case err = <-resultCh:
		// good — returned within the timeout
	case <-time.After(timeout):
		t.Fatalf("JobRejoinMysqldumpFromSource did not return within %s -- direct reseed is stuck", timeout)
	}

	if err == nil {
		t.Fatal("expected JobRejoinMysqldumpFromSource to return an error when the mysql client fails")
	}

	if dest.HasAnyReseedingState() {
		t.Fatalf("expected reseeding state to clear after failure, got %q", dest.IsReseeding)
	}

	task := dest.JobResults.Get("direct")
	if task == nil {
		t.Fatal("expected a JobResults entry for task \"direct\"")
	}
	if task.Done != 1 {
		t.Fatalf("expected task \"direct\" to be marked done, got Done=%d", task.Done)
	}
	if task.State != JobStateErrorExec {
		t.Fatalf("expected task \"direct\" to be marked failed (state=%d), got state=%d (result=%q)",
			JobStateErrorExec, task.State, task.Result)
	}
}

// TestJobRejoinMysqldumpFromSource_StallClearsReseed is the regression test
// for the remaining hang vector no exit-based fix can catch: mysqldump and
// the mysql client both wedge WITHOUT exiting or erroring (e.g. a source-side
// lock wait, or a destination query that never returns). None of the
// dump/client Wait() goroutines and none of the pump's own error paths ever
// fire in that case -- only a stall watchdog watching byte progress through
// the pump can. The fake mysqldump here writes one line, then blocks forever
// instead of exiting; the fake mysql client just drains whatever it's given
// and blocks waiting for more, exactly like a real client sitting on a
// long-running statement -- neither side ever exits or errors on its own.
func TestJobRejoinMysqldumpFromSource_StallClearsReseed(t *testing.T) {
	cluster, source := newTestRuntimeOnlyClusterServer(t, "direct-reseed-stall-test", "source", "3306")
	dest := newTestRuntimeOnlyServer(t, cluster, "dest", "3307")
	cluster.Servers = append(cluster.Servers, dest)
	binDir := t.TempDir()

	// exec, not a plain call: sleep/cat are external binaries the shell would
	// otherwise fork as a CHILD. cancel() only sends SIGKILL to dumpCmd's own
	// Process (the shell's PID) -- killing the shell wouldn't touch an
	// orphaned grandchild still holding the pipe open, and this test would
	// hang the same way a real stuck subprocess is supposed to prove it
	// doesn't. `exec` replaces the shell's process image with sleep/cat in
	// place (same PID), so killing dumpCmd's/clientCmd's Process kills the
	// real blocked process directly, exactly like a real single-process
	// mysqldump/mysql client would be killed.
	fakeDump := writeFakeExecutable(t, binDir, "fakestalldump.sh", `#!/bin/sh
echo "-- fake dump header" >&2
echo "INSERT INTO t VALUES (1);"
# Stall: no more output, no exit -- simulates a wedged source-side query.
exec sleep 300
`)

	fakeClient := writeFakeExecutable(t, binDir, "fakestallclient.sh", `#!/bin/sh
# Simulate a client that stays alive and keeps waiting for more input,
# exactly like a real mysql client blocked on a long-running statement --
# never exits, never errors on its own.
exec cat >/dev/null
`)

	cluster.Conf.BackupMysqldumpPath = fakeDump
	cluster.Conf.BackupMysqlclientPath = fakeClient
	cluster.Conf.BackupWriteStallTimeout = 1 // seconds; small so the test stays fast

	dest.SetInReseedBackup("direct")

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- cluster.JobRejoinMysqldumpFromSource(source, dest)
	}()

	const timeout = 10 * time.Second
	var err error
	select {
	case err = <-resultCh:
		// good — returned within the timeout
	case <-time.After(timeout):
		t.Fatalf("JobRejoinMysqldumpFromSource did not return within %s -- stalled direct reseed is stuck", timeout)
	}

	if err == nil {
		t.Fatal("expected JobRejoinMysqldumpFromSource to return an error when the stream stalls")
	}
	if !strings.Contains(err.Error(), "stall") && !strings.Contains(err.Error(), "no bytes streamed") {
		t.Fatalf("expected a stall-specific error, got: %v", err)
	}

	if dest.HasAnyReseedingState() {
		t.Fatalf("expected reseeding state to clear after stall, got %q", dest.IsReseeding)
	}

	task := dest.JobResults.Get("direct")
	if task == nil {
		t.Fatal("expected a JobResults entry for task \"direct\"")
	}
	if task.Done != 1 {
		t.Fatalf("expected task \"direct\" to be marked done, got Done=%d", task.Done)
	}
	if task.State != JobStateErrorExec {
		t.Fatalf("expected task \"direct\" to be marked failed (state=%d), got state=%d (result=%q)",
			JobStateErrorExec, task.State, task.Result)
	}
}

// mustSignalExitError runs a throwaway subprocess that kills itself with sig
// and returns the resulting *exec.ExitError. This gives reseedFailureMessage
// tests a real, signal-terminated os.ProcessState to inspect -- the same
// kind wasCollateralKill/wasCollateralPipeClose parse via ExitError.Sys() --
// without needing to race the actual dump/client/pump pipeline to provoke
// one.
func mustSignalExitError(t *testing.T, sig syscall.Signal) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("kill -s %d $$", int(sig))).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected a *exec.ExitError from a self-signal with %v, got %T: %v", sig, err, err)
	}
	if ws, ok := exitErr.Sys().(syscall.WaitStatus); !ok || !ws.Signaled() || ws.Signal() != sig {
		t.Fatalf("expected process to have exited via signal %v, got %v", sig, exitErr)
	}
	return exitErr
}

// mustNonzeroExitError runs a throwaway subprocess that exits with a normal
// nonzero status -- a "genuine failure" exit, as opposed to the
// signal-terminated collateral-kill exits from mustSignalExitError.
func mustNonzeroExitError(t *testing.T) error {
	t.Helper()
	err := exec.Command("sh", "-c", "exit 1").Run()
	if err == nil {
		t.Fatal("expected `sh -c exit 1` to return an error")
	}
	return err
}

// TestReseedFailureMessage is the regression test for the error-attribution
// switch in reseedFailureMessage (extracted from
// JobRejoinMysqldumpFromSource): given real signal-terminated and
// normal-nonzero-exit errors, does the message blame the side that actually
// failed on its own, and stay silent about a side that only died as
// collateral from cancelling the shared context or from the pump closing
// mysqldump's stdout pipe early? Covers both collateral mechanisms
// (wasCollateralKill's SIGKILL and wasCollateralPipeClose's pump-gated
// SIGPIPE) plus the case a bare signal heuristic would get wrong: a dump-side
// SIGPIPE with no pump failure at all, which must be reported as genuine
// since nothing of ours could have closed the pipe.
func TestReseedFailureMessage(t *testing.T) {
	const sourceURL = "source:3306"
	const destURL = "dest:3307"

	dumpLabel := "mysqldump on " + sourceURL
	clientLabel := "mysql client on " + destURL

	tests := []struct {
		name         string
		dumpErr      error
		clientErr    error
		pumpErr      error
		wantContains []string
		wantExcludes []string
	}{
		{
			name:         "client fails on its own, dump clean",
			clientErr:    mustNonzeroExitError(t),
			wantContains: []string{clientLabel},
			wantExcludes: []string{dumpLabel},
		},
		{
			name:         "dump fails on its own, client clean",
			dumpErr:      mustNonzeroExitError(t),
			wantContains: []string{dumpLabel},
			wantExcludes: []string{clientLabel},
		},
		{
			name:         "client collaterally SIGKILLed by a pump failure, dump clean",
			clientErr:    mustSignalExitError(t, syscall.SIGKILL),
			pumpErr:      errors.New("boom"),
			wantContains: []string{"stdin pump"},
			wantExcludes: []string{clientLabel, dumpLabel},
		},
		{
			// Regression test: dump and pump both succeeded, so nothing in
			// this function ever called cancel(); a client SIGKILL here can
			// only be from an external actor (OOM killer, `kill -9`, ...),
			// never our own doing. Scoring it "collateral" purely because
			// SIGKILL matches our own cancel()'s signal produces an empty
			// "Reseed failed: " with no cause at all -- it must be reported
			// as genuine instead.
			name:         "client SIGKILLed with no dump or pump failure is genuine, not collateral",
			clientErr:    mustSignalExitError(t, syscall.SIGKILL),
			wantContains: []string{clientLabel},
			wantExcludes: []string{dumpLabel},
		},
		{
			// Mirrors the case above but with both sides SIGKILLed and no
			// pump error: neither SIGKILL can "explain" the other (each
			// looks collateral only by pointing at the other's equally
			// unexplained SIGKILL), so both must be reported as genuine
			// rather than mutually cancelling out into an empty message.
			name:         "both sides SIGKILLed with no pump failure are both genuine",
			dumpErr:      mustSignalExitError(t, syscall.SIGKILL),
			clientErr:    mustSignalExitError(t, syscall.SIGKILL),
			wantContains: []string{dumpLabel, clientLabel},
		},
		{
			name:         "dump collaterally SIGPIPEd by a pump failure, client clean",
			dumpErr:      mustSignalExitError(t, syscall.SIGPIPE),
			pumpErr:      errors.New("boom"),
			wantContains: []string{"stdin pump"},
			wantExcludes: []string{dumpLabel, clientLabel},
		},
		{
			name:         "dump SIGPIPE with no pump failure is genuine, not collateral",
			dumpErr:      mustSignalExitError(t, syscall.SIGPIPE),
			wantContains: []string{dumpLabel},
		},
		{
			name:         "both fail independently -- double fault reported together",
			dumpErr:      mustNonzeroExitError(t),
			clientErr:    mustNonzeroExitError(t),
			wantContains: []string{dumpLabel, clientLabel},
		},
		{
			name:         "dump collaterally SIGKILLed, client genuine, pump also surfaced",
			dumpErr:      mustSignalExitError(t, syscall.SIGKILL),
			clientErr:    mustNonzeroExitError(t),
			pumpErr:      errors.New("boom"),
			wantContains: []string{clientLabel, "stdin pump"},
			wantExcludes: []string{dumpLabel},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := reseedFailureMessage(sourceURL, destURL, tt.dumpErr, tt.clientErr, tt.pumpErr, nil, nil)
			for _, want := range tt.wantContains {
				if !strings.Contains(msg, want) {
					t.Errorf("expected message to contain %q, got: %s", want, msg)
				}
			}
			for _, exclude := range tt.wantExcludes {
				if strings.Contains(msg, exclude) {
					t.Errorf("expected message to NOT contain %q, got: %s", exclude, msg)
				}
			}
		})
	}
}
