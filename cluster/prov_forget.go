// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// ForgetInstance is what a successful unprovision owes the NEXT provision: a repman
// that holds nothing of the instance's previous life (Stéphane 2026-09-30, dev3: a
// reprovisioned db2 came up as a fresh standalone and its automatic rejoin stopped on a
// rejoin outcome recorded for the old db2 on 2026-09-19 -- the one-shot guard read it
// as "this event already ended"). The orchestrator side is a clean slate; this makes
// repman's side one too, on disk AND in memory, without a restart:
//
//   - every FILE of the server datadir (<cluster>/<host_port>: cookies, preserved/delta/
//     agreed cnf, rendered config, jobs state, binlog metadata, serverstate, logs) is
//     deleted, the directory tree itself is kept (Stéphane: keep the structure, empty
//     it), the log tailers re-armed on the new files;
//   - every crash event whose failed server is this instance is deleted from disk
//     (its crash-bin archive with the binlog delta, and the legacy failover.<ts>.json
//     metadata) and the in-memory history is rebuilt from what remains on disk;
//   - the per-instance memory is reset: fail count, reseeding and rejoin flags, the
//     resize in-flight marks, the DBU consumed reading in the ResourceManager.
//
// The declared side (config, plan, agents) is untouched: it is what the next provision
// builds from. Nothing here is a retry policy: the rejoin stays one-shot per event, an
// unprovisioned instance simply has no events any more.
func (cluster *Cluster) ForgetInstance(server *ServerMonitor) {
	if cluster == nil || server == nil {
		return
	}
	// 1. datadir: every file goes, the tree stays; re-arm the tailers (their files are gone).
	if server.Datadir != "" && strings.HasPrefix(server.Datadir, cluster.Conf.WorkingDir) {
		if err := emptyDirKeepTree(server.Datadir); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Forget %s: cannot empty datadir %s: %s", server.URL, server.Datadir, err)
		}
		os.MkdirAll(server.Datadir+"/log", os.ModePerm)
		os.MkdirAll(server.Datadir+"/var", os.ModePerm)
		os.MkdirAll(server.Datadir+"/init", os.ModePerm)
		server.InitLogTailers()
	}
	// 2. crash history: the events of the old life go, memory rebuilt from disk.
	removed := cluster.deleteCrashEventsOf(server.URL)
	cluster.Lock()
	kept := make(crashList, 0, len(cluster.Crashes))
	for _, cr := range cluster.Crashes {
		if cr != nil && cr.URL != server.URL {
			kept = append(kept, cr)
		}
	}
	cluster.Crashes = kept
	cluster.Unlock()
	cluster.LoadFailoverHistory()
	// 3. memory: the instance starts its life with nothing tracked.
	server.FailCount = 0
	server.reseedMutex.Lock()
	server.IsReseeding = ""
	server.reseedMutex.Unlock()
	server.reseedFromRejoin.Store(false)
	server.PendingCgroupShrink = false
	server.BufferPoolMemGrowDue = false
	server.bufferPoolPressureSince = time.Time{}
	server.ResourceConsumedOverConfigAxes = nil
	server.ResourceConsumedUnderConfigAxes = nil
	server.ResourceConsumedOverPlanAxes = nil
	server.ResourceConsumedUnderPlanAxes = nil
	server.DBUConsumed = nil
	if cluster.resources != nil {
		cluster.resources.SetConsumed(ResourceKey{Cluster: cluster.Name, Server: server.URL}, nil)
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Forgot instance %s after unprovision: datadir recreated empty, %d crash event(s) of its old life deleted, tracked state reset", server.URL, removed)
}

// emptyDirKeepTree deletes every file (and symlink) under dir, recursively, and keeps
// every directory: the structure survives, the content of the old life does not.
func emptyDirKeepTree(dir string) error {
	var firstErr error
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if firstErr == nil {
				firstErr = walkErr
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if rmErr := os.Remove(path); rmErr != nil && firstErr == nil {
			firstErr = rmErr
		}
		return nil
	})
	if err != nil {
		return err
	}
	return firstErr
}

// ForgetProxyInstance is ForgetInstance for a proxy: every file of its datadir is
// deleted, the tree kept (proxies hold no crash history).
func (cluster *Cluster) ForgetProxyInstance(prx DatabaseProxy) {
	if cluster == nil || prx == nil {
		return
	}
	dir := prx.GetDatadir()
	if dir == "" || !strings.HasPrefix(dir, cluster.Conf.WorkingDir) {
		return
	}
	if err := emptyDirKeepTree(dir); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Forget proxy %s: cannot empty datadir %s: %s", prx.GetName(), dir, err)
	}
	if p, ok := prx.(interface{ SetDataDir() }); ok {
		p.SetDataDir() // recreates dir, log/ and var/ when missing
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Forgot proxy %s after unprovision: datadir recreated empty", prx.GetName())
}

// deleteCrashEventsOf removes from disk every crash event whose failed server is url:
// the crash-bin archive directory (crash.json + binlog delta) and any legacy
// failover.<ts>.json metadata naming it. Returns the number of events deleted.
func (cluster *Cluster) deleteCrashEventsOf(url string) int {
	n := 0
	for _, cr := range cluster.FailoverHistory {
		if cr == nil || cr.URL != url {
			continue
		}
		if cr.ArchiveDir != "" && strings.HasPrefix(cr.ArchiveDir, cluster.WorkingDir) {
			if err := os.RemoveAll(cr.ArchiveDir); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Forget %s: cannot delete crash archive %s: %s", url, cr.ArchiveDir, err)
				continue
			}
			n++
		}
	}
	// Legacy metadata files (failover.<ts>.json) that name this server.
	if entries, err := os.ReadDir(cluster.WorkingDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasPrefix(name, "failover") || !strings.HasSuffix(name, ".json") {
				continue
			}
			path := cluster.WorkingDir + "/" + name
			cr := &Crash{}
			if data, rerr := os.ReadFile(path); rerr == nil && json.Unmarshal(data, cr) == nil && cr.URL == url {
				if rmErr := os.Remove(path); rmErr == nil {
					n++
				}
			}
		}
	}
	return n
}
