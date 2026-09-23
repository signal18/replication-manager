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
	"fmt"
)

var (
	// ErrFailoverMasterHealthy indicates that a planned role change must use switchover.
	ErrFailoverMasterHealthy = errors.New("Master is still up; use switchover for a planned role change")
	// ErrSwitchoverMasterFailed indicates that the current master cannot be used for a planned switchover.
	ErrSwitchoverMasterFailed = errors.New("Master failed")
	// ErrPreferredMasterNotFound indicates that the requested preferred master is not in the cluster host list.
	ErrPreferredMasterNotFound = errors.New("Preferred master not found")
	// ErrFailoverFailed indicates that the failover operation could not be started or completed.
	ErrFailoverFailed = errors.New("Master failover failed")
	// ErrSwitchoverFailed indicates that the switchover operation could not be started or completed.
	ErrSwitchoverFailed = errors.New("Master switchover failed")
)

// Failover validates and starts a failure-driven master failover.
//
// Manual callers must only use this operation after the current master has
// actually failed. Planned role changes belong to Switchover instead.
func (cluster *Cluster) Failover() error {
	if !cluster.IsMasterFailed() {
		return ErrFailoverMasterHealthy
	}
	if !cluster.MasterFailover(true) {
		return ErrFailoverFailed
	}
	return nil
}

// Switchover validates and starts a planned master switchover.
//
// preferredMaster may be empty to keep the cluster's existing preference.
// Any temporary preference is restored after MasterFailover returns, including
// when the switchover cannot be completed.
func (cluster *Cluster) Switchover(preferredMaster string) error {
	if cluster.IsMasterFailed() {
		return ErrSwitchoverMasterFailed
	}
	if preferredMaster != "" && !cluster.IsInHostList(preferredMaster) {
		return fmt.Errorf("%w: %s", ErrPreferredMasterNotFound, preferredMaster)
	}

	savedPrefMaster := cluster.GetPreferedMasterList()
	defer cluster.SetPrefMaster(savedPrefMaster)

	if preferredMaster != "" {
		cluster.SetPrefMaster(preferredMaster)
	}
	if !cluster.MasterFailover(false) {
		return ErrSwitchoverFailed
	}
	return nil
}
