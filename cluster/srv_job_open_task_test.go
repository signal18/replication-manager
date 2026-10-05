// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"
)

func TestJobTaskStillOpen(t *testing.T) {
	cases := []struct {
		name        string
		state, done int
		open        bool
	}{
		{"new job", 0, 0, true},
		{"running", 1, 0, true},
		{"halted, waiting for the stream", 2, 0, true},
		{"last open state", 3, 0, true},
		{"finished ok", 4, 1, false},
		{"error", 5, 1, false},
		{"error after the job", 6, 1, false},
		{"ended although the state is low", 2, 1, false},
	}
	for _, c := range cases {
		if got := jobTaskStillOpen(c.state, c.done); got != c.open {
			t.Errorf("%s (state %d, done %d): open = %v, want %v", c.name, c.state, c.done, got, c.open)
		}
	}
}

func TestJobOpenTaskErrorNamesTheTaskAndTheWayOut(t *testing.T) {
	err := jobOpenTaskError("reseedmariabackup", 42, 2)
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, want := range []string{"reseedmariabackup", "id 42", "state 2", "wait for it to end or cancel it"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q lacks %q", err.Error(), want)
		}
	}
}
