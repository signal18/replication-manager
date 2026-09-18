// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Tests for JobReseedMysqldump's classify/replay unification with
// JobRejoinMysqldumpFromSource's live-stream model (see
// doc/implementation/cluster/SYSTEM_ALL_RESEED_IMPLEMENTATION_STATUS.md).
// JobReseedMysqldump itself spawns a real mysql subprocess, so these tests
// exercise its extracted, subprocess-free pieces directly: the main-dump
// pump (runReseedMysqldumpPump), the mysql.users.sql.gz sidecar fallback
// classify step (classifyReseedMysqldumpUserSidecar -- restore-user=true and
// the main dump has no mysql.system-all content, the only case the sidecar
// is ever consulted), the stage-attribution helper
// (reseedMysqldumpFailureMessage), and the classify->publish->restoreSystemCatalog
// chain via sqlmock. There is exactly one system/user replay authority per
// restore: phase one never injects the sidecar directly, and the sidecar is
// never consulted when the main dump already carried system content.

package cluster

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

// writeGzipSidecarFile writes content, gzip-compressed, to dir/name -- used
// to fabricate a mysql.users.sql.gz sidecar for classifyReseedMysqldumpUserSidecar
// tests. Standard library gzip output is read back fine by the pgzip reader
// ReadMysqldumpUser uses in production; both are plain RFC 1952 gzip.
func writeGzipSidecarFile(t *testing.T, dir, name, content string) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	defer f.Close()
	gw := gzip.NewWriter(f)
	if _, err := gw.Write([]byte(content)); err != nil {
		t.Fatalf("write gzip content for %s: %v", name, err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("close gzip writer for %s: %v", name, err)
	}
}

// TestReseedMysqldumpSystemReplaySource is a pure-function table test of
// JobReseedMysqldump's phase-two branch decision, covering the full
// restore-user x main-dump-has-system-content matrix -- including the two
// single-authority guarantees that are otherwise only reachable by spawning a
// real mysql client: restore-user=false always skips replay even when the
// main dump carried mysql.system-all content (it must not leak through just
// because content happened to be present), and an inline mysql.system-all
// match always wins over the sidecar (reseedMysqldumpSystemSourceMainDump,
// never Sidecar, when both could theoretically apply -- so the sidecar is
// never even opened in that case, ruling out a second, concurrent source).
func TestReseedMysqldumpSystemReplaySource(t *testing.T) {
	cases := []struct {
		name                     string
		restoreUser              bool
		mainDumpHasSystemContent bool
		want                     reseedMysqldumpSystemSource
	}{
		{"restore-user disabled, no inline system content", false, false, reseedMysqldumpSystemSourceNone},
		{"restore-user disabled, inline system content present: still skipped, not leaked", false, true, reseedMysqldumpSystemSourceNone},
		{"restore-user enabled, inline system content present: main dump wins over sidecar", true, true, reseedMysqldumpSystemSourceMainDump},
		{"restore-user enabled, no inline system content: falls back to sidecar", true, false, reseedMysqldumpSystemSourceSidecar},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := reseedMysqldumpSystemReplaySource(c.restoreUser, c.mainDumpHasSystemContent)
			if got != c.want {
				t.Errorf("reseedMysqldumpSystemReplaySource(%v, %v) = %v, want %v", c.restoreUser, c.mainDumpHasSystemContent, got, c.want)
			}
		})
	}
}

// newMysqldumpReseedTestServer mirrors newArtifactTestServer (srv_job_reseed_test.go)
// and newSystemReseedTestServer (srv_job_backup_system_reseed_test.go) combined:
// a server with a real temp WorkingDir (for the artifact writer) plus a
// sqlmock-backed connection (for restoreSystemCatalog).
func newMysqldumpReseedTestServer(t *testing.T, workingDir string) (*ServerMonitor, sqlmock.Sqlmock, *sqlx.Conn, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	sqlxdb := sqlx.NewDb(db, "sqlmock")
	conn, err := sqlxdb.Connx(context.Background())
	if err != nil {
		t.Fatalf("failed to acquire sqlmock conn: %v", err)
	}
	server := &ServerMonitor{
		ClusterGroup: &Cluster{Name: "testcluster", Conf: &config.Config{WorkingDir: workingDir}},
		Host:         "10.0.0.1",
		Port:         "3306",
		URL:          "10.0.0.1:3306",
		DBVersion:    &version.Version{Flavor: "MariaDB", Major: 10, Minor: 11},
	}
	return server, mock, conn, func() {
		conn.Close()
		db.Close()
	}
}

