// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// #2360: the replication user is created only when it is a user of its own and the switch is on.
// The server below has no cluster: touching it would panic, so each case proves no server was
// connected to.
func TestCreateReplicationUserSkips(t *testing.T) {
	cases := []struct {
		name string
		on   bool
		db   string
		rpl  string
	}{
		{"switch off", false, "root:pw", "repl:pw"},
		{"replication runs as the database user", true, "root:pw", "root:pw"},
		{"no replication credential", true, "root:pw", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cl := &Cluster{Conf: &config.Config{ProvDBCreateReplicationUser: c.on}}
			cl.Conf.Secrets = config.NewSecrets(map[string]config.Secret{
				"db-servers-credential":  {Value: c.db},
				"replication-credential": {Value: c.rpl},
			})
			cl.Servers = []*ServerMonitor{{}}
			cl.createReplicationUser()
		})
	}
}

// #2360, seen on a dev instance: a server whose name does not resolve yet right after the
// provision is tried again until it answers; one that never answers gets a last attempt told so
// (its warning) and is returned; a final error is not retried.
func TestCreateReplicationUserOnRetries(t *testing.T) {
	late, never, refused, ok := &ServerMonitor{URL: "late"}, &ServerMonitor{URL: "never"}, &ServerMonitor{URL: "refused"}, &ServerMonitor{URL: "ok"}
	calls := map[string]int{}
	lastSeen := map[string]bool{}
	left := createReplicationUserOn([]*ServerMonitor{late, nil, never, refused, ok}, func(s *ServerMonitor, last bool) bool {
		calls[s.URL]++
		if last {
			lastSeen[s.URL] = true
		}
		switch s.URL {
		case "late":
			return calls[s.URL] < 3 // answers at the third attempt
		case "never":
			return true
		default: // "refused": a final error; "ok": created
			return false
		}
	}, 200*time.Millisecond, 20*time.Millisecond)

	if len(left) != 1 || left[0] != never {
		t.Fatalf("left = %v, want only the server that never answered", left)
	}
	if calls["late"] != 3 {
		t.Fatalf("late server tried %d times, want 3", calls["late"])
	}
	if calls["refused"] != 1 || calls["ok"] != 1 {
		t.Fatalf("a final result was retried: refused %d, ok %d", calls["refused"], calls["ok"])
	}
	if !lastSeen["never"] {
		t.Fatal("the unreachable server's last attempt was not told it was the last")
	}
	if lastSeen["late"] {
		t.Fatal("the late server was told last while time remained")
	}
}
