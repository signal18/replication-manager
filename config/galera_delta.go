// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "strings"

// galeraPerStartVariables are Galera variables whose runtime value belongs to one start of the
// node, never to its configuration: a bootstrap node runs with wsrep_cluster_address=gcomm://
// (an empty member list), a node's start position changes at every start. Kept as a runtime
// delta (02_delta.cnf), gcomm:// overrode the member list at every later start and the node
// tried to bootstrap a new cluster, which Galera refuses ("It may not be safe to bootstrap the
// cluster from this node", #2350).
var galeraPerStartVariables = map[string]bool{
	"wsrep_cluster_address": true,
	"wsrep_start_position":  true,
}

// IsGaleraPerStartVariable says whether a variable is never kept as a runtime delta (the name as
// in a config file or SHOW VARIABLES: any case, '-' or '_', with or without loose_).
func IsGaleraPerStartVariable(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.TrimPrefix(strings.TrimPrefix(n, "loose_"), "loose-")
	return galeraPerStartVariables[strings.ReplaceAll(n, "-", "_")]
}

// StripGaleraPerStartVariables removes the lines of the per-start Galera variables from a
// config file's content, with the "# delta:" origin comment just above them: a delta file
// written before #2350 is cleaned when it is deployed, the node being down or not.
func StripGaleraPerStartVariables(content string) string {
	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") && !strings.HasPrefix(t, "[") {
			name := t
			if i := strings.IndexAny(t, "= \t"); i >= 0 {
				name = t[:i]
			}
			if IsGaleraPerStartVariable(name) {
				if n := len(out); n > 0 && strings.HasPrefix(strings.TrimSpace(out[n-1]), "# delta:") {
					out = out[:n-1]
				}
				continue
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
