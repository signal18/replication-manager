// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

// #2091: a rotation queues the binlog it closed, each once, bounded by the keep window
func TestQueueBinlogCopyOncePerBinlogBounded(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-queue", "db1", "3306")
	c.Conf.BackupBinlogsKeep = 3
	q := &server.binlogCopies
	q.running = true // no worker: the queue alone
	for _, b := range []string{"mysql-bin.000001", "mysql-bin.000002", "mysql-bin.000002", "mysql-bin.000003", "mysql-bin.000004"} {
		server.queueBinlogCopy(b)
	}
	if got, want := server.pendingBinlogCopies(), []string{"mysql-bin.000002", "mysql-bin.000003", "mysql-bin.000004"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("pending %v, want %v", got, want)
	}
	if got := server.oldestPendingBinlogCopy(); got != "mysql-bin.000002" {
		t.Fatalf("oldest pending %q", got)
	}
	if q.catchUp != "mysql-bin.000004" {
		t.Fatalf("catch-up after the copies: %q", q.catchUp)
	}
}

// #2091: the worker waits while a snapshot runs, with WARN0257, then copies the binlogs once
// each and stops
func TestBinlogCopyWorkerWaitsForSnapshot(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-worker", "db1", "3306")
	c.Conf.BackupBinlogsKeep = 10
	c.Conf.BinlogCopyMode = "none" // every copy refused at once: the worker's order alone
	server.BinaryLogFileOldest = "mysql-bin.000001"
	server.snapshotsRunning.Add(1)
	server.queueBinlogCopy("mysql-bin.000001")
	server.queueBinlogCopy("mysql-bin.000002")

	deadline := time.Now().Add(3 * time.Second)
	for {
		server.binlogCopies.mu.Lock()
		waiting := server.binlogCopies.waiting
		server.binlogCopies.mu.Unlock()
		if waiting != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the worker does not record what it waits for")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(server.pendingBinlogCopies()) != 2 {
		t.Fatalf("a copy ran during the snapshot: %v", server.pendingBinlogCopies())
	}
	c.checkBinlogCopyStates()
	if !hasOpenState(c, "WARN0257") {
		t.Fatal("WARN0257 not raised while the copies wait")
	}

	server.snapshotsRunning.Add(-1)
	deadline = time.Now().Add(5 * time.Second)
	for {
		server.binlogCopies.mu.Lock()
		running := server.binlogCopies.running
		server.binlogCopies.mu.Unlock()
		if !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the worker did not end: pending %v", server.pendingBinlogCopies())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if p := server.pendingBinlogCopies(); len(p) != 0 {
		t.Fatalf("copies left %v", p)
	}
}

// #2091: the keep removal keeps the last backup-binlogs-keep binlog copies, the one just
// rotated included: it ended before it, removing each copy right after it was made
func TestBinlogKeepWindowKeepsTheRotatedBinlog(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-keep", "db1", "3306")
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 2
	server.State = stateMaster
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000008", "binlog.000009", "binlog.000010"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for b, kept := range map[string]bool{"binlog.000008": false, "binlog.000009": true, "binlog.000010": true} {
		if fileExists(filepath.Join(dir, b)) != kept {
			t.Errorf("%s kept %v, want %v", b, !kept, kept)
		}
	}
}

// #2091: with the archive on, the keep removal removes a binlog copy only once a snapshot of the
// backup directory holds it; it keeps the others, with WARN0258, until one does
func TestKeepRemovalWaitsForASnapshot(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-held", "db1", "3306")
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 2
	c.Conf.BackupRestic = true
	server.State = stateMaster
	server.BinaryLogFile = "binlog.000011"
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000007", "binlog.000008", "binlog.000009", "binlog.000010"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := server.newestBinlogCopy(); got != "binlog.000010" {
		t.Fatalf("newest copy %q", got)
	}

	// no snapshot known (a restart): every copy beyond the window is kept
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"binlog.000007", "binlog.000008"} {
		if !fileExists(filepath.Join(dir, b)) {
			t.Fatalf("%s removed while in no snapshot", b)
		}
	}
	server.binlogCopies.mu.Lock()
	held, oldest, need := server.binlogCopies.held, server.binlogCopies.heldOldest, server.binlogCopies.needSnapshot
	server.binlogCopies.mu.Unlock()
	if held != 2 || oldest != "binlog.000007" || !need {
		t.Fatalf("held %d oldest %q need a snapshot %t", held, oldest, need)
	}
	c.checkBinlogCopyStates()
	if !hasOpenState(c, "WARN0258") {
		t.Fatal("WARN0258 not raised for the kept copies")
	}

	// a snapshot held the copies up to 000007: that one goes, 000008 stays
	server.recordBinlogsInSnapshot("binlog.000007")
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "binlog.000007")) || !fileExists(filepath.Join(dir, "binlog.000008")) {
		t.Fatal("only the copy a snapshot holds is removed")
	}

	// a snapshot holds them all: the window alone is left, the hold ends
	server.recordBinlogsInSnapshot("binlog.000010")
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for b, kept := range map[string]bool{"binlog.000008": false, "binlog.000009": true, "binlog.000010": true} {
		if fileExists(filepath.Join(dir, b)) != kept {
			t.Errorf("%s kept %v, want %v", b, !kept, kept)
		}
	}
	server.binlogCopies.mu.Lock()
	held, need = server.binlogCopies.held, server.binlogCopies.needSnapshot
	server.binlogCopies.mu.Unlock()
	if held != 0 || need {
		t.Fatalf("the hold did not end: held %d need %t", held, need)
	}
}

