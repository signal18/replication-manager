package cluster

import (
	"errors"
	"testing"
)

type stubRow struct {
	scan func(dest ...any) error
}

func (r stubRow) Scan(dest ...any) error {
	return r.scan(dest...)
}

func TestTableExistsQuerySchemaValidation(t *testing.T) {
	_, err := tableExistsQuery(func() rowScanner { return stubRow{} }, "", "tbl")
	if err == nil {
		t.Fatal("expected error for empty schema")
	}

	_, err = tableExistsQuery(func() rowScanner { return stubRow{} }, "db", "")
	if err == nil {
		t.Fatal("expected error for empty table")
	}
}

func TestTableExistsQueryNilQuery(t *testing.T) {
	_, err := tableExistsQuery(nil, "db", "tbl")
	if err == nil {
		t.Fatal("expected error for nil query function")
	}
}

func TestTableExistsQueryNilRow(t *testing.T) {
	_, err := tableExistsQuery(func() rowScanner { return nil }, "db", "tbl")
	if err == nil {
		t.Fatal("expected error for nil row")
	}
}

func TestTableExistsQueryReturnsTrue(t *testing.T) {
	got, err := tableExistsQuery(func() rowScanner {
		return stubRow{scan: func(dest ...any) error {
			*(dest[0].(*int)) = 1
			return nil
		}}
	}, "mysql", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got {
		t.Fatal("expected table to exist")
	}
}

func TestTableExistsQueryReturnsFalse(t *testing.T) {
	got, err := tableExistsQuery(func() rowScanner {
		return stubRow{scan: func(dest ...any) error {
			*(dest[0].(*int)) = 0
			return nil
		}}
	}, "mysql", "missing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected table to be missing")
	}
}

func TestTableExistsQueryScanError(t *testing.T) {
	scanErr := errors.New("scan failed")
	_, err := tableExistsQuery(func() rowScanner {
		return stubRow{scan: func(dest ...any) error {
			return scanErr
		}}
	}, "mysql", "user")
	if err == nil {
		t.Fatal("expected scan error")
	}
}

func TestTableExistsSkipsWhenConnectionUnavailable(t *testing.T) {
	_, server := newSplitDumpTestServer(t)
	server.Conn = nil

	got, err := server.tableExists("mysql", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected table check to be skipped")
	}
}
