// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

// Tests for the narrow, live-lookup-driven INSTALL PLUGIN replay policy that
// replaces the old blanket continueOnError=true bypass for mysql.system-all
// (doc/implementation/cluster/SYSTEM_ALL_RESEED_FIX_PLAN.md).

package cluster

import (
	"compress/gzip"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/version"
)

func TestIsInstallPluginStatement(t *testing.T) {
	cases := []struct {
		stmt     string
		wantName string
		wantOK   bool
	}{
		{"INSTALL PLUGIN metadata_lock_info SONAME 'metadata_lock_info.so'", "metadata_lock_info", true},
		{"install plugin metadata_lock_info soname 'metadata_lock_info.so'", "metadata_lock_info", true},
		{"INSTALL PLUGIN `disk` SONAME 'disk.so'", "disk", true},
		{"INSTALL PLUGIN \"disk\" SONAME 'disk.so'", "disk", true},
		{"CREATE USER 'x'@'y'", "", false},
		{"INSERT INTO mysql.plugin VALUES (1)", "", false},
		{"  INSTALL PLUGIN clone SONAME 'mysql_clone.so'", "clone", true},
	}
	for _, c := range cases {
		name, ok := isInstallPluginStatement(c.stmt)
		if ok != c.wantOK || name != c.wantName {
			t.Errorf("isInstallPluginStatement(%q) = (%q, %v), want (%q, %v)", c.stmt, name, ok, c.wantName, c.wantOK)
		}
	}
}

func TestIsCreateUserStatement(t *testing.T) {
	cases := []struct {
		stmt        string
		want        string
		wantAccount string
		wantHost    string
		wantOK      bool
	}{
		{"CREATE USER 'mariadb.sys'@'localhost' IDENTIFIED VIA mysql_native_password USING 'x'",
			"ALTER USER 'mariadb.sys'@'localhost' IDENTIFIED VIA mysql_native_password USING 'x'", "mariadb.sys", "localhost", true},
		{"create user 'x'@'y'", "ALTER USER 'x'@'y'", "x", "y", true},
		{"CREATE USER `mysql.sys`@`localhost`", "ALTER USER `mysql.sys`@`localhost`", "mysql.sys", "localhost", true},
		{"CREATE USER 'noHostGiven'", "ALTER USER 'noHostGiven'", "noHostGiven", "%", true},
		{"CREATE USER bareNoHostAtAll", "ALTER USER bareNoHostAtAll", "bareNoHostAtAll", "%", true},
		{"CREATE USER 'o''brien'@'localhost'", "ALTER USER 'o''brien'@'localhost'", "o'brien", "localhost", true},
		{"CREATE USER IF NOT EXISTS 'x'@'y'", "", "", "", false},
		{"INSTALL PLUGIN disk SONAME 'disk.so'", "", "", "", false},
		{"CREATE OR REPLACE USER 'x'@'y'", "", "", "", false},
	}
	for _, c := range cases {
		got, account, host, ok := isCreateUserStatement(c.stmt)
		if ok != c.wantOK || got != c.want || account != c.wantAccount || host != c.wantHost {
			t.Errorf("isCreateUserStatement(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)", c.stmt, got, account, host, ok, c.want, c.wantAccount, c.wantHost, c.wantOK)
		}
	}
}

func TestIsKnownProtectedSystemAccount(t *testing.T) {
	cases := []struct {
		user string
		host string
		want bool
	}{
		{"mysql.sys", "localhost", true},
		{"mysql.session", "localhost", true},
		{"mysql.infoschema", "localhost", true},
		{"mariadb.sys", "localhost", true},
		{"mysql.sys", "LOCALHOST", true},
		{"MySQL.Sys", "localhost", false},
		{"mysql.sys", "%", false},
		{"mysql.sys", "10.0.0.%", false},
		{"direct_reseed_probe", "localhost", false},
		{"", "localhost", false},
	}
	for _, c := range cases {
		if got := isKnownProtectedSystemAccount(c.user, c.host); got != c.want {
			t.Errorf("isKnownProtectedSystemAccount(%q, %q) = %v, want %v", c.user, c.host, got, c.want)
		}
	}
}

func TestIsAccessDeniedError(t *testing.T) {
	if !isAccessDeniedError(&mysql.MySQLError{Number: 1227, Message: "Access denied; you need (at least one of) the SYSTEM_USER privilege(s)"}) {
		t.Error("expected ER_SPECIFIC_ACCESS_DENIED_ERROR (1227) to be recognized")
	}
	if isAccessDeniedError(&mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'x'@'y'"}) {
		t.Error("expected a different MySQL error number to not be recognized as access-denied")
	}
	if isAccessDeniedError(errors.New("some other error")) {
		t.Error("expected a non-MySQLError to not be recognized as access-denied")
	}
}