// #2091: the copy worker takes the binlog-only snapshot for the copies its keep removal held,
// then runs the keep removal again: here the snapshot fails (no restic manager), so the copies
// beyond the window are removed and the lost continuity tracked, the hold ends
func TestBinlogCopyWorkerSnapshotsHeldCopies(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-worker-held", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 2
	c.Conf.BackupRestic = true
	server.State = stateMaster
	server.BinaryLogFile = "binlog.000011"
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000008", "binlog.000009", "binlog.000010"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server.recordBinlogsInSnapshot("binlog.000007") // a backup's snapshot held the copies up to 000007
	server.requeueBinlogCatchUp("binlog.000010")

	deadline := time.Now().Add(5 * time.Second)
	for {
		q := &server.binlogCopies
		q.mu.Lock()
		running, snapshotting, held := q.running, q.snapshotting, q.held
		q.mu.Unlock()
		if !running && !snapshotting && held == 0 && !fileExists(filepath.Join(dir, "binlog.000008")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no binlog-only snapshot taken for the held copies: running %t snapshotting %t held %d", running, snapshotting, held)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(filepath.Join(dir, "binlog.000009")) || !fileExists(filepath.Join(dir, "binlog.000010")) {
		t.Fatal("the keep window was removed")
	}
	if g := c.BinlogGaps(); len(g) != 1 || g[0].From != "binlog.000008" || g[0].To != "binlog.000008" {
		t.Fatalf("gaps %+v", g)
	}
}

// #2091: with no archive, the keep removal does not wait for a snapshot
func TestKeepRemovalWithoutArchive(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-noarchive", "db1", "3306")
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 1
	server.State = stateMaster
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000001", "binlog.000002"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := server.JobBackupBinlogPurge("binlog.000002"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "binlog.000001")) {
		t.Fatal("kept with no archive to wait for")
	}
}

