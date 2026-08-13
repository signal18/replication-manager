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
	"regexp"
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
		info, ok := isCreateUserStatement(c.stmt)
		if ok != c.wantOK || info.AlterUser != c.want || info.User != c.wantAccount || info.Host != c.wantHost {
			t.Errorf("isCreateUserStatement(%q) = (%q, %q, %q, %v), want (%q, %q, %q, %v)", c.stmt, info.AlterUser, info.User, info.Host, ok, c.want, c.wantAccount, c.wantHost, c.wantOK)
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

// TestResolveInstallPluginSkipNotInstalledExecutesNormally covers the
// production case behind this fix: SHOW PLUGINS can list a known plugin
// (e.g. QUERY_RESPONSE_TIME) with status "NOT INSTALLED" rather than simply
// omitting it. That must execute INSTALL PLUGIN normally, the same as
// PluginAbsent, not be treated as the fatal present-but-not-ACTIVE case.
func TestResolveInstallPluginSkipNotInstalledExecutesNormally(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("QUERY_RESPONSE_TIME", "NOT INSTALLED", "INFORMATION SCHEMA", "query_response_time.so", "GPL"),
	)

	skip, err := server.resolveInstallPluginSkip(context.Background(), conn, "QUERY_RESPONSE_TIME")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Fatal("expected skip=false for a NOT INSTALLED plugin")
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

// TestExecSplitdumpSingleExecutesNotInstalledPlugin reproduces the reported
// production failure: destination SHOW PLUGINS reports QUERY_RESPONSE_TIME as
// present with status "NOT INSTALLED" (not simply absent from the list). That
// must run INSTALL PLUGIN normally instead of aborting the reseed.
func TestExecSplitdumpSingleExecutesNotInstalledPlugin(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginShowRows().AddRow("QUERY_RESPONSE_TIME", "NOT INSTALLED", "INFORMATION SCHEMA", "query_response_time.so", "GPL"),
	)
	mock.ExpectExec("INSTALL PLUGIN QUERY_RESPONSE_TIME SONAME 'query_response_time.so'").WillReturnResult(sqlmock.NewResult(0, 0))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, "INSTALL PLUGIN QUERY_RESPONSE_TIME SONAME 'query_response_time.so'", "mysql.system-all.sql.gz", &progressed)
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

// TestExtractIdentifiedByPasswordHash covers the pure parser behind the
// hash-equivalence skip: only the classic `IDENTIFIED BY PASSWORD '<hash>'`
// form is recognized; every other auth clause (or no clause at all) reports
// ok=false rather than guessing.
func TestExtractIdentifiedByPasswordHash(t *testing.T) {
	cases := []struct {
		rest     string
		wantHash string
		wantOK   bool
	}{
		{"IDENTIFIED BY PASSWORD '*ABCDEF'", "*ABCDEF", true},
		{"   IDENTIFIED BY PASSWORD '*ABCDEF'", "*ABCDEF", true},
		{"identified by password '*abcdef'", "*abcdef", true},
		{"IDENTIFIED BY PASSWORD 'o''brien'", "o'brien", true},
		{"IDENTIFIED VIA mysql_native_password USING 'x'", "", false},
		{"IDENTIFIED WITH mysql_native_password AS '*ABCDEF'", "", false},
		{"", "", false},
		{"IDENTIFIED BY PASSWORDX '*ABCDEF'", "", false},
		{"WITH GRANT OPTION", "", false},
		{"IDENTIFIED BY PASSWORD ''", "", false},
	}
	for _, c := range cases {
		hash, ok := extractIdentifiedByPasswordHash(c.rest)
		if ok != c.wantOK || hash != c.wantHash {
			t.Errorf("extractIdentifiedByPasswordHash(%q) = (%q, %v), want (%q, %v)", c.rest, hash, ok, c.wantHash, c.wantOK)
		}
	}
}