func TestIsCannotUserError(t *testing.T) {
	if !isCannotUserError(&mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'x'@'y'"}) {
		t.Error("expected ER_CANNOT_USER (1396) to be recognized")
	}
	if isCannotUserError(&mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"}) {
		t.Error("expected a different MySQL error number to not be recognized as ER_CANNOT_USER")
	}
	if isCannotUserError(errors.New("some other error")) {
		t.Error("expected a non-MySQLError to not be recognized as ER_CANNOT_USER")
	}
}

// TestExecSplitdumpSingleCreateUserFallsBackToAlterUser is the regression for
// "please allow all users to be restored correctly": a CREATE USER statement
// for an account that already exists on the destination (e.g. mariadb.sys, or
// any account pre-created there) must not abort the whole replay -- it must
// bring the existing account's definition in line with the backup via ALTER
// USER, rather than either failing outright or silently skipping the account.
func TestExecSplitdumpSingleCreateUserFallsBackToAlterUser(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectExec("CREATE USER 'mariadb.sys'@'localhost'").
		WillReturnError(&mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'mariadb.sys'@'localhost'"})
	mock.ExpectExec("ALTER USER 'mariadb.sys'@'localhost'").WillReturnResult(sqlmock.NewResult(0, 0))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'mariadb.sys'@'localhost'", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after the ALTER USER fallback committed")
	}
}

// TestExecSplitdumpSingleCreateUserIfNotExistsFatalOn1396 covers the case
// isCreateUserStatement deliberately excludes: a CREATE USER IF NOT EXISTS
// statement already tolerates a pre-existing account, so a 1396 here has some
// other cause and must propagate rather than be masked by an ALTER USER retry.
func TestExecSplitdumpSingleCreateUserIfNotExistsFatalOn1396(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectExec("CREATE USER IF NOT EXISTS 'x'@'y'").
		WillReturnError(&mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'x'@'y'"})

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER IF NOT EXISTS 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected the error to propagate for CREATE USER IF NOT EXISTS")
	}
	if progressed.Load() {
		t.Error("a failed exec must not mark progress")
	}
}

// TestExecSplitdumpSingleCreateUserAlterFallbackAlsoFailsReportsBothErrors
// verifies that when even the ALTER USER fallback fails, execSplitdumpSingle
// reports BOTH the original ER_CANNOT_USER and the fallback's own error,
// rather than swallowing altErr and surfacing only the uninformative
// "already exists" -- an operator needs to see why the fallback didn't
// resolve it (privilege issue, version/flavor clause mismatch, etc.).
func TestExecSplitdumpSingleCreateUserAlterFallbackAlsoFailsReportsBothErrors(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	createErr := &mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'x'@'y'"}
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(createErr)
	mock.ExpectExec("ALTER USER 'x'@'y'").WillReturnError(errors.New("permission denied"))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected an error when the ALTER USER fallback also fails")
	}
	if !strings.Contains(err.Error(), "1396") {
		t.Errorf("expected the original CREATE USER error (1396) to still be present in %q", err.Error())
	}
	if !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("expected the ALTER USER fallback's own error to be present in %q, got lost instead of surfaced", err.Error())
	}
	var me *mysql.MySQLError
	if !errors.As(err, &me) || me.Number != 1396 {
		t.Errorf("expected errors.As to still reach the original *mysql.MySQLError(1396) through the wrapped error, got %v", err)
	}
	if progressed.Load() {
		t.Error("a failed fallback must not mark progress")
	}
}

// TestExecSplitdumpSingleCreateUserAlterFallbackRetriesOnTransientError
// verifies that a transient (lock-wait/deadlock) failure specifically on the
// ALTER USER fallback statement is retried like every other statement class
// in this function, not treated as an immediate permanent failure. The retry
// loop re-issues the original stmt each attempt, so a retry replays CREATE
// USER (which collides again, deterministically) before re-attempting the
// fallback.
func TestExecSplitdumpSingleCreateUserAlterFallbackRetriesOnTransientError(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	createErr := &mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'x'@'y'"}
	deadlock := &mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"}
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(createErr)
	mock.ExpectExec("ALTER USER 'x'@'y'").WillReturnError(deadlock)
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(createErr)
	mock.ExpectExec("ALTER USER 'x'@'y'").WillReturnResult(sqlmock.NewResult(0, 0))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked once the retried ALTER USER fallback committed")
	}
}

