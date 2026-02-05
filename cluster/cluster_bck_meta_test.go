package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/backupmgr"
)

func TestGetSnapshotTagValueKeyValue(t *testing.T) {
	tags := []string{
		"line:" + backupmgr.BackupLineAdhoc,
		"adhoc",
		"backup-tool:" + config.ConstBackupLogicalTypeMysqldump,
		"mysqldump",
	}
	lineExpected := backupmgr.BackupLineAdhoc
	toolExpected := config.ConstBackupLogicalTypeMysqldump
	t.Logf("input tags=%v", tags)
	if got := getSnapshotTagValue(tags, "line"); got != lineExpected {
		t.Logf("case=line expected=%q got=%q", lineExpected, got)
		t.Errorf("getSnapshotTagValue(line) = %q, want %q", got, lineExpected)
	}
	if got := getSnapshotTagValue(tags, "backup-tool"); got != toolExpected {
		t.Logf("case=backup-tool expected=%q got=%q", toolExpected, got)
		t.Errorf("getSnapshotTagValue(backup-tool) = %q, want %q", got, toolExpected)
	}
}

func TestGetSnapshotTagValueLegacyLine(t *testing.T) {
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "adhoc", tags: []string{"adhoc"}, want: backupmgr.BackupLineAdhoc},
		{name: "default", tags: []string{"default"}, want: backupmgr.BackupLineDefault},
		{name: "conflict", tags: []string{"adhoc", "default"}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("input tags=%v", tt.tags)
			if got := getSnapshotTagValue(tt.tags, "line"); got != tt.want {
				t.Logf("expected=%q got=%q", tt.want, got)
				t.Errorf("getSnapshotTagValue(line) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetSnapshotTagValueLegacyTool(t *testing.T) {
	toolA := config.ConstBackupLogicalTypeMysqldump
	toolB := config.ConstBackupPhysicalTypeXtrabackup
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{name: "single", tags: []string{strings.ToUpper(toolA)}, want: strings.ToLower(toolA)},
		{name: "multiple", tags: []string{toolA, toolB}, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("input tags=%v", tt.tags)
			if got := getSnapshotTagValue(tt.tags, "backup-tool"); got != tt.want {
				t.Logf("expected=%q got=%q", tt.want, got)
				t.Errorf("getSnapshotTagValue(backup-tool) = %q, want %q", got, tt.want)
			}
		})
	}
}