// TestIsCreateUserStatementHashExtraction covers isCreateUserStatement's
// additional Hash/HashOK/AccountSpec/AfterHash fields, layered on top of the
// existing TestIsCreateUserStatement coverage of the ALTER USER
// rewrite/account parse. AccountSpec/AfterHash matter specifically because
// execSplitdumpSingle uses them to rebuild a password-stripped ALTER USER
// when other attributes accompany the password clause -- dropping the whole
// statement in that case would silently leave those attributes unreconciled.
func TestIsCreateUserStatementHashExtraction(t *testing.T) {
	cases := []struct {
		stmt            string
		wantHash        string
		wantHashOK      bool
		wantAccountSpec string
		wantAfterHash   string
	}{
		{"CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASH1'", "*HASH1", true, " 'root'@'localhost'", ""},
		{"CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASH1' WITH MAX_USER_CONNECTIONS 5", "*HASH1", true, " 'root'@'localhost'", " WITH MAX_USER_CONNECTIONS 5"},
		{"CREATE USER 'mariadb.sys'@'localhost' IDENTIFIED VIA mysql_native_password USING 'x'", "", false, "", ""},
		{"CREATE USER 'x'@'y'", "", false, "", ""},
	}
	for _, c := range cases {
		info, ok := isCreateUserStatement(c.stmt)
		if !ok {
			t.Fatalf("isCreateUserStatement(%q) unexpectedly reported ok=false", c.stmt)
		}
		if info.HashOK != c.wantHashOK || info.Hash != c.wantHash {
			t.Errorf("isCreateUserStatement(%q) hash = (%q, %v), want (%q, %v)", c.stmt, info.Hash, info.HashOK, c.wantHash, c.wantHashOK)
		}
		if info.HashOK {
			if info.AccountSpec != c.wantAccountSpec {
				t.Errorf("isCreateUserStatement(%q) AccountSpec = %q, want %q", c.stmt, info.AccountSpec, c.wantAccountSpec)
			}
			if info.AfterHash != c.wantAfterHash {
				t.Errorf("isCreateUserStatement(%q) AfterHash = %q, want %q", c.stmt, info.AfterHash, c.wantAfterHash)
			}
		}
	}
}

// TestIsGrantWithIdentifiedByPassword covers the GRANT clause-stripping
// parser: only the fixed, single-account shape is recognized, and it must
// never guess -- multiple accounts, a REQUIRE clause, or no IDENTIFIED BY
// PASSWORD clause at all must all report ok=false so the caller executes the
// statement unmodified.
func TestIsGrantWithIdentifiedByPassword(t *testing.T) {
	cases := []struct {
		name          string
		stmt          string
		wantRewritten string
		wantUser      string
		wantHost      string
		wantHash      string
		wantOK        bool
	}{
		{
			name:          "with grant option preserved",
			stmt:          "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASH' WITH GRANT OPTION",
			wantRewritten: "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION",
			wantUser:      "root", wantHost: "localhost", wantHash: "*HASH", wantOK: true,
		},
		{
			name:          "no trailing clause",
			stmt:          "GRANT SELECT ON db.* TO 'app'@'%' IDENTIFIED BY PASSWORD '*H2'",
			wantRewritten: "GRANT SELECT ON db.* TO 'app'@'%'",
			wantUser:      "app", wantHost: "%", wantHash: "*H2", wantOK: true,
		},
		{
			name:   "multiple accounts bail out",
			stmt:   "GRANT ALL PRIVILEGES ON *.* TO 'a'@'h1', 'b'@'h2' IDENTIFIED BY PASSWORD '*H' WITH GRANT OPTION",
			wantOK: false,
		},
		{
			name:   "require clause bails out",
			stmt:   "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASH' REQUIRE NONE WITH GRANT OPTION",
			wantOK: false,
		},
		{
			name:   "no identified clause",
			stmt:   "GRANT PROXY ON ''@'%' TO 'root'@'localhost' WITH GRANT OPTION",
			wantOK: false,
		},
		{
			name:   "not a grant statement",
			stmt:   "CREATE USER 'x'@'y' IDENTIFIED BY PASSWORD '*H'",
			wantOK: false,
		},
		{
			name:   "no to clause",
			stmt:   "GRANT ALL PRIVILEGES ON *.*",
			wantOK: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rewritten, user, host, hash, ok := isGrantWithIdentifiedByPassword(c.stmt)
			if ok != c.wantOK {
				t.Fatalf("isGrantWithIdentifiedByPassword(%q) ok = %v, want %v", c.stmt, ok, c.wantOK)
			}
			if !ok {
				return
			}
			if rewritten != c.wantRewritten || user != c.wantUser || host != c.wantHost || hash != c.wantHash {
				t.Errorf("isGrantWithIdentifiedByPassword(%q) = (%q, %q, %q, %q), want (%q, %q, %q, %q)",
					c.stmt, rewritten, user, host, hash, c.wantRewritten, c.wantUser, c.wantHost, c.wantHash)
			}
		})
	}
}

