// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// StagingCreation is the progress of create-staging on the new cluster (#1988): the
// step running, then either done or the step it stopped at with the reason. Kept on the
// Cluster (not a ServerMonitor, rebuilt at every reload) and raised as a state every
// tick, since a one-shot state would be gone at the next tick.
type StagingCreation struct {
	Parent string `json:"parent"`
	Step   string `json:"step"`
	Error  string `json:"error,omitempty"`
	Done   bool   `json:"done"`
}

// SetStagingCreation records the current step or the outcome of create-staging.
func (cluster *Cluster) SetStagingCreation(sc StagingCreation) {
	cluster.stagingCreation.Store(&sc)
}

// GetStagingCreation returns the create-staging progress, nil for a cluster not
// created that way (or created before the last replication-manager start).
func (cluster *Cluster) GetStagingCreation() *StagingCreation {
	return cluster.stagingCreation.Load()
}

// checkStagingCreationStates raises CINF0014 while create-staging runs and WARN0238
// once it stopped on an error; nothing once it is done.
func (cluster *Cluster) checkStagingCreationStates() {
	sc := cluster.GetStagingCreation()
	switch {
	case sc == nil || sc.Done:
	case sc.Error != "":
		cluster.SetState("WARN0238", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["WARN0238"], cluster.Name, sc.Parent, sc.Step, sc.Error), ErrFrom: "PROV"})
	default:
		cluster.SetState("CINF0014", state.State{ErrType: config.LvlInfo, ErrDesc: fmt.Sprintf(clusterError["CINF0014"], cluster.Name, sc.Parent, sc.Step), ErrFrom: "PROV"})
	}
}
