// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os"
	"testing"
	"time"

	"github.com/signal18/replication-manager/cluster/replplugin/wire"
	"github.com/signal18/replication-manager/config"
)

// AnalyticFeedStatus: what the replication interface's CDC backend reads of a DuckDB child's
// feed, before its seed, while its plugin runs and while it is stopped.
func TestAnalyticFeedStatus(t *testing.T) {
	cl := &Cluster{Name: "shop-duck", Conf: &config.Config{WorkingDir: t.TempDir(), ReplicationPluginSource: "master", ReplicationPluginCheckpointInterval: 10}}
	if _, ok := cl.AnalyticFeedStatus(); ok {
		t.Fatal("a cluster without destination has no feed")
	}
	cl.Conf.ReplicationPluginDestination, cl.Conf.ReplicationPluginTables = "duckdb", "shop.*"
	f, ok := cl.AnalyticFeedStatus()
	if !ok || !f.ConfigNotFound || f.Running || f.Status != nil || f.Destination != "duckdb" || f.Tables != "shop.*" {
		t.Fatalf("before the seed: %+v", f)
	}

	// seeded: the TOML, a table left as it was, a runner with a status line
	if err := os.MkdirAll(cl.replPluginDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cl.analyticPluginPath(), []byte("plugin = \"duckdb\"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := writeAnalyticSkipped(cl.analyticSkippedPath(), []wire.CreateResult{{Table: "shop.nopk", Engine: "InnoDB", Reason: "no primary key"}}); err != nil {
		t.Fatal(err)
	}
	r := &replPluginRunner{path: cl.analyticPluginPath(), cfg: wire.Config{Plugin: "duckdb", Source: wire.Endpoint{Host: "db1"}}}
	r.status.Store(&wire.Status{State: "running", GTID: "0-1-42", AppliedGTID: "0-1-40", LagSeconds: 3, MergedAt: "2026-11-27T10:00:00Z"})
	cl.replPlugins = map[string]*replPluginRunner{r.path: r}
	f, _ = cl.AnalyticFeedStatus()
	if f.ConfigNotFound || !f.Running || f.SourceHost != "db1" || f.Status == nil || f.Status.AppliedGTID != "0-1-40" || f.Status.LagSeconds != 3 || f.Status.MergedAt == "" {
		t.Fatalf("running: %+v", f)
	}
	if len(f.Skipped) != 1 || f.Skipped[0].Table != "shop.nopk" {
		t.Fatalf("skipped: %+v", f.Skipped)
	}
	// a copy: changing it never changes the runner's
	f.Status.GTID = "changed"
	if r.status.Load().GTID != "0-1-42" {
		t.Fatal("the status returned must be a copy")
	}

	// an unreadable file of skipped tables is said, not hidden
	if err := os.WriteFile(cl.analyticSkippedPath(), []byte("{"), 0o640); err != nil {
		t.Fatal(err)
	}
	if f, _ = cl.AnalyticFeedStatus(); f.SkippedError == "" || f.Skipped != nil {
		t.Fatalf("unreadable skipped file: %+v", f)
	}

	// stopped by the supervisor: the reason and the restart due
	at := time.Now().Add(time.Minute)
	r.stopped.Store(&replPluginStop{Reason: "exit 1", Restart: at})
	f, _ = cl.AnalyticFeedStatus()
	if f.Running || f.StoppedReason != "exit 1" || f.RestartAt == nil || !f.RestartAt.Equal(at) {
		t.Fatalf("stopped: %+v", f)
	}
}
