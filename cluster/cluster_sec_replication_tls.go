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
)

// replicationTLSWithoutCA reports an encrypted replication channel that verifies its
// primary against no CA: the I/O thread falls back to the system trust store, so the
// channel stays encrypted and healthy while any public CA's certificate is accepted as
// the primary. Nothing in the replica status fails (#1933).
func replicationTLSWithoutCA(rep dbhelper.SlaveStatus) bool {
	return strings.EqualFold(rep.MasterSSLAllowed.String, "Yes") &&
		rep.MasterSSLCAFile.String == "" && rep.MasterSSLCAPath.String == ""
}

// CheckReplicationTLS runs every tick and opens the security WARN0235 per replication
// channel encrypted without a CA. It only reports: re-pointing a running replica is not
// the monitor's to do; the next CHANGE MASTER replication-manager issues carries the CA
// (ServerMonitor.ChangeMaster).
func (cluster *Cluster) CheckReplicationTLS() {
	for _, s := range cluster.Servers {
		if s == nil || s.IsDown() {
			continue
		}
		for _, rep := range s.Replications {
			if !replicationTLSWithoutCA(rep) {
				continue
			}
			from := rep.MasterHost.String + ":" + rep.MasterPort.String
			if rep.ConnectionName.String != "" {
				from += " (channel " + rep.ConnectionName.String + ")"
			}
			cluster.SecurityStateMachine.AddState("WARN0235", state.State{ErrType: "WARNING", ErrKey: "WARN0235", ErrFrom: "SECURITY", ServerUrl: s.URL,
				ErrDesc: fmt.Sprintf(clusterError["WARN0235"], s.URL, from)})
		}
	}
}
