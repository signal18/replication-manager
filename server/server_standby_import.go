// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"strings"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
)

// standbyImportInterval bounds how often a standby clones the shared config
// repository to look for clusters created on the active (#1946).
const standbyImportInterval = 10 * time.Minute

// standbyImportState: when the last import started, and whether one is running.
type standbyImportState struct {
	last     atomic.Int64 // unix seconds
	inFlight atomic.Bool
	// run replaces FetchDynamicClustersFromGit in tests.
	run func() (*DynamicClusterImportResult, error)
}

// standbyImportDue tells whether this instance should import, now, the clusters
// of the shared config repository it does not know: a Cloud18 standby of an
// active/standby pair that syncs its config from git, at most once per
// standbyImportInterval. A standby otherwise gets the config of the clusters it
// already knows only; a cluster created on the active after the standby started
// never reached it (preprod DR, 2026-10-09: 4 of 14 clusters missing).
func (repman *ReplicationManager) standbyImportDue(now time.Time) bool {
	c := repman.Conf
	if c == nil || !c.Cloud18 || !c.Arbitration || !c.GitConfigSyncStandby || c.GitUrl == "" {
		return false
	}
	if repman.Status != ConstMonitorStandby {
		return false
	}
	return now.Unix()-repman.standbyImport.last.Load() >= int64(standbyImportInterval/time.Second)
}

// maybeImportClustersOnStandby runs the missing-only, never-overwrite import of
// FetchDynamicClustersFromGit in the background when due; one run at a time.
func (repman *ReplicationManager) maybeImportClustersOnStandby(now time.Time) {
	if !repman.standbyImportDue(now) || !repman.standbyImport.inFlight.CompareAndSwap(false, true) {
		return
	}
	repman.standbyImport.last.Store(now.Unix())
	run := repman.standbyImport.run
	if run == nil {
		run = repman.FetchDynamicClustersFromGit
	}
	go func() {
		defer repman.standbyImport.inFlight.Store(false)
		res, err := run()
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Standby import of new clusters from the config repository failed: %s", err)
			return
		}
		if res == nil {
			return
		}
		if len(res.Imported) > 0 {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlInfo, "Standby imported clusters created on the active: %s", strings.Join(res.Imported, ", "))
		}
		for name, e := range res.Errors {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGit, config.LvlWarn, "Standby import of cluster %s failed: %s", name, e)
		}
	}()
}
