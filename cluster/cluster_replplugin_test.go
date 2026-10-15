package cluster

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/utils/state"
)

// openState is the description of an open state of the cluster, "" when none.
func openState(cl *Cluster, code string) string {
	for _, s := range cl.StateMachine.GetOpenStates() {
		if s.ErrKey == code {
			return s.ErrDesc
		}
	}
	return ""
}

// A replication plugin is supervised on the active monitor only (#2015): started from its
// enabled TOML, its status raised as CINF0017, stopped when its TOML is disabled, never
// started on a standby, and a failing one says why in WARN0242.
func TestReplicationPluginSupervision(t *testing.T) {
	work := t.TempDir()
	cl := newColumnstoreTestCluster()
	cl.Name = "belair-mcs"
	cl.Conf.WorkingDir = work
	cl.Conf.ShareDir = work
	cl.Status = ConstMonitorActif
	cl.StateMachine = &state.StateMachine{}
	cl.StateMachine.Init()
	bin := filepath.Join(work, "plugins")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho '{\"wire\":1,\"state\":\"running\",\"gtid\":\"4-1-77\",\"lagSeconds\":2,\"inserts\":3,\"updates\":1,\"deletes\":1}'\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "replplugin-fake"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	failing := "#!/bin/sh\necho '{\"wire\":1,\"state\":\"error\",\"error\":\"source refused the connection\"}'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "replplugin-broken"), []byte(failing), 0755); err != nil {
		t.Fatal(err)
	}
	dir := cl.replPluginDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	toml := filepath.Join(dir, "columnstore.toml")
	write := func(path, plugin string, enabled bool) {
		c := "plugin = \"" + plugin + "\"\nenabled = " + map[bool]string{true: "true", false: "false"}[enabled] + "\n[[rule]]\nschema = \"repl_test\"\n"
		if err := os.WriteFile(path, []byte(c), 0644); err != nil {
			t.Fatal(err)
		}
		// a new modification time, whatever the clock resolution
		future := time.Now().Add(time.Duration(len(c)) * time.Second)
		os.Chtimes(path, future, future)
	}
	write(toml, "fake", true)
	defer cl.StopReplicationPlugins()

	waitState := func(code, want string) string {
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			cl.CheckReplicationPlugins()
			if d := openState(cl, code); strings.Contains(d, want) {
				return d
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("no %s with %q", code, want)
		return ""
	}
	desc := waitState("CINF0017", "GTID 4-1-77")
	if !strings.Contains(desc, "3 inserts, 1 updates, 1 deletes") {
		t.Fatalf("status: %s", desc)
	}

	write(toml, "fake", false)
	cl.CheckReplicationPlugins()
	cl.replPluginsMu.Lock()
	n := len(cl.replPlugins)
	cl.replPluginsMu.Unlock()
	if n != 0 {
		t.Fatalf("a disabled plugin still runs: %d", n)
	}

	write(toml, "fake", true)
	cl.Status = ConstMonitorStandby
	cl.CheckReplicationPlugins()
	cl.replPluginsMu.Lock()
	n = len(cl.replPlugins)
	cl.replPluginsMu.Unlock()
	if n != 0 {
		t.Fatalf("a standby must not run a replication plugin: %d", n)
	}

	cl.Status = ConstMonitorActif
	write(toml, "broken", true)
	waitState("WARN0242", "source refused the connection")
}
