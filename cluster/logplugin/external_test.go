// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package logplugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/s18log"
)

// ---- helpers ----------------------------------------------------------------

// writeFakePlugin creates a tiny shell script (or .bat on Windows) in dir that
// outputs a canned JSON response and exits 0.
func writeFakePlugin(t *testing.T, dir, name string, resp stdioResponse) string {
	t.Helper()
	binPath := filepath.Join(dir, "plugin-"+name)

	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}

	var script string
	if runtime.GOOS == "windows" {
		binPath += ".bat"
		script = fmt.Sprintf("@echo off\necho %s\n", string(data))
	} else {
		script = fmt.Sprintf("#!/bin/sh\necho '%s'\n", string(data))
	}

	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return binPath
}

func writeBrokenPlugin(t *testing.T, dir, name string) string {
	t.Helper()
	binPath := filepath.Join(dir, "plugin-"+name)
	script := "#!/bin/sh\nexit 1\n"
	if runtime.GOOS == "windows" {
		binPath += ".bat"
		script = "@echo off\nexit /b 1\n"
	}
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return binPath
}

func writeNonExecFile(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("not a plugin"), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---- pluginNameFromBinary ---------------------------------------------------

func TestPluginNameFromBinary(t *testing.T) {
	cases := [][2]string{
		{"plugin-innodb-corruption", "innodb-corruption"},
		{"plugin-foo.exe", "foo"},
		{"myplugin", "myplugin"},
		{"plugin-a-b-c", "a-b-c"},
	}
	for _, tc := range cases {
		got := pluginNameFromBinary(tc[0])
		if got != tc[1] {
			t.Errorf("pluginNameFromBinary(%q) = %q, want %q", tc[0], got, tc[1])
		}
	}
}

// ---- PluginDir --------------------------------------------------------------

func TestPluginDir(t *testing.T) {
	got := PluginDir("/var/lib/repman/mycluster")
	want := "/var/lib/repman/mycluster/plugins"
	if got != want {
		t.Errorf("PluginDir = %q, want %q", got, want)
	}
}

// ---- LoadPluginsFromDir -----------------------------------------------------

func TestLoadPluginsFromDir_NonExistent(t *testing.T) {
	n, err := LoadPluginsFromDir("/does/not/exist", &Registry{})
	if err != nil {
		t.Errorf("expected nil error for missing dir, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 plugins, got %d", n)
	}
}

func TestLoadPluginsFromDir_SkipsNonExecutable(t *testing.T) {
	dir := t.TempDir()
	writeNonExecFile(t, dir, "notaplugin.txt")
	reg := &Registry{}
	n, err := LoadPluginsFromDir(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("expected 0 loaded, got %d", n)
	}
}

func TestLoadPluginsFromDir_LoadsExecutables(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	writeFakePlugin(t, dir, "alpha", stdioResponse{})
	writeFakePlugin(t, dir, "beta", stdioResponse{})
	writeNonExecFile(t, dir, "readme.txt")

	reg := &Registry{}
	n, err := LoadPluginsFromDir(dir, reg)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("expected 2 loaded, got %d", n)
	}
	names := map[string]bool{}
	for _, p := range reg.All() {
		names[p.Name()] = true
	}
	for _, want := range []string{"alpha", "beta"} {
		if !names[want] {
			t.Errorf("plugin %q not registered", want)
		}
	}
}

func TestLoadPluginsFromDir_ReplacesExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	writeFakePlugin(t, dir, "alpha", stdioResponse{})

	reg := &Registry{}
	// First load
	LoadPluginsFromDir(dir, reg)
	if len(reg.All()) != 1 {
		t.Fatal("expected 1 plugin after first load")
	}
	// Second load — same name, should replace not append
	LoadPluginsFromDir(dir, reg)
	if len(reg.All()) != 1 {
		t.Errorf("expected 1 plugin after reload, got %d", len(reg.All()))
	}
}

// ---- ExternalLogPlugin.Evaluate ---------------------------------------------

func TestExternalPlugin_AllClear(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	binPath := writeFakePlugin(t, dir, "noop", stdioResponse{Findings: []stdioFinding{}})

	p := NewExternalLogPlugin("noop", binPath, 5*time.Second)
	src := LogSource{ServerURL: "127.0.0.1:3306",
		ErrorLog: []s18log.HttpMessage{{Level: "ERROR", Text: "boom", Timestamp: "2024-01-02 10:00:00"}}}
	findings := p.Evaluate(src)
	if len(findings) != 0 {
		t.Errorf("expected 0 findings, got %v", findings)
	}
}

func TestExternalPlugin_ReturnsFinding(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	resp := stdioResponse{Findings: []stdioFinding{
		{ErrKey: "WARN0300", Severity: "WARNING", Description: "test finding"},
	}}
	binPath := writeFakePlugin(t, dir, "mycheck", resp)

	p := NewExternalLogPlugin("mycheck", binPath, 5*time.Second)
	findings := p.Evaluate(LogSource{ServerURL: "127.0.0.1:3306"})
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].ErrKey != "WARN0300" {
		t.Errorf("wrong ErrKey: %s", findings[0].ErrKey)
	}
	if findings[0].Severity != SeverityWarning {
		t.Errorf("wrong severity: %s", findings[0].Severity)
	}
}

func TestExternalPlugin_BrokenExitReturnsWARN0203(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	binPath := writeBrokenPlugin(t, dir, "broken")

	p := NewExternalLogPlugin("broken", binPath, 5*time.Second)
	findings := p.Evaluate(LogSource{ServerURL: "127.0.0.1:3306"})
	if len(findings) != 1 {
		t.Fatalf("expected 1 error finding, got %d", len(findings))
	}
	if findings[0].ErrKey != "WARN0203" {
		t.Errorf("expected WARN0203, got %s", findings[0].ErrKey)
	}
}

func TestExternalPlugin_Timeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "plugin-slow")
	script := "#!/bin/sh\nsleep 10\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	p := NewExternalLogPlugin("slow", binPath, 100*time.Millisecond)
	findings := p.Evaluate(LogSource{ServerURL: "127.0.0.1:3306"})
	if len(findings) != 1 || findings[0].ErrKey != "WARN0203" {
		t.Errorf("expected WARN0203 timeout finding, got %v", findings)
	}
}

func TestExternalPlugin_InvalidJSONReturnsWARN0203(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	binPath := filepath.Join(dir, "plugin-badjson")
	script := "#!/bin/sh\necho 'this is not json'\n"
	if err := os.WriteFile(binPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	p := NewExternalLogPlugin("badjson", binPath, 5*time.Second)
	findings := p.Evaluate(LogSource{ServerURL: "127.0.0.1:3306"})
	if len(findings) != 1 || findings[0].ErrKey != "WARN0203" {
		t.Errorf("expected WARN0203 for bad JSON, got %v", findings)
	}
}

func TestExternalPlugin_UnknownSeverityDefaultsToWarning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script plugins not tested on Windows")
	}
	dir := t.TempDir()
	resp := stdioResponse{Findings: []stdioFinding{
		{ErrKey: "WARN0301", Severity: "CRITICAL", Description: "unknown sev"},
	}}
	binPath := writeFakePlugin(t, dir, "critsev", resp)

	p := NewExternalLogPlugin("critsev", binPath, 5*time.Second)
	findings := p.Evaluate(LogSource{ServerURL: "127.0.0.1:3306"})
	if len(findings) != 1 {
		t.Fatalf("expected 1 finding, got %d", len(findings))
	}
	if findings[0].Severity != SeverityWarning {
		t.Errorf("unknown severity should default to WARNING, got %s", findings[0].Severity)
	}
}