// TestExecSplitdumpSingleProtectedSystemAccountSkipped is the regression for
// MySQL 8's SYSTEM_USER-protected bootstrap accounts (mysql.sys and
// friends): they always pre-exist, so CREATE USER always hits ER_CANNOT_USER,
// and a replay connection without the SYSTEM_USER privilege can't ALTER USER
// them either (ER_SPECIFIC_ACCESS_DENIED_ERROR). Since the destination's own
// engine-provisioned copy is already correct, that specific, narrowly-gated
// combination must be a deliberate skip, not a fatal error.
func TestExecSplitdumpSingleProtectedSystemAccountSkipped(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	createErr := &mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'mysql.sys'@'localhost'"}
	accessDeniedErr := &mysql.MySQLError{Number: 1227, Message: "Access denied; you need (at least one of) the SYSTEM_USER privilege(s)"}
	mock.ExpectExec("CREATE USER `mysql.sys`@`localhost`").WillReturnError(createErr)
	mock.ExpectExec("ALTER USER `mysql.sys`@`localhost`").WillReturnError(accessDeniedErr)

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER `mysql.sys`@`localhost`", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if progressed.Load() {
		t.Error("a deliberately skipped protected-system-account CREATE USER must not mark progress -- nothing committed")
	}
}

// TestExecSplitdumpSingleAccessDeniedOnOrdinaryAccountIsFatal proves the skip
// above is narrowly gated on the known account allowlist, not on the
// access-denied error alone: an ordinary account hitting the same
// ER_SPECIFIC_ACCESS_DENIED_ERROR on the ALTER USER fallback (e.g. a real
// CREATE USER privilege misconfiguration on the replay connection) must still
// surface as a fatal error, not be silently swallowed.
func TestExecSplitdumpSingleAccessDeniedOnOrdinaryAccountIsFatal(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	createErr := &mysql.MySQLError{Number: 1396, Message: "Operation CREATE USER failed for 'app_user'@'%'"}
	accessDeniedErr := &mysql.MySQLError{Number: 1227, Message: "Access denied; you need (at least one of) the SYSTEM_USER privilege(s)"}
	mock.ExpectExec("CREATE USER 'app_user'@'%'").WillReturnError(createErr)
	mock.ExpectExec("ALTER USER 'app_user'@'%'").WillReturnError(accessDeniedErr)

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'app_user'@'%'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected the access-denied fallback failure to propagate for a non-protected account")
	}
	if progressed.Load() {
		t.Error("a failed fallback must not mark progress")
	}
}

func newSystemReseedTestServer(t *testing.T) (*ServerMonitor, sqlmock.Sqlmock, *sqlx.Conn, func()) {
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
		ClusterGroup: &Cluster{Name: "test", Conf: &config.Config{}},
		DBVersion:    &version.Version{Flavor: "MariaDB"},
	}
	return server, mock, conn, func() {
		conn.Close()
		db.Close()
	}
}

func pluginShowRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"Name", "Status", "Type", "Library", "License"})
}

func TestResolveInstallPluginSkipActive(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("disk", "ACTIVE", "INFORMATION SCHEMA", "disk.so", "GPL"),
	)

	skip, err := server.resolveInstallPluginSkip(context.Background(), conn, "disk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skip {
		t.Fatal("expected skip=true for an ACTIVE plugin")
	}
}

func TestResolveInstallPluginSkipAbsentExecutesNormally(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(pluginShowRows())

	skip, err := server.resolveInstallPluginSkip(context.Background(), conn, "disk")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Fatal("expected skip=false for an absent plugin")
	}
}

func TestResolveInstallPluginSkipPresentNotActiveIsFatal(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("disk", "DISABLED", "INFORMATION SCHEMA", "disk.so", "GPL"),
	)

	_, err := server.resolveInstallPluginSkip(context.Background(), conn, "disk")
	if err == nil {
		t.Fatal("expected a fatal error for a present-but-not-ACTIVE plugin")
	}
}

func TestResolveInstallPluginSkipAmbiguousIsFatal(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().
			AddRow("disk", "ACTIVE", "INFORMATION SCHEMA", "disk.so", "GPL").
			AddRow("disk", "ACTIVE", "INFORMATION SCHEMA", "disk.so", "GPL"),
	)

	_, err := server.resolveInstallPluginSkip(context.Background(), conn, "disk")
	if err == nil {
		t.Fatal("expected a fatal error for an ambiguous plugin lookup")
	}
}

func TestResolveInstallPluginSkipLookupErrorIsFatal(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnError(errors.New("connection reset"))

	_, err := server.resolveInstallPluginSkip(context.Background(), conn, "disk")
	if err == nil {
		t.Fatal("expected a fatal error when the lookup itself fails")
	}
}

func TestExecSplitdumpSingleSkipsActivePluginWithoutExecuting(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("disk", "ACTIVE", "INFORMATION SCHEMA", "disk.so", "GPL"),
	)
	// No ExpectExec for the INSTALL PLUGIN statement itself: if execSplitdumpSingle
	// executed it anyway, mock.ExpectationsWereMet would fail below on the
	// unexpected call, since sqlmock is strict-ordered by default.

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "INSTALL PLUGIN disk SONAME 'disk.so'", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if progressed.Load() {
		t.Error("a deliberately skipped INSTALL PLUGIN must not mark progress -- nothing was sent to the connection")
	}
}

