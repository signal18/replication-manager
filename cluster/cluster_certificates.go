// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// certificateCompletion is the state of the background completion: the last attempt, why the set
// is not whole yet, and whether one is running.
type certificateCompletion struct {
	// gen serialises the completions: the start's and a tick's ran together on a dev instance,
	// and two runs each generating a key could sign a certificate for the other's key
	gen     sync.Mutex
	mu      sync.Mutex
	tried   time.Time
	why     string
	running atomic.Bool
}

// certificateCompleteInterval spaces the background completions: an RSA 4096 key costs seconds,
// and a set that cannot be completed yet (no servers) is tried again, not every tick.
const certificateCompleteInterval = time.Minute

// CheckCertificates keeps the cluster's generated TLS set whole (#2360). While a file is missing,
// WARN0318 is open in the security domain, with what is missing and why it is not completed yet,
// and its remediation (FixSecState) is offered. With db-servers-tls-complete-generated-cert on
// (the default) the remediation also runs by itself, in the background, at most once a minute:
// never on the tick (F2), never overwriting a file (createKeys). The state closes on the next
// tick after the set is whole.
func (cluster *Cluster) CheckCertificates() {
	if cluster.SecurityStateMachine == nil {
		return
	}
	missing := cluster.missingCertificateFiles()
	if len(missing) == 0 {
		return
	}
	c := &cluster.certificates
	c.mu.Lock()
	why := c.why
	due := time.Since(c.tried) >= certificateCompleteInterval
	c.mu.Unlock()
	if why == "" {
		why = "not completed yet"
	}
	cluster.SecurityStateMachine.AddState("WARN0318", state.State{ErrType: "WARNING", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0318"], strings.Join(missing, ", "), why)})
	if !cluster.Conf.DBServersTLSCompleteGeneratedCertificate || !due || !c.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer c.running.Store(false)
		cluster.completeCertificates()
	}()
}

// completeCertificates runs createKeys and records the outcome for WARN0318. It is the
// remediation of WARN0318, automatic or asked (FixSecState).
func (cluster *Cluster) completeCertificates() error {
	c := &cluster.certificates
	c.gen.Lock()
	defer c.gen.Unlock()
	before := cluster.missingCertificateFiles()
	err := cluster.createKeys()
	c.mu.Lock()
	c.tried = time.Now()
	if err != nil {
		c.why = err.Error()
	} else {
		c.why = ""
	}
	c.mu.Unlock()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlDbg, "Cluster TLS set not completed: %s", err)
		return err
	}
	if len(before) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Cluster TLS set completed: wrote %s, signed by the cluster's CA", strings.Join(before, ", "))
	}
	return nil
}