// #2091 F12: when the binlog-only snapshot is refused (here backup-archive-remove-original) or
// failed, the keep removal removes the copies anyway, and the lost continuity is a state kept
// across restarts until a backup of the server is in the archive
func TestKeepRemovalLosesBinlogsNotBackups(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-gap", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 2
	c.Conf.BackupRestic = true
	c.Conf.BackupArchiveRemoveOriginal = true
	server.State = stateMaster
	server.BinaryLogFile = "binlog.000011"
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000007", "binlog.000008", "binlog.000009", "binlog.000010"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	server.recordBinlogsInSnapshot("binlog.000006") // a backup's snapshot held the copies up to 000006
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for b, kept := range map[string]bool{"binlog.000007": false, "binlog.000008": false, "binlog.000009": true, "binlog.000010": true} {
		if fileExists(filepath.Join(dir, b)) != kept {
			t.Errorf("%s kept %v, want %v", b, !kept, kept)
		}
	}
	gaps := c.BinlogGaps()
	if len(gaps) != 1 || gaps[0].From != "binlog.000007" || gaps[0].To != "binlog.000008" || gaps[0].Server != server.URL {
		t.Fatalf("gaps %+v", gaps)
	}
	c.checkBinlogCopyStates()
	if !hasOpenState(c, "WARN0259") {
		t.Fatal("WARN0259 not raised for the lost continuity")
	}

	// a failed binlog-only snapshot: the copies it was taken for are removed, the gap grows
	c.Conf.BackupArchiveRemoveOriginal = false
	server.binlogCopies.snapshotFailed = "boom"
	if err := os.WriteFile(filepath.Join(dir, "binlog.000011"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := server.JobBackupBinlogPurge("binlog.000011"); err != nil {
		t.Fatal(err)
	}
	if fileExists(filepath.Join(dir, "binlog.000009")) {
		t.Fatal("kept after a failed binlog-only snapshot")
	}
	if g := c.BinlogGaps(); len(g) != 1 || g[0].To != "binlog.000009" {
		t.Fatalf("the gap does not grow: %+v", g)
	}
	// the failure is taken in once: the next copy beyond the window is kept for a snapshot again
	if r := server.binlogSnapshotRefusal(); r != "" {
		t.Fatalf("refused again: %s", r)
	}

	// read again by a new process
	c.binlogGaps = nil
	if g := c.BinlogGaps(); len(g) != 1 || g[0].From != "binlog.000007" {
		t.Fatalf("the gap is not kept across a restart: %+v", g)
	}
	c.clearBinlogGap(server.URL)
	if len(c.BinlogGaps()) != 0 {
		t.Fatal("a backup in the archive does not end the gap")
	}
}

// A binlog older than the server's oldest one was purged there: its copy can never be made.
func TestBinlogOlderThan(t *testing.T) {
	for _, c := range []struct {
		binlog, oldest string
		want           bool
	}{
		{"binlog.000034", "binlog.000036", true},     // purged before its copy
		{"binlog.000036", "binlog.000036", false},    // still the oldest on the server
		{"binlog.000037", "binlog.000036", false},    // still on the server
		{"binlog.000034", "", false},                 // the list was not read
		{"binlog.000034", "mysql-bin.000036", false}, // another sequence
		{"binlog", "binlog.000036", false},           // not a binlog name
	} {
		if got := binlogOlderThan(c.binlog, c.oldest); got != c.want {
			t.Errorf("binlogOlderThan(%q, %q) = %v, want %v", c.binlog, c.oldest, got, c.want)
		}
	}
}

// The server's binlog list is read at the copy failure: a binlog older than its oldest one is
// purged (a gap), one still listed is not; an unreadable list is no proof of a loss.
func TestBinlogPurgedOnServer(t *testing.T) {
	_, server := newTestRuntimeOnlyClusterServer(t, "binlog-purged", "db2", "3306")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server.Conn = sqlx.NewDb(db, "sqlmock")
	server.DBVersion = &version.Version{Flavor: "MariaDB", Major: 11, Minor: 8}
	logs := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"Log_name", "File_size"}).AddRow("binlog.000036", 1024).AddRow("binlog.000037", 512)
	}

	mock.ExpectQuery("SHOW BINARY LOGS").WillReturnRows(logs())
	if !server.binlogPurgedOnServer("binlog.000035") {
		t.Error("binlog.000035, older than the oldest binlog.000036: want purged")
	}
	mock.ExpectQuery("SHOW BINARY LOGS").WillReturnRows(logs())
	if server.binlogPurgedOnServer("binlog.000036") {
		t.Error("binlog.000036, still on the server: want not purged")
	}
	mock.ExpectQuery("SHOW BINARY LOGS").WillReturnError(sqlmock.ErrCancelled)
	if server.binlogPurgedOnServer("binlog.000035") {
		t.Error("the binlog list was not read: want not purged")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

// #2091: the newest binlog copy a snapshot held is kept across restarts; before any is recorded a
// copy beyond the keep window is kept, never counted as lost, even with the binlog-only snapshot
// refused (backup-archive-remove-original)
func TestKeepRemovalSnapshotKnownAcrossRestart(t *testing.T) {
	c, server := newTestRuntimeOnlyClusterServer(t, "binlog-restart", "db1", "3306")
	c.WorkingDir = t.TempDir()
	c.Conf.BackupBinlogs = true
	c.Conf.BackupBinlogsKeep = 2
	c.Conf.BackupRestic = true
	c.Conf.BackupArchiveRemoveOriginal = true
	server.State = stateMaster
	server.BinaryLogFile = "binlog.000011"
	c.master = server
	dir := server.GetMyBackupDirectory()
	for _, b := range []string{"binlog.000007", "binlog.000008", "binlog.000009", "binlog.000010"} {
		if err := os.WriteFile(filepath.Join(dir, b), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	// no snapshot recorded: every copy kept, no gap
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"binlog.000007", "binlog.000008"} {
		if !fileExists(filepath.Join(dir, b)) {
			t.Fatalf("%s removed while no snapshot is recorded", b)
		}
	}
	if g := c.BinlogGaps(); len(g) != 0 {
		t.Fatalf("a copy maybe in the archive counted as lost: %+v", g)
	}

	// a backup's snapshot held them up to 000008, then a restart: the copies it holds go, no gap
	server.recordBinlogsInSnapshot("binlog.000008")
	server.binlogCopies = binlogCopyQueue{}
	if err := server.JobBackupBinlogPurge("binlog.000010"); err != nil {
		t.Fatal(err)
	}
	for b, kept := range map[string]bool{"binlog.000007": false, "binlog.000008": false, "binlog.000009": true, "binlog.000010": true} {
		if fileExists(filepath.Join(dir, b)) != kept {
			t.Errorf("%s kept %v, want %v", b, !kept, kept)
		}
	}
	if g := c.BinlogGaps(); len(g) != 0 {
		t.Fatalf("copies in a snapshot counted as lost after a restart: %+v", g)
	}
}
