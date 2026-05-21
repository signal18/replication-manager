package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/dbhelper"
)

func strPtr(s string) *string { return &s }

func TestFormatColumn(t *testing.T) {
	tests := []struct {
		name string
		col  dbhelper.Column
		want string
	}{
		{
			name: "simple not null",
			col:  dbhelper.Column{Name: "id", Type: "int(11)", Nullable: false},
			want: "id int(11) NOT NULL",
		},
		{
			name: "nullable with default",
			col:  dbhelper.Column{Name: "email", Type: "varchar(255)", Nullable: true, Default: strPtr("NULL")},
			want: "email varchar(255) DEFAULT NULL",
		},
		{
			name: "auto increment",
			col:  dbhelper.Column{Name: "id", Type: "int(11)", Nullable: false, Extra: "auto_increment"},
			want: "id int(11) NOT NULL auto_increment",
		},
		{
			name: "default value",
			col:  dbhelper.Column{Name: "status", Type: "enum('active','inactive')", Nullable: false, Default: strPtr("'active'")},
			want: "status enum('active','inactive') NOT NULL DEFAULT 'active'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatColumn(tt.col)
			if got != tt.want {
				t.Errorf("formatColumn() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestColumnDiff_NewTable(t *testing.T) {
	newCols := []dbhelper.Column{
		{Name: "id", Type: "int(11)", Nullable: false, Extra: "auto_increment"},
		{Name: "name", Type: "varchar(100)", Nullable: false},
	}
	diff := columnDiff("mydb", "users", nil, newCols)

	if !strings.Contains(diff, "--- mydb.users (before)") {
		t.Error("missing before header")
	}
	if !strings.Contains(diff, "+++ mydb.users (after)") {
		t.Error("missing after header")
	}
	if !strings.Contains(diff, "+ id int(11) NOT NULL auto_increment") {
		t.Error("missing new column id")
	}
	if !strings.Contains(diff, "+ name varchar(100) NOT NULL") {
		t.Error("missing new column name")
	}
	if strings.Contains(diff, "\n- ") {
		t.Error("new table should have no removals")
	}
}

func TestColumnDiff_DroppedTable(t *testing.T) {
	oldCols := []dbhelper.Column{
		{Name: "id", Type: "int(11)", Nullable: false},
		{Name: "email", Type: "varchar(50)", Nullable: true},
	}
	diff := columnDiff("mydb", "users", oldCols, nil)

	if !strings.Contains(diff, "- id int(11) NOT NULL") {
		t.Error("missing dropped column id")
	}
	if !strings.Contains(diff, "- email varchar(50)") {
		t.Error("missing dropped column email")
	}
	if strings.Contains(diff, "\n+ ") {
		t.Error("dropped table should have no additions")
	}
}

func TestColumnDiff_AlteredTable(t *testing.T) {
	oldCols := []dbhelper.Column{
		{Name: "id", Type: "int(11)", Nullable: false},
		{Name: "email", Type: "varchar(50)", Nullable: false},
		{Name: "old_field", Type: "text", Nullable: true},
	}
	newCols := []dbhelper.Column{
		{Name: "id", Type: "int(11)", Nullable: false},
		{Name: "email", Type: "varchar(255)", Nullable: false},
		{Name: "phone", Type: "varchar(20)", Nullable: true},
	}
	diff := columnDiff("mydb", "users", oldCols, newCols)

	// email changed
	if !strings.Contains(diff, "- email varchar(50) NOT NULL") {
		t.Error("missing old email")
	}
	if !strings.Contains(diff, "+ email varchar(255) NOT NULL") {
		t.Error("missing new email")
	}
	// old_field dropped
	if !strings.Contains(diff, "- old_field text") {
		t.Error("missing dropped old_field")
	}
	// phone added
	if !strings.Contains(diff, "+ phone varchar(20)") {
		t.Error("missing new phone")
	}
	// id unchanged — should NOT appear
	if strings.Contains(diff, "id int(11)") {
		t.Error("unchanged column id should not appear in diff")
	}
}

func TestColumnDiff_EmptyBoth(t *testing.T) {
	diff := columnDiff("mydb", "empty", nil, nil)
	lines := strings.Split(strings.TrimSpace(diff), "\n")
	// Only header lines
	if len(lines) != 2 {
		t.Errorf("expected 2 header lines, got %d: %q", len(lines), diff)
	}
}

func TestColumnDiff_NoChange(t *testing.T) {
	cols := []dbhelper.Column{
		{Name: "id", Type: "int(11)", Nullable: false},
		{Name: "name", Type: "varchar(100)", Nullable: true},
	}
	diff := columnDiff("mydb", "users", cols, cols)
	// Should only have header lines, no +/- lines
	if strings.Contains(diff, "\n+ ") || strings.Contains(diff, "\n- ") {
		t.Errorf("identical columns should produce no diff lines, got: %q", diff)
	}
}
