// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestValidateResticS3Mode(t *testing.T) {
	conf := &Config{}
	cases := []struct {
		mode  string
		valid bool
	}{
		{"auto", true},
		{"new", true},
		{"legacy", true},
		{"", false},
		{"AUTO", false},
		{"invalid", false},
		{"restic-aws", false},
	}
	for _, tc := range cases {
		err := conf.ValidateResticS3Mode(tc.mode)
		if tc.valid && err != nil {
			t.Errorf("ValidateResticS3Mode(%q): unexpected error: %v", tc.mode, err)
		}
		if !tc.valid && err == nil {
			t.Errorf("ValidateResticS3Mode(%q): expected error, got nil", tc.mode)
		}
	}
}

func TestNormalizeResticS3Mode(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", "auto"},
		{"invalid", "auto"},
		{"AUTO", "auto"},
		{"auto", "auto"},
		{"new", "new"},
		{"legacy", "legacy"},
	}
	for _, tc := range cases {
		conf := &Config{BackupResticS3Mode: tc.input}
		conf.NormalizeResticS3Mode()
		if conf.BackupResticS3Mode != tc.want {
			t.Errorf("NormalizeResticS3Mode(%q): got %q, want %q", tc.input, conf.BackupResticS3Mode, tc.want)
		}
	}
}

func TestNormalizeBackupArchiveMode_SetsS3ModeAuto(t *testing.T) {
	conf := &Config{
		BackupRestic:       true,
		BackupResticAws:    true,
		BackupResticS3Mode: "", // empty: should be normalized to auto
	}
	conf.NormalizeBackupArchiveMode()
	if conf.BackupResticS3Mode != ConstResticS3ModeAuto {
		t.Errorf("expected BackupResticS3Mode=auto after NormalizeBackupArchiveMode, got %q", conf.BackupResticS3Mode)
	}
}
