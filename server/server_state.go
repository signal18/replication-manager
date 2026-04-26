// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Guillaume Lefranc <guillaume@signal18.io>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/config/manager"
	"github.com/signal18/replication-manager/utils/state"
)

const (
	clusterHeartbeatWarnErrKey                  = "GWARN001"
	clusterHeartbeatCriticalErrKey              = "GERR001"
	gitPushWarnErrKey                           = "GWARN002"
	gitPushErrErrKey                            = "GERR002"
	gitPullWarnErrKey                           = "GWARN003"
	gitPullErrErrKey                            = "GERR003"
	gitlabConnectWarnErrKey                     = "GWARN004"
	crmConnectWarnErrKey                        = "GWARN005"
	defaultMonitorGlobalHeartbeatStallThreshold = 5
	heartbeatCriticalThresholdMultiplier        = int64(3)

	// cloud18ConnectivityProbeTimeout is the per-request deadline for the
	// lightweight HTTP probe used by ProduceCloud18ConnectivityStates.
	cloud18ConnectivityProbeTimeout = 10 * time.Second
)

type clusterHeartbeatTracker struct {
	Snapshot      int64
	StalledCycles int64
}

type clusterHeartbeatObservation struct {
	currentHeartbeat int64
	stalledCycles    int64
	shouldLogWarn    bool
	shouldSetWarn    bool
	shouldLogCrit    bool
	shouldSetCrit    bool
	shouldLogResume  bool
}

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
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "RESOLV %s : %s", st.ErrKey, st.ErrDesc)
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
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "OPENED %s : %s", st.ErrKey, st.ErrDesc)
		}
	}

	repman.StateMachine.ClearState()
}

func (repman *ReplicationManager) ProduceGitSupervisionStates() {
	if repman == nil || repman.StateMachine == nil || repman.ConfigManager == nil {
		return
	}

	snapshot := repman.ConfigManager.GetGitHealthSnapshot()
	// Keep a single global key scope (@git) and distinguish push vs pull
	// with dedicated error codes (GWARN/GERR 002 for push, 003 for pull).
	repman.produceGitOperationSupervisionState("push", snapshot.Push, gitPushWarnErrKey, gitPushErrErrKey)
	repman.produceGitOperationSupervisionState("pull", snapshot.Pull, gitPullWarnErrKey, gitPullErrErrKey)
}

func (repman *ReplicationManager) produceGitOperationSupervisionState(operation string, operationStatus manager.GitOperationHealth, warnErrKey, errErrKey string) {
	if !operationStatus.HasFailure {
		return
	}

	severity := "ERROR"
	errKey := errErrKey
	if operationStatus.Recoverable {
		severity = "WARNING"
		errKey = warnErrKey
	}

	reason := operationStatus.Reason
	if reason == "" {
		reason = operationStatus.Details
	}
	// Guard template lookup so alert descriptions stay valid even if a future
	// refactor removes/renames a GlobalError key. When mapping exists, use the
	// operation-specific template from config/error.go; otherwise fall back.
	tmpl, ok := config.GlobalError[errKey]
	errDesc := ""
	if ok && tmpl != "" {
		errDesc = fmt.Sprintf(tmpl, reason)
	} else {
		if operationStatus.Recoverable {
			tmpl = "ReplicationManager git %s reported warning-level failure: %s"
		} else {
			tmpl = "ReplicationManager git %s reported persistent failure: %s"
		}
		errDesc = fmt.Sprintf(tmpl, operation, reason)
	}

	repman.SetState(fmt.Sprintf("%s@git", errKey), state.State{
		ErrType: severity,
		ErrKey:  errKey,
		ErrDesc: errDesc,
		ErrFrom: "REPMAN",
	})
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

	// Take a snapshot of clusters under repman lock to avoid concurrent map access.
	repman.Lock()
	clusterSnapshot := make(map[string]*cluster.Cluster)
	if repman.Clusters != nil {
		for name, cl := range repman.Clusters {
			clusterSnapshot[name] = cl
		}
	}
	repman.Unlock()

	if len(clusterSnapshot) == 0 {
		repman.resetClusterHeartbeatTracking()
		return
	}

	threshold := repman.getClusterHeartbeatStallThresholdCycles()
	criticalThreshold := repman.getClusterHeartbeatCriticalThresholdCycles(threshold)
	trackedClusters := make(map[string]struct{}, len(clusterSnapshot))

	for clusterName, cl := range clusterSnapshot {
		if cl == nil || cl.GetStateMachine() == nil {
			continue
		}

		trackedClusters[clusterName] = struct{}{}
		currentHeartbeat := cl.GetStateMachine().GetHeartbeats()
		observation := repman.observeClusterHeartbeat(clusterName, currentHeartbeat, threshold, criticalThreshold)

		if observation.shouldLogResume && repman.Conf != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "HB_SUPERVISION_RESUME cluster=%s heartbeat=%d", clusterName, observation.currentHeartbeat)
		}

		if observation.shouldLogWarn && repman.Conf != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn, "HB_SUPERVISION_STALLED cluster=%s heartbeat=%d stalled_intervals=%d threshold=%d", clusterName, observation.currentHeartbeat, observation.stalledCycles, threshold)
		}

		if observation.shouldLogCrit && repman.Conf != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "HB_SUPERVISION_CRITICAL cluster=%s heartbeat=%d stalled_intervals=%d critical_threshold=%d", clusterName, observation.currentHeartbeat, observation.stalledCycles, criticalThreshold)
		}

		if observation.shouldSetWarn {
			stateKey := fmt.Sprintf("%s@%s", clusterHeartbeatWarnErrKey, clusterName)
			errDesc := fmt.Sprintf(config.GlobalError[clusterHeartbeatWarnErrKey], clusterName)
			repman.SetState(stateKey, state.State{
				ErrType: "WARNING",
				ErrKey:  clusterHeartbeatWarnErrKey,
				ErrDesc: errDesc,
				ErrFrom: "REPMAN",
			})
		}

		if observation.shouldSetCrit {
			stateKey := fmt.Sprintf("%s@%s", clusterHeartbeatCriticalErrKey, clusterName)
			errDesc := fmt.Sprintf(config.GlobalError[clusterHeartbeatCriticalErrKey], clusterName)
			repman.SetState(stateKey, state.State{
				ErrType: "ERROR",
				ErrKey:  clusterHeartbeatCriticalErrKey,
				ErrDesc: errDesc,
				ErrFrom: "REPMAN",
			})
		}
	}

	repman.pruneClusterHeartbeatTracking(trackedClusters)
}