// TestAccountAlreadyMatchesHash covers the live equivalence-lookup helper
// directly: matching hash -> skip; differing hash -> no skip; account absent
// -> no skip (nothing to compare against); lookup error -> no skip, error
// returned (the caller treats this as "forgo the optimization", not fatal).
func TestAccountAlreadyMatchesHash(t *testing.T) {
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"

	t.Run("matching hash", func(t *testing.T) {
		server, mock, conn, closeFn := newSystemReseedTestServer(t)
		defer closeFn()
		mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
			WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*HASH"))
		skip, err := server.accountAlreadyMatchesHash(context.Background(), conn, "root", "localhost", "*HASH")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !skip {
			t.Error("expected skip=true for a matching hash")
		}
	})

	t.Run("differing hash", func(t *testing.T) {
		server, mock, conn, closeFn := newSystemReseedTestServer(t)
		defer closeFn()
		mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
			WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*OTHER"))
		skip, err := server.accountAlreadyMatchesHash(context.Background(), conn, "root", "localhost", "*HASH")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			t.Error("expected skip=false for a differing hash")
		}
	})

	t.Run("account absent", func(t *testing.T) {
		server, mock, conn, closeFn := newSystemReseedTestServer(t)
		defer closeFn()
		mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("ghost", "localhost").
			WillReturnRows(sqlmock.NewRows([]string{"password"}))
		skip, err := server.accountAlreadyMatchesHash(context.Background(), conn, "ghost", "localhost", "*HASH")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if skip {
			t.Error("expected skip=false when the account doesn't exist yet")
		}
	})

	t.Run("lookup error", func(t *testing.T) {
		server, mock, conn, closeFn := newSystemReseedTestServer(t)
		defer closeFn()
		mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
			WillReturnError(errors.New("connection reset"))
		skip, err := server.accountAlreadyMatchesHash(context.Background(), conn, "root", "localhost", "*HASH")
		if err == nil {
			t.Fatal("expected the lookup error to propagate")
		}
		if skip {
			t.Error("expected skip=false on a lookup error")
		}
	})
}

// TestExecSplitdumpSingleSkipsCreateUserWhenHashMatches is the regression for
// the strict_password_validation case: when the destination already has this
// exact account with this exact password hash, CREATE USER (and its ALTER
// USER fallback) must never be sent at all -- both can be rejected by
// strict_password_validation purely for containing a password-setting
// clause, even though the value wouldn't change.
func TestExecSplitdumpSingleSkipsCreateUserWhenHashMatches(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASHMATCH'"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*HASHMATCH"))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No ExpectExec was registered at all: if CREATE USER (or an ALTER USER
	// fallback) had actually been sent, sqlmock would have returned an
	// "unexpected call" error, which would have surfaced as err above.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if progressed.Load() {
		t.Error("a deliberate hash-equivalence skip must not mark progress -- nothing committed")
	}
}

// TestExecSplitdumpSingleCreateUserStripsPasswordButKeepsOtherAttributes is
// the regression for silently dropping non-password attributes: a matching
// hash must not turn into a full statement skip when the dumped CREATE USER
// also carries other attributes (resource limits, lock state, password
// expiry, etc.) -- those must still be reconciled against the destination.
// Only the redundant IDENTIFIED BY PASSWORD clause is dropped; the rest is
// replayed as ALTER USER.
func TestExecSplitdumpSingleCreateUserStripsPasswordButKeepsOtherAttributes(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASHMATCH' WITH MAX_USER_CONNECTIONS 7"
	rewritten := "ALTER USER 'root'@'localhost' WITH MAX_USER_CONNECTIONS 7"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*HASHMATCH"))
	mock.ExpectExec(regexp.QuoteMeta(rewritten)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the rewritten (password-stripped) ALTER USER was registered: if
	// either the original CREATE USER or a full skip (nothing sent) had
	// occurred instead, sqlmock's unmet/unexpected-call checks below would
	// catch it.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after the rewritten ALTER USER committed")
	}
}

