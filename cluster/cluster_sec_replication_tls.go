// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// replicationTLSUnverified reports an encrypted replication channel whose primary is not
// verified: no CA and either MASTER_SSL_VERIFY_SERVER_CERT off, or a release without the
// 11.4 zero-configuration verification (MySQL, MariaDB before 11.4), where an empty CA
// falls back to the system trust store and any public CA's certificate is accepted as the
// primary. On MariaDB 11.4+ with verification on, an empty CA is verified through the
// replication password (tested on 12.3, #1933): no warning.
func replicationTLSUnverified(rep dbhelper.SlaveStatus, replica *version.Version) bool {
	if !strings.EqualFold(rep.MasterSSLAllowed.String, "Yes") || rep.MasterSSLCAFile.String != "" || rep.MasterSSLCAPath.String != "" {
		return false
	}
	if !strings.EqualFold(rep.MasterSSLVerifyServerCert.String, "Yes") {
		return true
	}
	return !hasZeroConfigTLS(replica)
}

// replicationTLSHandshakeFailed: the I/O thread fails on TLS, error 2026. Measured on
// dev3 (MariaDB 12.3): a CA that did not sign the primary's certificate and a CA file
// that does not exist both answer 2026.
func replicationTLSHandshakeFailed(rep dbhelper.SlaveStatus) bool {
	return rep.LastIOErrno.String == "2026"
}

// hasZeroConfigTLS: MariaDB 11.4+ verifies a server certificate without a CA through the
// account password.
func hasZeroConfigTLS(v *version.Version) bool {
	return v != nil && v.IsMariaDB() && v.GreaterEqual("11.4")
}

// CheckReplicationTLS runs every tick. Per replication channel it opens the security
// WARN0235 when the primary is not verified (replicationTLSUnverified), and WARN0236 when
// the channel fails its TLS handshake (error 2026), the trace of a CA that did not sign
// the primary's certificate. It covers every channel, managed by replication-manager or
// set by hand, whatever replication-use-ssl: it is a security state. It only reports:
// re-pointing a running replica is not the monitor's to do; a switchover or a rejoin
// issues the CHANGE MASTER that carries the TLS decision (ServerMonitor.ChangeMaster).
func (cluster *Cluster) CheckReplicationTLS() {
	for _, s := range cluster.Servers {
		if s == nil || s.IsDown() {
			continue
		}
		for _, rep := range s.Replications {
			from := rep.MasterHost.String + ":" + rep.MasterPort.String
			if rep.ConnectionName.String != "" {
				from += " (channel " + rep.ConnectionName.String + ")"
			}
			if replicationTLSHandshakeFailed(rep) {
				ca := rep.MasterSSLCAFile.String
				if ca == "" {
					ca = "none"
				}
				cluster.SecurityStateMachine.AddState("WARN0236", state.State{ErrType: "WARNING", ErrKey: "WARN0236", ErrFrom: "SECURITY", ServerUrl: s.URL,
					ErrDesc: fmt.Sprintf(clusterError["WARN0236"], s.URL, from, ca)})
			}
			if replicationTLSUnverified(rep, s.DBVersion) {
				cluster.SecurityStateMachine.AddState("WARN0235", state.State{ErrType: "WARNING", ErrKey: "WARN0235", ErrFrom: "SECURITY", ServerUrl: s.URL,
					ErrDesc: fmt.Sprintf(clusterError["WARN0235"], s.URL, from)})
			}
		}
	}
}
