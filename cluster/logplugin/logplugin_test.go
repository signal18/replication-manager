// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package logplugin

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

// ---- helpers ----------------------------------------------------------------

func recentTS() string {
	return time.Now().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
}

func oldTS() string {
	return time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")
}

func makeMsg(level, text, ts string) s18log.HttpMessage {
	return s18log.HttpMessage{Level: level, Text: text, Timestamp: ts}
}

// ---- registry ---------------------------------------------------------------

func TestGlobalRegistryContainsBuiltins(t *testing.T) {
	plugins := GlobalRegistry.All()
	if len(plugins) < 1 {
		t.Errorf("expected at least 1 registered plugin, got %d", len(plugins))
	}
}

// ---- Finding.ToState --------------------------------------------------------

func TestFindingToState(t *testing.T) {
	f := Finding{
		ErrKey:      "WARN0200",
		Severity:    SeverityWarning,
		Description: "test description",
	}
	st := f.ToState("PLUGIN")
	if st.ErrKey != "WARN0200" {
		t.Errorf("ErrKey: got %q", st.ErrKey)
	}
	if st.ErrType != "WARNING" {
		t.Errorf("ErrType: got %q", st.ErrType)
	}
	if st.ErrFrom != "PLUGIN" {
		t.Errorf("ErrFrom: got %q", st.ErrFrom)
	}
}

// ---- helper -----------------------------------------------------------------

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		}())
}

// ---- parseLogTimestamp ------------------------------------------------------

func TestParseLogTimestamp(t *testing.T) {
	cases := []string{
		"2024-01-02 15:04:05",
		"2024-01-02T15:04:05.000000Z",
		"2024-01-02T15:04:05Z",
		"2006/01/02 15:04:05",
		"2024-01-02T15:04:05",
	}
	for _, tc := range cases {
		if _, err := parseLogTimestamp(tc); err != nil {
			t.Errorf("failed to parse %q: %v", tc, err)
		}
	}
	if _, err := parseLogTimestamp("not-a-date"); err == nil {
		t.Error("expected error for garbage timestamp")
	}
}