func (repman *ReplicationManager) ensureClusterHeartbeatTrackingInitLocked() {
	if repman.clusterHeartbeatTracking == nil {
		repman.clusterHeartbeatTracking = make(map[string]clusterHeartbeatTracker)
	}
}

func (repman *ReplicationManager) observeClusterHeartbeat(clusterName string, currentHeartbeat, threshold, criticalThreshold int64) clusterHeartbeatObservation {
	repman.clusterHeartbeatTrackingLock.Lock()
	defer repman.clusterHeartbeatTrackingLock.Unlock()

	repman.ensureClusterHeartbeatTrackingInitLocked()

	tracker, seen := repman.clusterHeartbeatTracking[clusterName]
	if !seen {
		repman.clusterHeartbeatTracking[clusterName] = clusterHeartbeatTracker{Snapshot: currentHeartbeat}
		return clusterHeartbeatObservation{currentHeartbeat: currentHeartbeat}
	}

	if currentHeartbeat != tracker.Snapshot {
		shouldLogResume := tracker.StalledCycles >= threshold
		repman.clusterHeartbeatTracking[clusterName] = clusterHeartbeatTracker{Snapshot: currentHeartbeat}
		return clusterHeartbeatObservation{
			currentHeartbeat: currentHeartbeat,
			shouldLogResume:  shouldLogResume,
		}
	}

	tracker.StalledCycles++
	repman.clusterHeartbeatTracking[clusterName] = tracker

	return clusterHeartbeatObservation{
		currentHeartbeat: currentHeartbeat,
		stalledCycles:    tracker.StalledCycles,
		shouldLogWarn:    tracker.StalledCycles == threshold,
		shouldSetWarn:    tracker.StalledCycles >= threshold && tracker.StalledCycles < criticalThreshold,
		shouldLogCrit:    tracker.StalledCycles == criticalThreshold,
		shouldSetCrit:    tracker.StalledCycles >= criticalThreshold,
	}
}

func (repman *ReplicationManager) getClusterHeartbeatCriticalThresholdCycles(warnThreshold int64) int64 {
	if warnThreshold <= 0 {
		warnThreshold = int64(defaultMonitorGlobalHeartbeatStallThreshold)
	}

	return warnThreshold * heartbeatCriticalThresholdMultiplier
}

