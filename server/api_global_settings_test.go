// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// TestSetRepmanSetting_LogHistory verifies that the four log-history-* keys
// (config.go:161-164, scope:"server") are accepted and persisted by
// setRepmanSetting. Guards against the scope tag classifying these as valid
// global settings (server/api_global_settings.go's config.IsScope gate) while
// this switch has no case for them, which would fall through to "setting not
// found" despite passing that earlier check.
func TestSetRepmanSetting_LogHistory(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
		ConfigManager: newConfigManagerForTest(),
	}

	cases := []struct {
		setting string
		value   string
		check   func(*config.Config) bool
	}{
		{
			setting: "log-history-enable",
			value:   "on",
			check:   func(c *config.Config) bool { return c.LogHistoryEnable == true },
		},
		{
			setting: "log-history-max-scan-bytes",
			value:   "1048576",
			check:   func(c *config.Config) bool { return c.LogHistoryMaxScanBytes == 1048576 },
		},
		{
			setting: "log-history-max-lines",
			value:   "500",
			check:   func(c *config.Config) bool { return c.LogHistoryMaxLines == 500 },
		},
		{
			setting: "log-history-max-files",
			value:   "10",
			check:   func(c *config.Config) bool { return c.LogHistoryMaxFiles == 10 },
		},
	}

	for _, tc := range cases {
		t.Run(tc.setting, func(t *testing.T) {
			if err := repman.setRepmanSetting(tc.setting, tc.value); err != nil {
				t.Fatalf("setRepmanSetting(%q): unexpected error: %v", tc.setting, err)
			}
			if !tc.check(repman.Conf) {
				t.Fatalf("setRepmanSetting(%q) = %q: config field not updated", tc.setting, tc.value)
			}
		})
	}
}

// TestSetRepmanSetting_LogHistoryBounds_ClearResetsToZero verifies that
// clearing one of the log-history-max-* settings — which reaches
// setRepmanSetting with an empty value, since /actions/clear/{settingName}
// routes through handlerMuxSetGlobalSettings with no settingValue var set —
// resets the field to 0 (the documented "use default bound" sentinel)
// instead of being rejected as invalid input.
func TestSetRepmanSetting_LogHistoryBounds_ClearResetsToZero(t *testing.T) {
	for _, setting := range []string{"log-history-max-scan-bytes", "log-history-max-lines", "log-history-max-files"} {
		t.Run(setting, func(t *testing.T) {
			repman := &ReplicationManager{
				Conf:          &config.Config{Secrets: make(map[string]config.Secret), LogHistoryMaxScanBytes: 999, LogHistoryMaxLines: 999, LogHistoryMaxFiles: 999},
				ConfigManager: newConfigManagerForTest(),
			}
			if err := repman.setRepmanSetting(setting, ""); err != nil {
				t.Fatalf("setRepmanSetting(%q, \"\"): unexpected error: %v", setting, err)
			}
			switch setting {
			case "log-history-max-scan-bytes":
				if repman.Conf.LogHistoryMaxScanBytes != 0 {
					t.Errorf("LogHistoryMaxScanBytes = %d, want 0", repman.Conf.LogHistoryMaxScanBytes)
				}
			case "log-history-max-lines":
				if repman.Conf.LogHistoryMaxLines != 0 {
					t.Errorf("LogHistoryMaxLines = %d, want 0", repman.Conf.LogHistoryMaxLines)
				}
			case "log-history-max-files":
				if repman.Conf.LogHistoryMaxFiles != 0 {
					t.Errorf("LogHistoryMaxFiles = %d, want 0", repman.Conf.LogHistoryMaxFiles)
				}
			}
		})
	}
}

// TestSetRepmanSetting_LogHistoryEnable_ClearRestoresDefault verifies that
// clearing log-history-enable — which reaches setRepmanSetting with an empty
// value, same as the log-history-max-* settings — restores the flag's real
// default (true, per server_cmd.go) rather than being read through
// isactive's "on" check (which treats "" as false, turning "clear" into
// "disable").
func TestSetRepmanSetting_LogHistoryEnable_ClearRestoresDefault(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret), LogHistoryEnable: false},
		ConfigManager: newConfigManagerForTest(),
	}
	if err := repman.setRepmanSetting("log-history-enable", ""); err != nil {
		t.Fatalf("setRepmanSetting(\"log-history-enable\", \"\"): unexpected error: %v", err)
	}
	if !repman.Conf.LogHistoryEnable {
		t.Fatal("expected LogHistoryEnable to be restored to true (its default) after clear, got false")
	}
}

// TestSetRepmanSetting_LogHistoryBounds_InvalidInput verifies that a
// non-numeric or negative value for the three log-history-max-* settings is
// rejected with an error rather than being silently coerced to 0 by
// strconv.Atoi's ignored parse error (0 has its own meaning here — "use the
// package default" — so it must never be what an invalid request quietly
// becomes).
func TestSetRepmanSetting_LogHistoryBounds_InvalidInput(t *testing.T) {
	settings := []string{"log-history-max-scan-bytes", "log-history-max-lines", "log-history-max-files"}
	badValues := []string{"not-a-number", "-1"}

	for _, setting := range settings {
		for _, bad := range badValues {
			t.Run(setting+"/"+bad, func(t *testing.T) {
				repman := &ReplicationManager{
					Conf:          &config.Config{Secrets: make(map[string]config.Secret)},
					ConfigManager: newConfigManagerForTest(),
				}
				if err := repman.setRepmanSetting(setting, bad); err == nil {
					t.Fatalf("setRepmanSetting(%q, %q): expected error, got nil", setting, bad)
				}
			})
		}
	}
}

// TestSwitchRepmanSetting_LogHistoryEnable verifies switchRepmanSetting
// toggles log-history-enable rather than falling through to "setting not
// found".
func TestSwitchRepmanSetting_LogHistoryEnable(t *testing.T) {
	repman := &ReplicationManager{
		Conf:          &config.Config{Secrets: make(map[string]config.Secret), LogHistoryEnable: false},
		ConfigManager: newConfigManagerForTest(),
	}

	if err := repman.switchRepmanSetting("log-history-enable"); err != nil {
		t.Fatalf("switchRepmanSetting(\"log-history-enable\"): unexpected error: %v", err)
	}
	if !repman.Conf.LogHistoryEnable {
		t.Fatal("expected LogHistoryEnable to be true after switch")
	}

	if err := repman.switchRepmanSetting("log-history-enable"); err != nil {
		t.Fatalf("switchRepmanSetting(\"log-history-enable\") second call: unexpected error: %v", err)
	}
	if repman.Conf.LogHistoryEnable {
		t.Fatal("expected LogHistoryEnable to be false after second switch")
	}
}
