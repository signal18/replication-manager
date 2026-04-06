// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"sort"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

const (
	clusterHeartbeatStalledStatePrefix   = "WARN_RM_CLUSTER_HEARTBEAT_STALLED"
	defaultHeartbeatStallThresholdCycles = int64(5)
)

// InitAlertStateMachine initializes the global alert state machine on ReplicationManager.
// It is safe to call multiple times and follows the same initialization pattern as cluster.
func (repman *ReplicationManager) InitAlertStateMachine() {
	if repman.StateMachine == nil {
		repman.StateMachine = new(state.StateMachine)
		repman.StateMachine.Init()
		// Prime heartbeats so AddState does not mirror CurState into OldState.
		// This keeps opened/resolved lifecycle transitions detectable for
		// ReplicationManager global alerts from the first processed cycle.
		repman.StateMachine.SetMasterUpAndSync(true, true, true)
	}
}

// GetStateMachine returns the global state machine for ReplicationManager.
// This provides parity with cluster-level GetStateMachine().
func (repman *ReplicationManager) GetStateMachine() *state.StateMachine {
	return repman.StateMachine
}

// SetState adds a global state to the ReplicationManager state machine.
// The key format supports per-cluster identity via @<cluster> suffix.
// This provides a simple producer API for opening warning/error states.
func (repman *ReplicationManager) SetState(key string, s state.State) {
	if repman.StateMachine != nil {
		repman.StateMachine.AddState(key, s)
	}
}

// PreserveState preserves matching prior states across state machine cycles.
// This delegates to the underlying state machine's PreserveState method
// and is used for checks that do not run every monitoring cycle.
func (repman *ReplicationManager) PreserveState(keys ...string) {
	if repman.StateMachine != nil {
		repman.StateMachine.PreserveState(keys...)
	}
}

// ProcessAlertStateLifecycle evaluates global resolved/opened state transitions
// and rolls state forward for the next monitoring cycle.
//
// Lifecycle order is deterministic and mirrors cluster behavior:
// 1) evaluate resolved transitions
// 2) evaluate opened transitions
// 3) clear state (roll current -> old)
//
// It is safe to call when no state machine is initialized.
func (repman *ReplicationManager) ProcessAlertStateLifecycle() {
	if repman.StateMachine == nil {
		return
	}

	resolvedStates := repman.StateMachine.GetLastResolvedStates()
	resolvedKeys := make([]string, 0, len(resolvedStates))
	for key := range resolvedStates {
		resolvedKeys = append(resolvedKeys, key)
	}
	sort.Strings(resolvedKeys)
	for _, key := range resolvedKeys {
		if repman.Conf != nil {
			st := resolvedStates[key]
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "STATE", "RESOLV %s : %s", st.ErrKey, st.ErrDesc)
		}
	}

	openedStates := repman.StateMachine.GetLastOpenedStates()
	openedKeys := make([]string, 0, len(openedStates))
	for key := range openedStates {
		openedKeys = append(openedKeys, key)
	}
	sort.Strings(openedKeys)
	for _, key := range openedKeys {
		if repman.Conf != nil {
			st := openedStates[key]
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, "STATE", "OPENED %s : %s", st.ErrKey, st.ErrDesc)
		}
	}

	repman.StateMachine.ClearState()
}

// ProduceClusterHeartbeatSupervisionStates inspects per-cluster state-machine
// heartbeats and emits global stalled-heartbeat warning states after thresholded
// unchanged intervals.
func (repman *ReplicationManager) ProduceClusterHeartbeatSupervisionStates() {
	if repman == nil {
		return
	}

	// Skip if global heartbeat supervision is disabled
	if repman.Conf != nil && !repman.Conf.MonitorGlobalHeartbeatSupervision {
		return
	}

	if repman.ClusterHeartbeatSnapshot == nil {
		repman.ClusterHeartbeatSnapshot = make(map[string]int64)
	}
	if repman.ClusterHeartbeatLastChange == nil {
		repman.ClusterHeartbeatLastChange = make(map[string]int64)
	}

	// Take a snapshot of cluster names under lock to avoid concurrent map access
	repman.Lock()
	var clusterNames []string
	if repman.Clusters != nil {
		for name := range repman.Clusters {
			clusterNames = append(clusterNames, name)
		}
	}
	repman.Unlock()

	if len(clusterNames) == 0 {
		for clusterName := range repman.ClusterHeartbeatSnapshot {
			delete(repman.ClusterHeartbeatSnapshot, clusterName)
		}
		for clusterName := range repman.ClusterHeartbeatLastChange {
			delete(repman.ClusterHeartbeatLastChange, clusterName)
		}
		return
	}

	threshold := repman.getClusterHeartbeatStallThresholdCycles()
	trackedClusters := make(map[string]struct{}, len(clusterNames))

	for _, clusterName := range clusterNames {
		repman.Lock()
		cl := repman.Clusters[clusterName]
		repman.Unlock()
		if cl == nil || cl.GetStateMachine() == nil {
			continue
		}

		trackedClusters[clusterName] = struct{}{}
		currentHeartbeat := cl.GetStateMachine().GetHeartbeats()
		previousHeartbeat, seen := repman.ClusterHeartbeatSnapshot[clusterName]

		if !seen {
			repman.ClusterHeartbeatSnapshot[clusterName] = currentHeartbeat
			repman.ClusterHeartbeatLastChange[clusterName] = 0
			continue
		}

		if currentHeartbeat != previousHeartbeat {
			if repman.ClusterHeartbeatLastChange[clusterName] >= threshold && repman.Conf != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "HB_SUPERVISION_RESUME cluster=%s heartbeat=%d", clusterName, currentHeartbeat)
			}
			repman.ClusterHeartbeatSnapshot[clusterName] = currentHeartbeat
			repman.ClusterHeartbeatLastChange[clusterName] = 0
			continue
		}

		stalledIntervals := repman.ClusterHeartbeatLastChange[clusterName] + 1
		repman.ClusterHeartbeatLastChange[clusterName] = stalledIntervals

		if stalledIntervals >= threshold {
			if stalledIntervals == threshold && repman.Conf != nil {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "HB_SUPERVISION_STALLED cluster=%s heartbeat=%d stalled_intervals=%d threshold=%d", clusterName, currentHeartbeat, stalledIntervals, threshold)
			}

			stateKey := fmt.Sprintf("%s@%s", clusterHeartbeatStalledStatePrefix, clusterName)
			repman.SetState(stateKey, state.State{
				ErrType: "WARNING",
				ErrDesc: fmt.Sprintf("ReplicationManager detected stalled cluster heartbeat for %s", clusterName),
				ErrFrom: "REPMAN",
			})
		}
	}

	for clusterName := range repman.ClusterHeartbeatSnapshot {
		if _, exists := trackedClusters[clusterName]; !exists {
			delete(repman.ClusterHeartbeatSnapshot, clusterName)
			delete(repman.ClusterHeartbeatLastChange, clusterName)
		}
	}
}

func (repman *ReplicationManager) getClusterHeartbeatStallThresholdCycles() int64 {
	if repman != nil && repman.Conf != nil {
		if repman.Conf.MonitorGlobalHeartbeatStallThreshold > 0 {
			return int64(repman.Conf.MonitorGlobalHeartbeatStallThreshold)
		}
	}

	return defaultHeartbeatStallThresholdCycles
}