// TestReseedMysqldumpPumpNoSystemContentDiscardsArtifact covers the common
// case: a dump with no mysql.system-all content goes through the pump
// untouched and produces an empty artifact that the caller discards -- no
// "is this a --system=all dump" pre-check gates this, the classify result
// alone decides.
func TestReseedMysqldumpPumpNoSystemContentDiscardsArtifact(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	dump := "USE `db`;\nCREATE TABLE `t` (id int);\nINSERT INTO `t` VALUES (1);\n"

	artifactWriter, err := server.newDirectReseedSystemArtifactWriter("mysqldump-nosys", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}

	var app bytes.Buffer
	result, pumpErr, fromClassify := runReseedMysqldumpPump(&app, "RESET MASTER;", strings.NewReader(dump), artifactWriter)
	if pumpErr != nil {
		t.Fatalf("unexpected pump error: %v", pumpErr)
	}
	if fromClassify {
		t.Error("fromClassify must be false on success")
	}
	if result.HasSystemContent {
		t.Fatal("expected HasSystemContent=false for a dump with no system-catalogue content")
	}

	wantApp := "RESET MASTER;" + dump
	if app.String() != wantApp {
		t.Errorf("application output:\n got:  %q\n want: %q", app.String(), wantApp)
	}

	tmpDir := artifactWriter.tmpDir
	artifactWriter.discard()
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("expected artifact temp dir removed after discard, got err=%v", err)
	}
}