func TestExecSplitdumpSingleExecutesAbsentPlugin(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(pluginShowRows())
	mock.ExpectExec("INSTALL PLUGIN disk SONAME 'disk.so'").WillReturnResult(sqlmock.NewResult(0, 0))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "INSTALL PLUGIN disk SONAME 'disk.so'", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after a real statement executed")
	}
}

func TestExecSplitdumpSingleFatalOnNonActivePlugin(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("disk", "DISABLED", "INFORMATION SCHEMA", "disk.so", "GPL"),
	)

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "INSTALL PLUGIN disk SONAME 'disk.so'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected a fatal error for a present-but-not-ACTIVE plugin")
	}
	if progressed.Load() {
		t.Error("a plugin-lookup fatal error must not mark progress -- nothing was sent to the connection")
	}
}

func TestExecSplitdumpSingleNonPluginFailureIsFatal(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(errors.New("access denied"))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected the non-plugin statement failure to propagate as fatal")
	}
	if progressed.Load() {
		t.Error("a failed exec must not mark progress")
	}
}

// TestExecSplitdumpSingleRetryLoopNeverSwallows exercises the retry loop's
// two exit paths, both of which must never return nil after an error: a
// retryable error keeps retrying, and once the context is cancelled the loop
// returns a non-nil error immediately. Cancellation is the fast proxy for
// full retry-budget exhaustion, which would otherwise need ~11s of real
// backoff to reach.
func TestExecSplitdumpSingleRetryLoopNeverSwallows(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	deadlock := &mysql.MySQLError{Number: 1213, Message: "Deadlock found when trying to get lock"}
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(deadlock)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first post-attempt select must hit ctx.Done()

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(ctx, conn, "CREATE USER 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err == nil {
		t.Fatal("expected a non-nil error from a cancelled retry loop, got nil")
	}
	if progressed.Load() {
		t.Error("a cancelled retry loop must not mark progress")
	}
}

func TestExecSplitdumpSingleSucceedsOnFirstTry(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnResult(sqlmock.NewResult(0, 0))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "CREATE USER 'x'@'y'", "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after a real statement executed")
	}
}

// writeGzipSQLArtifact writes content gzip-compressed to a fresh file under
// t.TempDir() and returns its path, for restoreSystemCatalog tests that need
// a real artifact file on disk (the connection itself is still sqlmock).
func writeGzipSQLArtifact(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mysql.system-all.sql.gz")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("failed to create artifact file: %v", err)
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(content)); err != nil {
		t.Fatalf("failed to write gzip content: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}
	return path
}

func expectPrepareRestoreConn(mock sqlmock.Sqlmock) {
	mock.ExpectExec("SET SESSION long_query_time=10").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET SESSION FOREIGN_KEY_CHECKS=0").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("SET SESSION UNIQUE_CHECKS=0").WillReturnResult(sqlmock.NewResult(0, 0))
}

// TestRestoreSystemCatalogProgressFalseWhenFirstStatementFails is the
// regression for the retry-safety mechanism itself: a failure before any
// system-catalogue statement commits must report progressed=false, so
// JobRejoinMysqldumpFromSource/RetryDirectReseedSystemCatalog can mark the
// artifact replay-failed-safe (retryable from the beginning) instead of
// replay-failed.
func TestRestoreSystemCatalogProgressFalseWhenFirstStatementFails(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	expectPrepareRestoreConn(mock)
	mock.ExpectExec("CREATE USER 'x'@'y'").WillReturnError(errors.New("access denied"))

	path := writeGzipSQLArtifact(t, "CREATE USER 'x'@'y';\n")
	progressed, err := server.restoreSystemCatalog(context.Background(), conn, path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if progressed {
		t.Error("expected progressed=false: the very first statement failed, nothing committed")
	}
}

// TestRestoreSystemCatalogProgressTrueAfterPartialCommit covers the case
// that's unsafe to retry blindly: once at least one statement has committed,
// a later statement's failure must report progressed=true, so the artifact
// is marked replay-failed (NOT safely
// retryable from the beginning) rather than replay-failed-safe.
func TestRestoreSystemCatalogProgressTrueAfterPartialCommit(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	expectPrepareRestoreConn(mock)
	mock.ExpectExec("CREATE USER 'first'@'%'").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("CREATE USER 'second'@'%'").WillReturnError(errors.New("duplicate user"))

	path := writeGzipSQLArtifact(t, "CREATE USER 'first'@'%';\nCREATE USER 'second'@'%';\n")
	progressed, err := server.restoreSystemCatalog(context.Background(), conn, path)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !progressed {
		t.Error("expected progressed=true: the first statement committed before the second failed")
	}
}
