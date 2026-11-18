// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"strings"
	"testing"
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
