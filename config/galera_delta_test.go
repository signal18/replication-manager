// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// #2350: the per-start Galera variables are never kept as a runtime delta, whatever their
// spelling, and a delta file written before is cleaned with its origin comments.
func TestGaleraPerStartVariables(t *testing.T) {
	for name, want := range map[string]bool{
		"wsrep_cluster_address": true, "WSREP_CLUSTER_ADDRESS": true, "wsrep-cluster-address": true,
		"loose_wsrep_cluster_address": true, "wsrep_start_position": true,
		"wsrep_on": false, "wsrep_node_address": false, "max_connections": false,
	} {
		if got := IsGaleraPerStartVariable(name); got != want {
			t.Errorf("%s: %v, want %v", name, got, want)
		}
	}
	in := "[mysqld]\n# delta:value-differs\nwsrep_cluster_address=gcomm://\n# delta:value-differs\nmax_connections = 300\n# delta:no-config\nloose_wsrep_start_position = 6a57:-1\n"
	want := "[mysqld]\n# delta:value-differs\nmax_connections = 300\n"
	if got := StripGaleraPerStartVariables(in); got != want {
		t.Fatalf("stripped:\n%q\nwant\n%q", got, want)
	}
}
