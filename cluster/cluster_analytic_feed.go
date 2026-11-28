// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
)

// AnalyticFeed is the state of an analytic child's feed (a DuckDB child, any destination
// plugin): what the replication interface's CDC backend reads to show the feed as a
// replication link, beside the native channels. It is read only: the feed's states stay
// CINF0017, WARN0242, WARN0243, CINF0030, CINF0031 and WARN0340, raised every tick.
type AnalyticFeed struct {
	Cluster     string `json:"cluster"`
	Destination string `json:"destination"` // the plugin, replication-plugin-destination
	Tables      string `json:"tables"`      // replication-plugin-tables
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
// not one. Safe to call from any goroutine: it takes the supervisor's lock briefly and reads the
// runner's atomics, never a database.
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
	path := cluster.analyticPluginPath()
	cluster.replPluginsMu.Lock()
	r := cluster.replPlugins[path]
	cluster.replPluginsMu.Unlock()
	if r != nil {
		f.SourceHost = r.cfg.Source.Host
		if stop := r.stopped.Load(); stop != nil {
			at := stop.Restart
			f.StoppedReason, f.RestartAt = stop.Reason, &at
		} else {
			f.Running = true
		}
		if st := r.status.Load(); st != nil {
			s := *st
			f.Status = &s
		}
	} else if _, err := os.Stat(path); os.IsNotExist(err) {
		f.ConfigNotFound = true
	}
	var err error
	if f.Skipped, err = readAnalyticSkipped(cluster.analyticSkippedPath()); err != nil {
		f.SkippedError = err.Error()
	}
	return f, true
}
