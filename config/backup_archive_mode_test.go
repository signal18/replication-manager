// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestIsS3ResticRepository(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"s3 prefix", "s3:bucket/path", true},
		{"uppercase scheme is not matched", "S3:bucket/path", false},
		{"sftp prefix", "sftp:user@host:/path", false},
		{"local path", "/var/backups/restic", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsS3ResticRepository(tt.path); got != tt.want {
				t.Errorf("IsS3ResticRepository(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsSftpResticRepository(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{"sftp prefix", "sftp:user@host:/path", true},
		{"uppercase scheme is not matched", "SFTP:user@host:/path", false},
		{"s3 prefix", "s3:bucket/path", false},
		{"local path", "/var/backups/restic", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSftpResticRepository(tt.path); got != tt.want {
				t.Errorf("IsSftpResticRepository(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestNormalizeBackupArchiveMode(t *testing.T) {
	tests := []struct {
		name string
		conf Config
		// expected results after NormalizeBackupArchiveMode
		wantMode      string
		wantRestic    bool
		wantResAws    bool
		wantLocalRepo string
	}{
		{
			name: "invalid mode with legacy flags unset migrates to none",
			conf: Config{
				BackupArchiveMode: "not-a-real-mode",
			},
			wantMode:   ConstBackupArchiveModeNone,
			wantRestic: false,
			wantResAws: false,
		},
		{
			name: "empty mode with backup-restic-aws migrates to restic-aws",
			conf: Config{
				BackupArchiveMode: "",
				BackupRestic:      true,
				BackupResticAws:   true,
			},
			wantMode:   ConstBackupArchiveModeResticAws,
			wantRestic: true,
			wantResAws: true,
		},
		{
			name: "none with legacy backup-restic=true and sftp local repo migrates to restic-sftp",
			conf: Config{
				BackupArchiveMode:           ConstBackupArchiveModeNone,
				BackupRestic:                true,
				BackupResticAws:             false,
				BackupResticLocalRepository: "sftp:backup@10.0.0.1:/srv/restic-repo",
			},
			wantMode:      ConstBackupArchiveModeResticSftp,
			wantRestic:    true,
			wantResAws:    false,
			wantLocalRepo: "sftp:backup@10.0.0.1:/srv/restic-repo",
		},
		{
			name: "none with legacy backup-restic=true and no sftp prefix migrates to restic-local",
			conf: Config{
				BackupArchiveMode:           ConstBackupArchiveModeNone,
				BackupRestic:                true,
				BackupResticAws:             false,
				BackupResticLocalRepository: "/var/lib/replication-manager/archive",
			},
			wantMode:   ConstBackupArchiveModeResticLocal,
			wantRestic: true,
			wantResAws: false,
		},
		{
			name: "valid mode is left untouched and resyncs legacy flags",
			conf: Config{
				BackupArchiveMode: ConstBackupArchiveModeResticLocal,
				BackupRestic:      false,
				BackupResticAws:   true,
			},
			wantMode:   ConstBackupArchiveModeResticLocal,
			wantRestic: true,
			wantResAws: false,
		},
		{
			name: "backup-restic-local-repository is trimmed",
			conf: Config{
				BackupArchiveMode:           ConstBackupArchiveModeNone,
				BackupRestic:                true,
				BackupResticLocalRepository: "  sftp:backup@10.0.0.1:/srv/restic-repo  ",
			},
			wantMode:      ConstBackupArchiveModeResticSftp,
			wantRestic:    true,
			wantResAws:    false,
			wantLocalRepo: "sftp:backup@10.0.0.1:/srv/restic-repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := tt.conf
			conf.NormalizeBackupArchiveMode()

			if conf.BackupArchiveMode != tt.wantMode {
				t.Errorf("BackupArchiveMode = %q, want %q", conf.BackupArchiveMode, tt.wantMode)
			}
			if conf.BackupRestic != tt.wantRestic {
				t.Errorf("BackupRestic = %v, want %v", conf.BackupRestic, tt.wantRestic)
			}
			if conf.BackupResticAws != tt.wantResAws {
				t.Errorf("BackupResticAws = %v, want %v", conf.BackupResticAws, tt.wantResAws)
			}
			if tt.wantLocalRepo != "" && conf.BackupResticLocalRepository != tt.wantLocalRepo {
				t.Errorf("BackupResticLocalRepository = %q, want %q", conf.BackupResticLocalRepository, tt.wantLocalRepo)
			}
		})
	}
}

func TestApplyBackupArchiveMode(t *testing.T) {
	conf := &Config{}

	if err := conf.ApplyBackupArchiveMode(ConstBackupArchiveModeResticAws); err != nil {
		t.Fatalf("unexpected error applying restic-aws: %v", err)
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeResticAws {
		t.Fatalf("BackupArchiveMode = %q, want %q", conf.BackupArchiveMode, ConstBackupArchiveModeResticAws)
	}
	if !conf.BackupRestic || !conf.BackupResticAws {
		t.Fatalf("expected BackupRestic=true BackupResticAws=true, got %v/%v", conf.BackupRestic, conf.BackupResticAws)
	}

	// An invalid mode is rejected and leaves the previous mode/flags intact.
	if err := conf.ApplyBackupArchiveMode("bogus"); err == nil {
		t.Fatalf("expected error for invalid backup-archive-mode")
	}
	if conf.BackupArchiveMode != ConstBackupArchiveModeResticAws {
		t.Fatalf("BackupArchiveMode changed after invalid input: %q", conf.BackupArchiveMode)
	}
}

func TestDeriveBackupArchiveModeFromFlags(t *testing.T) {
	tests := []struct {
		name            string
		backupRestic    bool
		backupResticAws bool
		localRepo       string
		want            string
	}{
		{"restic disabled maps to none", false, false, "", ConstBackupArchiveModeNone},
		{"restic disabled ignores aws/local repo", false, true, "sftp:user@host:/path", ConstBackupArchiveModeNone},
		{"aws flag maps to restic-aws", true, true, "", ConstBackupArchiveModeResticAws},
		{"sftp local repo maps to restic-sftp", true, false, "sftp:user@host:/path", ConstBackupArchiveModeResticSftp},
		{"non-sftp local repo maps to restic-local", true, false, "/var/lib/archive", ConstBackupArchiveModeResticLocal},
		{"empty local repo maps to restic-local", true, false, "", ConstBackupArchiveModeResticLocal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conf := &Config{BackupResticLocalRepository: tt.localRepo}
			if got := conf.DeriveBackupArchiveModeFromFlags(tt.backupRestic, tt.backupResticAws); got != tt.want {
				t.Errorf("DeriveBackupArchiveModeFromFlags(%v, %v) with localRepo %q = %q, want %q",
					tt.backupRestic, tt.backupResticAws, tt.localRepo, got, tt.want)
			}
		})
	}
}
