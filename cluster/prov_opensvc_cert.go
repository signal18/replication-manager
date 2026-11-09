// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/state"
)

// errOpenSVCCertNotLoaded is returned by the orchestrator secret sink when the cluster's OpenSVC
// p12 could not be loaded: the agent is not called, and the store-behind state names this cause
// instead of a TLS error after a timeout.
var errOpenSVCCertNotLoaded = errors.New("OpenSVC cluster certificate not loaded (WARN0099)")

// openSVCCertState is the cluster's view of its OpenSVC client certificate, shared by every
// OpenSVCConnect() caller (API handlers, jobs, the monitor tick, the secret worker) under one
// mutex. It replaces a bare bool that those goroutines read and wrote unlocked.
//
// It also tracks the p12 fingerprint: om3 caches one HTTP client per certificate for the life of
// the process and never evicts it, so when the fingerprint changes (a rotated p12 or password)
// the cache is purged and the old client and its connections are released (F4).
type openSVCCertState struct {
	mu          sync.Mutex
	failed      bool
	fingerprint string
}

// purgeOpenSVCClients drops om3's cached agent clients; a variable so a test can count the
// purges without emptying the process-wide cache.
var purgeOpenSVCClients = opensvc.PurgeV3Clients

// observe records one load under the mutex and reports whether it is the first failure after a
// success (or at start) and whether a successful load carries a new fingerprint. The first
// successful load sets the fingerprint without counting as a rotation.
func (c *openSVCCertState) observe(p12 []byte, password string, loadErr error) (firstFailure, rotated bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if loadErr != nil {
		firstFailure = !c.failed
		c.failed = true
		return firstFailure, false
	}
	c.failed = false
	fp := opensvc.P12Fingerprint(p12, password)
	rotated = c.fingerprint != "" && c.fingerprint != fp
	c.fingerprint = fp
	return false, rotated
}

// recordOpenSVCCertLoad records the result of one p12 load. On a failure it sets WARN0099 and
// logs at ERR only on the OK->failed transition (a missing p12 on a hot loop once produced ~8K
// Slack messages a day, 2026-09); the state carries the persistent condition. On a success
// whose fingerprint differs from the last loaded one, it purges om3's client cache. The purge
// and the logs run after the mutex is released.
func (cluster *Cluster) recordOpenSVCCertLoad(p12 []byte, password string, loadErr error) {
	firstFailure, rotated := cluster.openSVCCert.observe(p12, password, loadErr)

	if loadErr != nil {
		cluster.SetState("WARN0099", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0099"], cluster.Conf.ProvOpensvcP12Certificate, loadErr), ErrFrom: "OpenSVC"})
		level := config.LvlDbg
		if firstFailure {
			level = config.LvlErr
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, level, "Cannot load OpenSVC cluster certificate %s ", loadErr)
		return
	}
	if rotated {
		purgeOpenSVCClients()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OpenSVC cluster certificate %s changed, cached agent clients purged", cluster.Conf.ProvOpensvcP12Certificate)
		return
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Load OpenSVC cluster certificate %s ", cluster.Conf.ProvOpensvcP12Certificate)
}

// openSVCCertFailed reports whether the last p12 load failed.
func (cluster *Cluster) openSVCCertFailed() bool {
	c := &cluster.openSVCCert
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed
}