func (repman *ReplicationManager) resetClusterHeartbeatTracking() {
	repman.clusterHeartbeatTrackingLock.Lock()
	defer repman.clusterHeartbeatTrackingLock.Unlock()

	repman.ensureClusterHeartbeatTrackingInitLocked()
	for clusterName := range repman.clusterHeartbeatTracking {
		delete(repman.clusterHeartbeatTracking, clusterName)
	}
}

func (repman *ReplicationManager) pruneClusterHeartbeatTracking(trackedClusters map[string]struct{}) {
	repman.clusterHeartbeatTrackingLock.Lock()
	defer repman.clusterHeartbeatTrackingLock.Unlock()

	repman.ensureClusterHeartbeatTrackingInitLocked()
	for clusterName := range repman.clusterHeartbeatTracking {
		if _, exists := trackedClusters[clusterName]; !exists {
			delete(repman.clusterHeartbeatTracking, clusterName)
		}
	}
}

func (repman *ReplicationManager) clusterHeartbeatTrackingSnapshotForTest() map[string]clusterHeartbeatTracker {
	repman.clusterHeartbeatTrackingLock.RLock()
	defer repman.clusterHeartbeatTrackingLock.RUnlock()

	if repman.clusterHeartbeatTracking == nil {
		return map[string]clusterHeartbeatTracker{}
	}

	copyTracking := make(map[string]clusterHeartbeatTracker, len(repman.clusterHeartbeatTracking))
	for clusterName, tracker := range repman.clusterHeartbeatTracking {
		copyTracking[clusterName] = tracker
	}

	return copyTracking
}

func (repman *ReplicationManager) getClusterHeartbeatStallThresholdCycles() int64 {
	if repman != nil && repman.Conf != nil {
		if repman.Conf.MonitorGlobalHeartbeatStallThreshold > 0 {
			return int64(repman.Conf.MonitorGlobalHeartbeatStallThreshold)
		}
	}

	return int64(defaultMonitorGlobalHeartbeatStallThreshold)
}

// ProduceCloud18ConnectivityStates probes the GitLab identity provider and the
// CRM API for reachability and opens/closes GWARN004 / GWARN005 global alert
// states accordingly.  It is a no-op when Cloud18 is not enabled.
//
// The probe is a lightweight HEAD (GitLab OAuth endpoint) / GET (CRM health)
// with a short timeout so it never blocks the monitoring loop.  Both checks
// run in the same goroutine — the total worst-case overhead per call is
// 2 × cloud18ConnectivityProbeTimeout.
func (repman *ReplicationManager) ProduceCloud18ConnectivityStates() {
	if repman == nil || repman.Conf == nil || !repman.Conf.Cloud18 {
		return
	}

	client := &http.Client{Timeout: cloud18ConnectivityProbeTimeout}

	// ── GitLab probe ────────────────────────────────────────────────────────
	gitlabURL := repman.Conf.OAuthProvider
	if gitlabURL == "" {
		gitlabURL = "https://gitlab.signal18.io"
	}
	gitlabProbeURL := gitlabURL + "/users/sign_in"

	gitlabErr := probeHTTPReachability(client, gitlabProbeURL)
	if gitlabErr != nil {
		desc := fmt.Sprintf(config.GlobalError[gitlabConnectWarnErrKey], gitlabErr.Error())
		repman.SetState(gitlabConnectWarnErrKey, state.State{
			ErrType: "WARNING",
			ErrKey:  gitlabConnectWarnErrKey,
			ErrDesc: desc,
			ErrFrom: "REPMAN",
		})
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Cloud18 GitLab unreachable (%s): %s", gitlabProbeURL, gitlabErr)
	}

	// ── CRM API probe ────────────────────────────────────────────────────────
	crmURL := repman.Conf.Cloud18CrmApiUrl
	if crmURL != "" {
		crmProbeURL := strings.TrimRight(crmURL, "/") + "/api/health"
		crmErr := probeHTTPReachability(client, crmProbeURL)
		if crmErr != nil {
			desc := fmt.Sprintf(config.GlobalError[crmConnectWarnErrKey], crmErr.Error())
			repman.SetState(crmConnectWarnErrKey, state.State{
				ErrType: "WARNING",
				ErrKey:  crmConnectWarnErrKey,
				ErrDesc: desc,
				ErrFrom: "REPMAN",
			})
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Cloud18 CRM API unreachable (%s): %s", crmProbeURL, crmErr)
		}
	}
}

// probeHTTPReachability performs a HEAD request to url and returns an error if
// the host is unreachable or returns a 5xx server error.  4xx responses are
// treated as reachable (the endpoint exists; access control is expected).
func probeHTTPReachability(client *http.Client, url string) error {
	resp, err := client.Head(url)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return nil
}
