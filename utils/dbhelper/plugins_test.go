package dbhelper

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/utils/version"
)

func newPluginTestConn(t *testing.T) (*sqlx.Conn, sqlmock.Sqlmock, func()) {
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
	return conn, mock, func() {
		conn.Close()
		db.Close()
	}
}

func pluginRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{"Name", "Status", "Type", "Library", "License"})
}

func TestGetPluginStatusConnAbsent(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginRows().AddRow("OTHER_PLUGIN", "ACTIVE", "STORAGE ENGINE", "other.so", "GPL"),
	)

	status, observed, err := GetPluginStatusConn(context.Background(), conn, "METADATA_LOCK_INFO", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginAbsent {
		t.Fatalf("expected PluginAbsent, got %v", status)
	}
	if observed != "" {
		t.Fatalf("expected empty observed status, got %q", observed)
	}
}

func TestGetPluginStatusConnActiveCaseInsensitive(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginRows().AddRow("METADATA_LOCK_INFO", "active", "INFORMATION SCHEMA", "metadata_lock_info.so", "GPL"),
	)

	status, observed, err := GetPluginStatusConn(context.Background(), conn, "METADATA_LOCK_INFO", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginActive {
		t.Fatalf("expected PluginActive, got %v", status)
	}
	if observed != "active" {
		t.Fatalf("expected observed status 'active', got %q", observed)
	}
}

// TestGetPluginStatusConnNameCaseInsensitive: a dump's INSTALL PLUGIN can
// name a plugin in a different case than SHOW PLUGINS reports (e.g. dump
// says "metadata_lock_info", server reports "METADATA_LOCK_INFO"). An
// exact-case comparison would misreport this ACTIVE plugin as absent.
func TestGetPluginStatusConnNameCaseInsensitive(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginRows().AddRow("METADATA_LOCK_INFO", "ACTIVE", "INFORMATION SCHEMA", "metadata_lock_info.so", "GPL"),
	)

	status, _, err := GetPluginStatusConn(context.Background(), conn, "metadata_lock_info", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginActive {
		t.Fatalf("expected PluginActive despite the name-case mismatch, got %v", status)
	}
}

func TestGetPluginStatusConnPresentNotActive(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MySQL"}
	mock.ExpectQuery("SHOW PLUGINS").WillReturnRows(
		pluginRows().AddRow("QUERY_RESPONSE_TIME", "DISABLED", "INFORMATION SCHEMA", "query_response_time.so", "GPL"),
	)

	status, observed, err := GetPluginStatusConn(context.Background(), conn, "QUERY_RESPONSE_TIME", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginPresentNotActive {
		t.Fatalf("expected PluginPresentNotActive, got %v", status)
	}
	if observed != "DISABLED" {
		t.Fatalf("expected observed status 'DISABLED', got %q", observed)
	}
}

func TestGetPluginStatusConnAmbiguous(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(
		pluginRows().
			AddRow("SQL_ERROR_LOG", "ACTIVE", "AUDIT", "sql_errlog.so", "GPL").
			AddRow("SQL_ERROR_LOG", "ACTIVE", "AUDIT", "sql_errlog.so", "GPL"),
	)

	status, _, err := GetPluginStatusConn(context.Background(), conn, "SQL_ERROR_LOG", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginAmbiguous {
		t.Fatalf("expected PluginAmbiguous, got %v", status)
	}
}

// TestGetPluginStatusConnRowsIterationErrorIsWrapped covers rows.Err() --
// distinct from a query-setup error (WillReturnError on the query itself,
// covered by TestGetPluginStatusConnQueryError) or a scan error (covered by
// TestGetPluginStatusConnScanErrorIsWrapped). rows.Err() reports a failure
// that surfaces mid-iteration (e.g. a dropped connection while streaming
// rows), and that underlying cause must be preserved, not discarded.
func TestGetPluginStatusConnRowsIterationErrorIsWrapped(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	iterErr := errors.New("connection reset mid-stream")
	rows := pluginRows().
		AddRow("disk", "ACTIVE", "INFORMATION SCHEMA", "disk.so", "GPL").
		RowError(0, iterErr)
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(rows)

	_, _, err := GetPluginStatusConn(context.Background(), conn, "disk", myver)
	if err == nil {
		t.Fatal("expected a rows-iteration error, got nil")
	}
	if !errors.Is(err, iterErr) {
		t.Fatalf("expected the wrapped error to unwrap to the injected row error, got: %v", err)
	}
}

func TestGetPluginStatusConnQueryError(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnError(context.DeadlineExceeded)

	_, _, err := GetPluginStatusConn(context.Background(), conn, "DISK", myver)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// The underlying query error must be preserved (wrapped), not discarded,
	// so callers/operators can see the real cause (a deadline, a permission
	// error, a connection drop, ...) instead of a generic message.
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the wrapped error to unwrap to context.DeadlineExceeded, got: %v", err)
	}
}

// TestGetPluginStatusConnScanErrorIsWrapped verifies a row-scan failure also
// preserves the underlying error rather than replacing it with a generic
// message.
func TestGetPluginStatusConnScanErrorIsWrapped(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MariaDB"}
	// A row with too few columns for Plugin{}'s Scan forces a scan error.
	badRows := sqlmock.NewRows([]string{"Name", "Status"}).AddRow("disk", "ACTIVE")
	mock.ExpectQuery("SHOW PLUGINS soname").WillReturnRows(badRows)

	_, _, err := GetPluginStatusConn(context.Background(), conn, "disk", myver)
	if err == nil {
		t.Fatal("expected a scan error, got nil")
	}
	if !strings.Contains(err.Error(), "scan") {
		t.Fatalf("expected the scan error to be identifiable in the message, got: %v", err)
	}
}

func TestGetPluginStatusConnMySQLUsesPlainShowPlugins(t *testing.T) {
	conn, mock, closeFn := newPluginTestConn(t)
	defer closeFn()

	myver := &version.Version{Flavor: "MySQL"}
	mock.ExpectQuery("^SHOW PLUGINS$").WillReturnRows(
		pluginRows().AddRow("clone", "ACTIVE", "CLONE", nil, "GPL"),
	)

	status, _, err := GetPluginStatusConn(context.Background(), conn, "clone", myver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status != PluginActive {
		t.Fatalf("expected PluginActive, got %v", status)
	}
}