// TestExecSplitdumpSingleCreateUserExecutesWhenHashDiffers proves the skip
// above is gated on actual equivalence, not merely on the account existing:
// a differing hash must still execute CREATE USER (and fall through to the
// existing ALTER USER fallback machinery on collision) exactly as before.
func TestExecSplitdumpSingleCreateUserExecutesWhenHashDiffers(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*NEWHASH'"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*OLDHASH"))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after CREATE USER committed")
	}
}

// TestExecSplitdumpSingleCreateUserExecutesWhenLookupErrors proves a failed
// equivalence lookup only forgoes the optimization, never blocks the
// statement: CREATE USER still executes normally.
func TestExecSplitdumpSingleCreateUserExecutesWhenLookupErrors(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "CREATE USER 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASH'"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnError(errors.New("connection reset"))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after CREATE USER committed")
	}
}

// TestExecSplitdumpSingleGrantRewritesWhenHashMatches is the GRANT-side
// regression: mariadb-dump --system=all emits `GRANT ... IDENTIFIED BY
// PASSWORD` for the same account CREATE USER targets, and that clause can
// independently trip strict_password_validation. When the hash already
// matches, only that clause is stripped -- the privilege grant itself still
// executes.
func TestExecSplitdumpSingleGrantRewritesWhenHashMatches(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' IDENTIFIED BY PASSWORD '*HASHMATCH' WITH GRANT OPTION"
	rewritten := "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*HASHMATCH"))
	mock.ExpectExec(regexp.QuoteMeta(rewritten)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Only the rewritten statement was registered: if the original
	// (unstripped) GRANT had been sent instead, sqlmock would reject it as
	// an unexpected call, surfacing as err above.
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after the rewritten GRANT committed")
	}
}

// TestExecSplitdumpSingleGrantExecutesUnmodifiedWhenHashDiffers proves the
// GRANT rewrite is also gated on actual equivalence: a differing hash must
// execute the GRANT exactly as dumped, IDENTIFIED BY PASSWORD clause intact.
func TestExecSplitdumpSingleGrantExecutesUnmodifiedWhenHashDiffers(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' IDENTIFIED BY PASSWORD '*NEWHASH' WITH GRANT OPTION"
	const lookupQuery = "SELECT password FROM mysql.user WHERE user = ? AND host = ? LIMIT 1"
	mock.ExpectQuery(regexp.QuoteMeta(lookupQuery)).WithArgs("root", "localhost").
		WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow("*OLDHASH"))
	mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after the unmodified GRANT committed")
	}
}

// TestExecSplitdumpSingleGrantWithoutIdentifiedClauseUnaffected proves
// ordinary GRANT statements (no password-setting clause at all -- e.g. GRANT
// PROXY, or a modern GRANT with no legacy auth tail) never trigger the new
// equivalence lookup and execute exactly as before.
func TestExecSplitdumpSingleGrantWithoutIdentifiedClauseUnaffected(t *testing.T) {
	server, mock, conn, closeFn := newSystemReseedTestServer(t)
	defer closeFn()

	stmt := "GRANT PROXY ON ''@'%' TO 'root'@'localhost' WITH GRANT OPTION"
	mock.ExpectExec(regexp.QuoteMeta(stmt)).WillReturnResult(sqlmock.NewResult(0, 1))

	var progressed atomic.Bool
	err := server.execSplitdumpSingle(context.Background(), conn, stmt, "mysql.system-all.sql.gz", &progressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet sqlmock expectations: %v", err)
	}
	if !progressed.Load() {
		t.Error("expected progress to be marked after GRANT PROXY committed")
	}
}
