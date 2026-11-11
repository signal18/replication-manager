// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import "github.com/signal18/replication-manager/config"

// seedInstanceName saves the hostname as the middle part of the instance's own name at the
// first start (CREDENTIAL_STORES.md "Two levels"); the company and the location are entered in
// the GUI. The active monitor only: it writes default.toml and pushes it, the standby takes the
// same name from it, so the pair share one name.
func (repman *ReplicationManager) seedInstanceName() {
	if repman.Status != ConstMonitorActif {
		return
	}
	if repman.Conf.SeedInstanceName(repman.Hostname) {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModConfigLoad, config.LvlInfo, "Instance hostname saved for its name: %s (the company and the location are entered in the GUI)", repman.Conf.InstanceSubDomain)
		repman.Save()
	}
}
