package cluster

import (
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// The ping mode is chosen only when the app asks for it, in any case (#1919).
func TestAppMonitorModeIsPing(t *testing.T) {
	if appMonitorModeIsPing(nil) {
		t.Fatal("nil config: port mode")
	}
	for _, v := range []string{"", "port", "PORT", "tcp", "anything"} {
		if appMonitorModeIsPing(&config.AppConfig{AppMonitorMode: v}) {
			t.Fatalf("%q must be port mode", v)
		}
	}
	for _, v := range []string{"ping", "PING", " ping "} {
		if !appMonitorModeIsPing(&config.AppConfig{AppMonitorMode: v}) {
			t.Fatalf("%q must be ping mode", v)
		}
	}
}

// Loopback answers an echo; an unresolvable name fails with the resolve error. The echo
// test is skipped where the unprivileged ICMP socket is refused.
func TestPingHost(t *testing.T) {
	if err := pingHost("no-such-host.invalid", time.Second); err == nil || !strings.Contains(err.Error(), "resolve") {
		t.Fatalf("unresolvable host must fail on resolution, got %v", err)
	}
	err := pingHost("127.0.0.1", 2*time.Second)
	if err != nil && strings.Contains(err.Error(), "icmp socket") {
		t.Skipf("unprivileged ICMP not available here: %v", err)
	}
	if err != nil {
		t.Fatalf("loopback echo: %v", err)
	}
}

// The setting accepts port, ping and empty, refuses anything else, and never leaves the
// app half-set (#1919).
func TestSetAppMonitorMode(t *testing.T) {
	app := &App{AppConfig: &config.AppConfig{}, ClusterGroup: &Cluster{}}
	for _, v := range []string{"ping", "PORT", ""} {
		if err := app.SetSetting("app-monitor-mode", v); err != nil {
			t.Fatalf("%q must be accepted: %v", v, err)
		}
		if got := app.AppConfig.AppMonitorMode; got != strings.ToLower(strings.TrimSpace(v)) {
			t.Fatalf("%q stored as %q", v, got)
		}
	}
	if err := app.SetSetting("app-monitor-mode", "bogus"); err == nil {
		t.Fatal("bogus must be refused")
	}
	if app.AppConfig.AppMonitorMode != "" {
		t.Fatalf("a refused value must not be stored, got %q", app.AppConfig.AppMonitorMode)
	}
}
