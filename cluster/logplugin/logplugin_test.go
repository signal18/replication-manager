// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package logplugin

import (
	"fmt"
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

func TestGlobalRegistryContainsThreeBuiltins(t *testing.T) {
	// The three init() calls in plugin_*.go must have registered exactly three
	// plugins by the time this test runs.
	plugins := GlobalRegistry.All()
	if len(plugins) < 3 {
		t.Errorf("expected at least 3 registered plugins, got %d", len(plugins))
	}
	names := map[string]bool{}
	for _, p := range plugins {
		names[p.Name()] = true
	}
	for _, want := range []string{"errorlog-24h", "sqlerrorlog-24h", "slowlog-24h"} {
		if !names[want] {
			t.Errorf("plugin %q not found in registry", want)
		}
	}
}

func TestRegisterCustomPlugin(t *testing.T) {
	r := &Registry{}
	p := &ErrorLog24hPlugin{}
	r.plugins = append(r.plugins, p)
	if len(r.All()) != 1 {
		t.Fatal("expected 1 plugin after manual register")
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

// ---- ErrorLog24hPlugin ------------------------------------------------------

func TestErrorLog24hPlugin_NoEntries(t *testing.T) {
	p := &ErrorLog24hPlugin{}
	src := LogSource{ServerURL: "127.0.0.1:3306", ErrorLog: nil}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings, got %d", len(f))
	}
}

func TestErrorLog24hPlugin_OldErrorIgnored(t *testing.T) {
	p := &ErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		ErrorLog: []s18log.HttpMessage{
			makeMsg("ERROR", "disk full", oldTS()),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings for old error, got %d", len(f))
	}
}

func TestErrorLog24hPlugin_RecentErrorRaisesWarning(t *testing.T) {
	p := &ErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		ErrorLog: []s18log.HttpMessage{
			makeMsg("ERROR", "InnoDB corruption", recentTS()),
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(f))
	}
	if f[0].ErrKey != ErrKeyDBError24h {
		t.Errorf("wrong ErrKey: %q", f[0].ErrKey)
	}
	if f[0].Severity != SeverityWarning {
		t.Errorf("wrong severity: %q", f[0].Severity)
	}
}

func TestErrorLog24hPlugin_InfoLevelSkipped(t *testing.T) {
	p := &ErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		ErrorLog: []s18log.HttpMessage{
			makeMsg("Note", "server started", recentTS()),
			makeMsg("Info", "connected", recentTS()),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("Info/Note entries should not trigger plugin, got %d findings", len(f))
	}
}

func TestErrorLog24hPlugin_NoTimestampIncluded(t *testing.T) {
	// Entry with no timestamp is conservatively included.
	p := &ErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		ErrorLog: []s18log.HttpMessage{
			{Level: "ERROR", Text: "unknown error", Timestamp: ""},
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Errorf("no-timestamp ERROR should be included; got %d findings", len(f))
	}
}

func TestErrorLog24hPlugin_CountInDescription(t *testing.T) {
	p := &ErrorLog24hPlugin{}
	msgs := make([]s18log.HttpMessage, 5)
	for i := range msgs {
		msgs[i] = makeMsg("ERROR", fmt.Sprintf("err %d", i), recentTS())
	}
	src := LogSource{ServerURL: "127.0.0.1:3306", ErrorLog: msgs}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(f))
	}
	// Description should mention the count 5.
	want := "5"
	if !containsStr(f[0].Description, want) {
		t.Errorf("description %q doesn't mention count %q", f[0].Description, want)
	}
}

// ---- SqlErrorLog24hPlugin ---------------------------------------------------

func TestSqlErrorLog24hPlugin_NoEntries(t *testing.T) {
	p := &SqlErrorLog24hPlugin{}
	src := LogSource{ServerURL: "127.0.0.1:3306"}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings, got %d", len(f))
	}
}

func TestSqlErrorLog24hPlugin_RecentEntry(t *testing.T) {
	p := &SqlErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		SqlErrorLog: []s18log.HttpMessage{
			makeMsg("ERROR", "SELECT syntax error", recentTS()),
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(f))
	}
	if f[0].ErrKey != ErrKeySQLError24h {
		t.Errorf("wrong ErrKey: %q", f[0].ErrKey)
	}
}

func TestSqlErrorLog24hPlugin_OldEntryIgnored(t *testing.T) {
	p := &SqlErrorLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		SqlErrorLog: []s18log.HttpMessage{
			makeMsg("ERROR", "old sql error", oldTS()),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings for old SQL error, got %d", len(f))
	}
}

// ---- SlowLog24hPlugin -------------------------------------------------------

func TestSlowLog24hPlugin_NoEntries(t *testing.T) {
	p := &SlowLog24hPlugin{}
	src := LogSource{ServerURL: "127.0.0.1:3306"}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings, got %d", len(f))
	}
}

func TestSlowLog24hPlugin_RecentEntry(t *testing.T) {
	p := &SlowLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		SlowLog: []s18log.HttpMessage{
			makeMsg("", "SELECT SLEEP(10)", recentTS()),
		},
	}
	f := p.Evaluate(src)
	if len(f) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(f))
	}
	if f[0].ErrKey != ErrKeySlowLog24h {
		t.Errorf("wrong ErrKey: %q", f[0].ErrKey)
	}
}

func TestSlowLog24hPlugin_OldEntryIgnored(t *testing.T) {
	p := &SlowLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		SlowLog: []s18log.HttpMessage{
			makeMsg("", "SELECT SLEEP(10)", oldTS()),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("expected 0 findings for old slow query, got %d", len(f))
	}
}

func TestSlowLog24hPlugin_EmptyTextSkipped(t *testing.T) {
	p := &SlowLog24hPlugin{}
	src := LogSource{
		ServerURL: "127.0.0.1:3306",
		SlowLog: []s18log.HttpMessage{
			makeMsg("", "", recentTS()),
		},
	}
	if f := p.Evaluate(src); len(f) != 0 {
		t.Errorf("empty-text entry should be skipped, got %d findings", len(f))
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
