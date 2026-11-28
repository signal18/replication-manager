// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// AnalyticFeed is the state of an analytic child's feed (a DuckDB child, any destination
// plugin), read through its Replication object (the CDC replication of
// repl-mariadb-duckdb-cdc, ... in its target's server.Replications, CDCReplication.Feed). It is
// read only: the feed's states stay CINF0017, WARN0242, WARN0243, CINF0030, CINF0031 and
// WARN0340, raised every tick.
type AnalyticFeed struct {
	Cluster     string `json:"cluster"`
	Destination string `json:"destination"` // the plugin, replication-plugin-destination
	// Replication: the feed's plugin (repl-mariadb-duckdb-cdc, ...) and its name among the
	// links of its target ("cdc:<destination>"), "" before the seed
	Replication string `json:"replication,omitempty"`
	Name        string `json:"name,omitempty"`
	Tables      string `json:"tables"` // replication-plugin-tables
	// Source: replication-plugin-source as set (master or host:port), SourceHost the server
	// the running plugin reads ("" when none runs)
	Source     string `json:"source"`
	SourceHost string `json:"sourceHost,omitempty"`
	// CheckpointInterval, MergeInterval: seconds (merge 0: no staging)
	CheckpointInterval int `json:"checkpointInterval"`
	MergeInterval      int `json:"mergeInterval"`
	// Running: a process of the plugin runs; Stopped its last stop when it does not, with its
	// reason and the restart due (WARN0242)
	Running        bool       `json:"running"`
	StoppedReason  string     `json:"stoppedReason,omitempty"`
	RestartAt      *time.Time `json:"restartAt,omitempty"`
	CreatingSince  *time.Time `json:"creatingSince,omitempty"` // the seed's -create runs (CINF0031)
	ConfigNotFound bool       `json:"configNotFound"`          // no plugin TOML yet: the child is not seeded
	// Status: the plugin's last status line (GTID captured and applied, lag, counts, apply
	// error, last merge), nil before the first one
	Status *wire.Status `json:"status,omitempty"`
	// Skipped: the tables the seed left as they were, with the reason (WARN0340)
	Skipped []wire.CreateResult `json:"skipped,omitempty"`
	// SkippedError: the file of the tables left as they were cannot be read (WARN0340 says it too)
	SkippedError string `json:"skippedError,omitempty"`
}

// AnalyticFeedStatus is the feed of the cluster when it is an analytic child, false when it is
// not one. Safe to call from any goroutine: it reads the feed's TOML, takes the supervisor's
// lock briefly and reads the runner's atomics, never a database.
func (cluster *Cluster) AnalyticFeedStatus() (AnalyticFeed, bool) {
	if !cluster.HasAnalyticChild() {
		return AnalyticFeed{}, false
	}
	f := AnalyticFeed{
		Cluster:            cluster.Name,
		Destination:        strings.TrimSpace(cluster.Conf.ReplicationPluginDestination),
		Tables:             cluster.Conf.ReplicationPluginTables,
		Source:             cluster.Conf.ReplicationPluginSource,
		CheckpointInterval: cluster.Conf.ReplicationPluginCheckpointInterval,
		MergeInterval:      cluster.Conf.ReplicationPluginMergeInterval,
	}
	if t := cluster.analyticCreating.Load(); t != nil {
		at := *t
		f.CreatingSince = &at
	}
	if r := cluster.cdcReplicationAt(cluster.analyticPluginPath()); r == nil {
		f.ConfigNotFound = true
	} else if st, err := r.Feed(); err == nil {
		f.Replication, f.Name = r.Plugin().Name, r.Name()
		f.SourceHost, f.Running, f.StoppedReason, f.RestartAt, f.Status = st.SourceHost, st.Running, st.StoppedReason, st.RestartAt, st.Status
	}
	var err error
	if f.Skipped, err = readAnalyticSkipped(cluster.analyticSkippedPath()); err != nil {
		f.SkippedError = err.Error()
	}
	return f, true
}
