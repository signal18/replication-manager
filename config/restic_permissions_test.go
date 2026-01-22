package config

import (
	"os"
	"testing"
)

func TestParseResticMode(t *testing.T) {
	defaultMode := os.FileMode(0700)

	if got := parseResticMode(700, defaultMode); got != 0700 {
		t.Fatalf("expected 700 to parse as 0700, got %#o", got)
	}
	if got := parseResticMode(600, defaultMode); got != 0600 {
		t.Fatalf("expected 600 to parse as 0600, got %#o", got)
	}
	if got := parseResticMode(755, defaultMode); got != 0755 {
		t.Fatalf("expected 755 to parse as 0755, got %#o", got)
	}
	if got := parseResticMode(400, defaultMode); got != defaultMode {
		t.Fatalf("expected 400 to fallback to default, got %#o", got)
	}
	if got := parseResticMode(888, defaultMode); got != defaultMode {
		t.Fatalf("expected 888 to fallback to default, got %#o", got)
	}
}

func TestValidateResticPermissions(t *testing.T) {
	conf := &Config{}

	conf.BackupResticDirMode = 700
	conf.BackupResticFileMode = 600
	if err := conf.ValidateResticPermissions(); err != nil {
		t.Fatalf("expected valid permissions, got error: %v", err)
	}

	conf.BackupResticDirMode = 400
	conf.BackupResticFileMode = 600
	if err := conf.ValidateResticPermissions(); err == nil {
		t.Fatalf("expected error for invalid dir mode 400")
	}

	conf.BackupResticDirMode = 700
	conf.BackupResticFileMode = 888
	if err := conf.ValidateResticPermissions(); err == nil {
		t.Fatalf("expected error for invalid file mode 888")
	}

	conf.BackupResticDirMode = 0
	conf.BackupResticFileMode = 0
	if err := conf.ValidateResticPermissions(); err != nil {
		t.Fatalf("expected zero values to be valid, got error: %v", err)
	}
}
