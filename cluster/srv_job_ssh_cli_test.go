// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/utils/state"
)

func TestUnameToGo(t *testing.T) {
	for uname, want := range map[string]string{
		"Linux x86_64\n": "linux/amd64",
		"Linux aarch64":  "linux/arm64",
		"Darwin arm64":   "darwin/arm64",
		"Linux armv7l":   "",
		"SunOS i86pc":    "",
		"":               "",
	} {
		if got := unameToGo(uname); got != want {
			t.Errorf("%q: %q, want %q", uname, got, want)
		}
	}
}

// The copy decision of an on-premise run: nothing when the host's marker names the monitor's CLI,
// a copy when it is absent or another one, an error naming both platforms (and the PATH escape)
// when the host is not the monitor's OS and architecture.
func TestJobsCliPlan(t *testing.T) {
	local := jobsCliLocal{hash: "aaa", version: "v3.1.51"}
	if why, err := jobsCliPlan("Linux x86_64", "aaa v3.1.51\n", local, "linux/amd64"); why != "" || err != nil {
		t.Fatalf("same CLI: %q %v", why, err)
	}
	if why, err := jobsCliPlan("Linux x86_64", "", local, "linux/amd64"); why != "absent" || err != nil {
		t.Fatalf("no marker: %q %v", why, err)
	}
	if why, err := jobsCliPlan("Linux x86_64", "bbb v3.1.50", local, "linux/amd64"); why != "another version" || err != nil {
		t.Fatalf("older CLI: %q %v", why, err)
	}
	_, err := jobsCliPlan("Linux aarch64", "", local, "linux/amd64")
	if err == nil || !strings.Contains(err.Error(), "linux/arm64") || !strings.Contains(err.Error(), "linux/amd64") || !strings.Contains(err.Error(), "PATH") {
		t.Fatalf("another architecture: %v", err)
	}
	if _, err := jobsCliPlan("Linux armv7l", "aaa", local, "linux/amd64"); err == nil || !strings.Contains(err.Error(), "armv7l") {
		t.Fatalf("an unknown architecture is named as uname gives it: %v", err)
	}
}

func TestShellSingleQuote(t *testing.T) {
	if got := shellSingleQuote(`/data/it's $HOME`); got != `'/data/it'"'"'s $HOME'` {
		t.Fatalf("%s", got)
	}
}

// An older script or CLI (no CLI version in its calls) asking for its script upgrade gets "no":
// the current script would run next to a CLI that cannot run it. The cookies are dropped (the
// rolling jobs upgrade does not wait), WARN0325 stays open in the config domain until the jobs
// call with a CLI version (after the service restart) or the upgrade is delivered.
func TestJobsUpgradeDeferredWithoutCli(t *testing.T) {
	s := jobLoginServer()
	c := s.ClusterGroup
	s.Datadir = t.TempDir()
	c.ConfigStateMachine = new(state.StateMachine)
	c.ConfigStateMachine.Init()
	s.SetWaitJobsUpgradeCookie()
	s.SetRollingJobsUpgradeCookie()

	s.DeferJobsUpgrade("")
	if s.HasWaitJobsUpgradeCookie() || s.HasRollingJobsUpgradeCookie() {
		t.Fatal("a deferred upgrade drops its cookies")
	}
	c.CheckJobsCli()
	open := c.ConfigStateMachine.GetOpenStates()
	if len(open) != 1 || open[0].ErrKey != "WARN0325" || !strings.Contains(open[0].ErrDesc, "db1:3306") || !strings.Contains(open[0].ErrDesc, "none") {
		t.Fatalf("WARN0325 naming the server and the CLI it saw: %+v", open)
	}

	s.JobsCliCalled("v3.1.52")
	c.ConfigStateMachine.ClearState()
	c.CheckJobsCli()
	if open := c.ConfigStateMachine.GetOpenStates(); len(open) != 0 || func() bool { c.CheckJobsCli(); return s.JobsCliVersion != "v3.1.52" }() {
		t.Fatalf("jobs calling with a CLI version close it: %+v %q", open, s.JobsCliVersion)
	}
}

// The probe exits 0 and prints the system then the marker only next to an executable CLI: on a
// host with no CLI, or a CLI without a marker, it is the first copy, not an error.
func TestJobsCliProbe(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "replication-manager-cli")
	run := func() (string, string) {
		out, err := exec.Command("bash", "-c", jobsCliProbe(target)).Output()
		if err != nil {
			t.Fatalf("the probe must exit 0: %v", err)
		}
		uname, marker, _ := strings.Cut(string(out), "\n\n")
		return uname, marker
	}
	if uname, marker := run(); uname == "" || marker != "" {
		t.Fatalf("no CLI: %q %q", uname, marker)
	}
	os.WriteFile(target+".sha256", []byte("aaa v1\n"), 0644)
	if _, marker := run(); marker != "" {
		t.Fatalf("a marker without a CLI is not read: %q", marker)
	}
	os.WriteFile(target, []byte("#!/bin/sh\n"), 0755)
	if _, marker := run(); marker != "aaa v1\n" {
		t.Fatalf("CLI and marker: %q", marker)
	}
	os.Remove(target + ".sha256")
	if _, marker := run(); marker != "" {
		t.Fatalf("CLI without a marker: %q", marker)
	}
}
