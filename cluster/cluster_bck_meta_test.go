package cluster

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestGetSnapshotTagValueKeyValue(t *testing.T) {
	tags := []string{
		"backup-tool:" + config.ConstBackupLogicalTypeMysqldump,
		"mysqldump",
	}
	if got := getSnapshotTagValue(tags, "backup-tool"); got != config.ConstBackupLogicalTypeMysqldump {
		t.Errorf("getSnapshotTagValue(backup-tool) = %q, want %q", got, config.ConstBackupLogicalTypeMysqldump)
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