// TestReseedMysqldumpPumpSystemAllRoutesAndPublishes proves the full
// pump -> publish -> restoreSystemCatalog chain for a --system=all-style
// dump without spawning a real mysql binary: application SQL and system SQL
// are correctly separated by the pump, the system half publishes as a valid
// artifact, and restoreSystemCatalog replays it via the same narrow phase-two
// path JobRejoinMysqldumpFromSource and RetryDirectReseedSystemCatalog use.
func TestReseedMysqldumpPumpSystemAllRoutesAndPublishes(t *testing.T) {
	server, mock, conn, closeFn := newMysqldumpReseedTestServer(t, t.TempDir())
	defer closeFn()

	dump := strings.Join([]string{
		"-- MySQL dump header",
		"USE `db`;",
		"CREATE TABLE `t` (id int);",
		"INSERT INTO `t` VALUES (1);",
		"INSTALL PLUGIN disk SONAME 'disk.so';",
		"CREATE USER 'x'@'y';",
	}, "\n") + "\n"

	artifactWriter, err := server.newDirectReseedSystemArtifactWriter("mysqldump-sysall", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}

	var app bytes.Buffer
	result, pumpErr, fromClassify := runReseedMysqldumpPump(&app, "RESET MASTER;SET sql_log_bin=0;", strings.NewReader(dump), artifactWriter)
	if pumpErr != nil {
		t.Fatalf("unexpected pump error: %v", pumpErr)
	}
	if fromClassify {
		t.Error("fromClassify must be false on success")
	}
	if !result.HasSystemContent {
		t.Fatal("expected HasSystemContent=true")
	}
	wantApp := "RESET MASTER;SET sql_log_bin=0;-- MySQL dump header\nUSE `db`;\nCREATE TABLE `t` (id int);\nINSERT INTO `t` VALUES (1);\n"
	if app.String() != wantApp {
		t.Errorf("application output:\n got:  %q\n want: %q", app.String(), wantApp)
	}

	finalDir, err := artifactWriter.publish(result.Metadata, directReseedArtifactExtra{
		SourceServer:          "file:/tmp/backup.sql.gz",
		DestinationServer:     server.URL,
		DestinationFamily:     server.DBVersion.Flavor,
		DestinationMajorMinor: directReseedServerMajorMinor(server.DBVersion),
		BoundaryFormat:        "v1-eof-bounded",
		ArtifactState:         directReseedArtifactStatePublished,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	expectPrepareRestoreConn(mock)
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(pluginShowRows())
	mock.ExpectExec("INSTALL PLUGIN disk SONAME 'disk.so'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnResult(sqlmock.NewResult(0, 0))

	progressed, replayErr := server.restoreSystemCatalog(context.Background(), conn, filepath.Join(finalDir, directReseedSystemArtifactName))
	if replayErr != nil {
		t.Fatalf("restoreSystemCatalog: %v", replayErr)
	}
	if !progressed {
		t.Error("expected progressed=true after statements executed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// TestClassifyReseedMysqldumpUserSidecarMissingIsNotExist covers the common
// case for backups not taken with backup-split-mysql-user (or, per the
// unified contract, MySQL/Percona dumps that already carried their own
// mysql.system-all content so the sidecar was never consulted): no
// mysql.users.sql.gz next to backupfile is reported as an os.ErrNotExist-
// wrapping error, not silently ok=true with an empty result, so callers can
// tell "nothing to do" apart from "found it, nothing in it" (the next test).
func TestClassifyReseedMysqldumpUserSidecarMissingIsNotExist(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "backup.sql.gz")

	var sys bytes.Buffer
	result, ok, err := server.classifyReseedMysqldumpUserSidecar(backupfile, &sys)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected an os.ErrNotExist-wrapping error, got: %v", err)
	}
	if ok {
		t.Error("expected ok=false when the sidecar file is missing")
	}
	if result.HasSystemContent {
		t.Error("expected HasSystemContent=false on the zero result")
	}
	if sys.Len() != 0 {
		t.Errorf("expected nothing written to systemWriter, got %d bytes", sys.Len())
	}
}

// TestClassifyReseedMysqldumpUserSidecarNoSystemContent covers a
// mysql.users.sql.gz sidecar that exists but, once classified, turns out to
// carry no actual CREATE USER/GRANT/INSTALL PLUGIN content (e.g. an empty or
// header-only --system=user dump) -- ok=false here, same as a missing
// sidecar, since there is still nothing to replay.
func TestClassifyReseedMysqldumpUserSidecarNoSystemContent(t *testing.T) {
	server := newArtifactTestServer(t, t.TempDir())
	dir := t.TempDir()
	backupfile := filepath.Join(dir, "backup.sql.gz")
	writeGzipSidecarFile(t, dir, "mysql.users.sql.gz", "-- mysqldump header\nSET NAMES utf8;\n")

	var sys bytes.Buffer
	result, ok, err := server.classifyReseedMysqldumpUserSidecar(backupfile, &sys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Error("expected ok=false when the sidecar has no system-catalogue content")
	}
	if result.HasSystemContent {
		t.Error("expected HasSystemContent=false")
	}
}

// TestClassifyReseedMysqldumpUserSidecarSystemContentPublishesAndReplays
// proves the fallback path end to end: classifyReseedMysqldumpUserSidecar
// correctly extracts CREATE USER/INSTALL PLUGIN content from the sidecar
// (discarding its non-system preamble, same as the main-dump classify pass),
// and the resulting artifact publishes and replays through the exact same
// restoreSystemCatalog path the main-dump artifact uses -- one shared
// replay mechanism regardless of which source produced the content.
func TestClassifyReseedMysqldumpUserSidecarSystemContentPublishesAndReplays(t *testing.T) {
	server, mock, conn, closeFn := newMysqldumpReseedTestServer(t, t.TempDir())
	defer closeFn()

	dir := t.TempDir()
	backupfile := filepath.Join(dir, "backup.sql.gz")
	writeGzipSidecarFile(t, dir, "mysql.users.sql.gz",
		"-- mysqldump header\nINSTALL PLUGIN disk SONAME 'disk.so';\nCREATE USER 'x'@'y';\n")

	artifactWriter, err := server.newDirectReseedSystemArtifactWriter("mysqldump-user-sysall", time.Now())
	if err != nil {
		t.Fatalf("newDirectReseedSystemArtifactWriter: %v", err)
	}

	result, ok, err := server.classifyReseedMysqldumpUserSidecar(backupfile, artifactWriter)
	if err != nil {
		t.Fatalf("classifyReseedMysqldumpUserSidecar: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true: sidecar carries INSTALL PLUGIN/CREATE USER content")
	}

	finalDir, err := artifactWriter.publish(result.Metadata, directReseedArtifactExtra{
		SourceServer:          "file:" + mysqldumpUserSidecarPath(backupfile),
		DestinationServer:     server.URL,
		DestinationFamily:     server.DBVersion.Flavor,
		DestinationMajorMinor: directReseedServerMajorMinor(server.DBVersion),
		BoundaryFormat:        "v1-eof-bounded",
		ArtifactState:         directReseedArtifactStatePublished,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	expectPrepareRestoreConn(mock)
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(pluginShowRows())
	mock.ExpectExec("INSTALL PLUGIN disk SONAME 'disk.so'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnResult(sqlmock.NewResult(0, 0))

	progressed, replayErr := server.restoreSystemCatalog(context.Background(), conn, filepath.Join(finalDir, directReseedSystemArtifactName))
	if replayErr != nil {
		t.Fatalf("restoreSystemCatalog: %v", replayErr)
	}
	if !progressed {
		t.Error("expected progressed=true after statements executed")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// TestReseedMysqldumpFailureStageAttribution is a pure-function table test of
// reseedMysqldumpFailureMessage's stage-selection logic. Unlike
// reseedFailureMessage (JobRejoinMysqldumpFromSource's sibling, which
// arbitrates between two concurrent racing subprocesses), there is only one
// subprocess here, so a nonzero client exit is always authoritative even when
// the pump also errored (almost always collateral: a broken pipe from
// writing into a stdin the client already closed by dying).
func TestReseedMysqldumpFailureStageAttribution(t *testing.T) {
	cases := []struct {
		name         string
		clientErr    error
		pumpErr      error
		fromClassify bool
		wantStage    reseedStage
	}{
		{"client error only", errors.New("mysql exited 1"), nil, false, reseedStageApplicationRestore},
		{"client error wins over collateral pump error", errors.New("mysql exited 1"), errors.New("write: broken pipe"), false, reseedStageApplicationRestore},
		{"pump error before classify starts", nil, errors.New("writing restore preamble to mysql client stdin: broken pipe"), false, reseedStageApplicationRestore},
		{"pump error from classify itself", nil, errors.New("classifying mysqldump output into application/system SQL: bufio.Scanner: token too long"), true, reseedStageSystemExtraction},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg := reseedMysqldumpFailureMessage("10.0.0.1:3306", c.clientErr, c.pumpErr, c.fromClassify, nil)
			wantPrefix := string(c.wantStage) + ":"
			if !strings.HasPrefix(msg, wantPrefix) {
				t.Errorf("reseedMysqldumpFailureMessage() = %q, want prefix %q", msg, wantPrefix)
			}
		})
	}
}

// TestReseedMysqldumpFailureMessageIncludesClientTail confirms a bounded
// mysql-client stderr tail is folded into the message, so a caller isn't left
// with just "exit status 1" and no diagnostic content.
func TestReseedMysqldumpFailureMessageIncludesClientTail(t *testing.T) {
	msg := reseedMysqldumpFailureMessage("10.0.0.1:3306", errors.New("exit status 1"), nil, false,
		[]string{"ERROR 1045 (28000)", "Access denied for user"})
	if !strings.Contains(msg, "ERROR 1045 (28000)") || !strings.Contains(msg, "Access denied for user") {
		t.Errorf("expected client stderr tail folded into message, got: %q", msg)
	}
}

// TestReseedMysqldumpSystemReplayConnSlaveDisablesBinlog covers the common
// case: reseeding a slave (sqlLogBin==0, matching phase one's own
// SET sql_log_bin=0 preamble) must replay the system-catalogue artifact on a
// connection with binlog explicitly disabled, same as GetConnNoBinlog does
// for every other slave-reseed connection in this codebase.
func TestReseedMysqldumpSystemReplayConnSlaveDisablesBinlog(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")
	server := &ServerMonitor{ClusterGroup: &Cluster{Name: "test", Conf: &config.Config{}}}

	mock.ExpectExec("set session sql_log_bin=0").WillReturnResult(sqlmock.NewResult(0, 0))

	conn, err := server.reseedMysqldumpSystemReplayConn(context.Background(), sqlxdb, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer conn.Close()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}

// TestReseedMysqldumpSystemReplayConnMasterKeepsBinlogOn covers restoring
// the master itself (sqlLogBin==1, server.URL == cluster master, matching
// phase one's own SET sql_log_bin=1 preamble): the system-catalogue replay
// connection must leave binlog ON so replicas still receive the replayed
// statements, matching restoreSplitdumpWithMysql's identical branching. No
// sqlmock expectation is set for a sql_log_bin statement -- sqlmock fails any
// unexpected call by default, so a nil error here is itself proof no such
// statement was issued.
func TestReseedMysqldumpSystemReplayConnMasterKeepsBinlogOn(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to create sqlmock: %v", err)
	}
	defer db.Close()
	sqlxdb := sqlx.NewDb(db, "sqlmock")
	server := &ServerMonitor{ClusterGroup: &Cluster{Name: "test", Conf: &config.Config{}}}

	conn, err := server.reseedMysqldumpSystemReplayConn(context.Background(), sqlxdb, 1)
	if err != nil {
		t.Fatalf("unexpected error (a sql_log_bin statement would have been unexpected by sqlmock): %v", err)
	}
	defer conn.Close()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
}
