// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"

	"github.com/signal18/replication-manager/config"
)

// ErrFailoverMasterHealthy indicates that a planned role change must use switchover.
var ErrFailoverMasterHealthy = errors.New("Master is still up; use switchover for a planned role change")

// ErrSwitchoverMasterFailed indicates that the current master cannot be used for a planned switchover.
var ErrSwitchoverMasterFailed = errors.New("Master failed")

// Failover guards the manual/API failover path without changing the
// monitoring-driven MasterFailover behavior.
func (cluster *Cluster) Failover() error {
	if !cluster.IsMasterFailed() {
		return ErrFailoverMasterHealthy
	}

	// Preserve MasterFailover's existing result handling for API callers.
	cluster.MasterFailover(true)
	return nil
}

// Switchover applies the legacy REST switchover behavior. An empty or unknown
// preferred master leaves the existing preference intact. Server-specific REST
// callers pass forcePreferred=true because their target was already resolved.
func (cluster *Cluster) Switchover(preferredMaster string, forcePreferred bool) error {
	if preferredMaster != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "API force for prefered master: %s", preferredMaster)
	}

	if cluster.IsMasterFailed() {
		return ErrSwitchoverMasterFailed
	}

	savedPrefMaster := cluster.GetPreferedMasterList()
	defer cluster.SetPrefMaster(savedPrefMaster)

	if forcePreferred || cluster.IsInHostList(preferredMaster) {
		cluster.SetPrefMaster(preferredMaster)
	}
	cluster.MasterFailover(false)
	return nil
}
