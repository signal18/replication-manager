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
	"github.com/signal18/replication-manager/utils/meethelper"
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
	meetConnectWarnErrKey                       = "GWARN012"
	// Stall detection counts repman loop ticks (monitoring-ticker, 2s) with an
	// unchanged cluster heartbeat — but a busy cluster tick can legitimately
	// take 10s+ (slow DB connection attempts during an outage), so a tight
	// threshold flaps STALLED/RESUME on every cluster tick. A truly stalled
	// cluster loop stays stalled for minutes: 30 ticks (~60s) detects it fast
	// without flapping. Tunable via monitor-global-heartbeat-stall-threshold.
	defaultMonitorGlobalHeartbeatStallThreshold = 30
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

// ProduceClusterAggregateStates rolls per-cluster conditions up to the
// monitor-level state machine so the global Monitor button reflects fleet
// health: clusters with open blockers (GERR005, error), unprovisioned
// clusters (GWARN009) and unmonitored clusters (GWARN010). States are
// re-asserted every cycle and auto-resolve when the condition clears.
func (repman *ReplicationManager) ProduceClusterAggregateStates() {
	if repman == nil || repman.StateMachine == nil {
		return
	}

	repman.Lock()
	clusterSnapshot := make(map[string]*cluster.Cluster)
	if repman.Clusters != nil {
		for name, cl := range repman.Clusters {
			clusterSnapshot[name] = cl
		}
	}
	repman.Unlock()

	var withBlockers, unprovisioned, unmonitored, pullConfig []string
	for name, cl := range clusterSnapshot {
		if cl == nil {
			continue
		}
		// Unprovisioned or unmonitored clusters carry inherent error states
		// (unreachable databases): they are surfaced as GINF002/GINF003 tags
		// and must not pollute the fleet blocker count.
		if sm := cl.GetStateMachine(); sm != nil && len(sm.GetOpenErrors()) > 0 && cl.IsProvision && !cl.Conf.MonitorPause {
			withBlockers = append(withBlockers, name)
		}
		if !cl.IsProvision {
			unprovisioned = append(unprovisioned, name)
		}
		if cl.Conf.MonitorPause {
			unmonitored = append(unmonitored, name)
		}
		// Same condition as the standby config sync apply loop: this cluster's
		// config is being pulled from the active peer.
		if !cl.IsActive() && cl.Conf.GitConfigSyncStandby {
			pullConfig = append(pullConfig, name)
		}
	}
	sort.Strings(withBlockers)
	sort.Strings(unprovisioned)
	sort.Strings(unmonitored)
	sort.Strings(pullConfig)

	if len(withBlockers) > 0 {
		repman.SetState("GERR005", state.State{
			ErrType: "ERROR",
			ErrKey:  "GERR005",
			ErrDesc: fmt.Sprintf(config.GlobalError["GERR005"], len(withBlockers), strings.Join(withBlockers, ", ")),
			ErrFrom: "REPMAN",
		})
	}
	if len(pullConfig) > 0 {
		repman.SetState("GINF001", state.State{
			ErrType: "INFO",
			ErrKey:  "GINF001",
			ErrDesc: fmt.Sprintf(config.GlobalError["GINF001"], len(pullConfig), strings.Join(pullConfig, ", ")),
			ErrFrom: "REPMAN",
		})
	}
	// Monitor-mode tags: Cloud18 registration and active support chat.
	// Registered means the GitLab SSO login of the instance actually
	// succeeded — InitGitConfig acquired a personal access token (and turns
	// Cloud18 off on auth failure) — not merely that credentials are present
	// in the config. Not registered surfaces as a warning: community
	// features (support chat, marketplace, arbitration, peers) may be
	// disabled without registration.
	registered := repman.Conf != nil && repman.Conf.Cloud18 && repman.Conf.Cloud18GitUser != "" && repman.Conf.GetDecryptedValue("git-acces-token") != ""
	if registered {
		repman.SetState("GINF004", state.State{
			ErrType: "INFO",
			ErrKey:  "GINF004",
			ErrDesc: fmt.Sprintf(config.GlobalError["GINF004"], repman.Conf.Cloud18GitUser, repman.Conf.Cloud18SubscriptionPlan),
			ErrFrom: "REPMAN",
		})
	} else {
		reason := "cloud18 disabled"
		if repman.Conf != nil && repman.Conf.Cloud18 {
			reason = "GitLab SSO login not completed"
		}
		repman.SetState("GWARN014", state.State{
			ErrType: "WARNING",
			ErrKey:  "GWARN014",
			ErrDesc: fmt.Sprintf(config.GlobalError["GWARN014"], reason),
			ErrFrom: "REPMAN",
		})
	}
	if repman.MeetUserID != "" {
		repman.SetState("GINF005", state.State{
			ErrType: "INFO",
			ErrKey:  "GINF005",
			ErrDesc: config.GlobalError["GINF005"],
			ErrFrom: "REPMAN",
		})
	}
	if len(unprovisioned) > 0 {
		repman.SetState("GINF002", state.State{
			ErrType: "INFO",
			ErrKey:  "GINF002",
			ErrDesc: fmt.Sprintf(config.GlobalError["GINF002"], len(unprovisioned), strings.Join(unprovisioned, ", ")),
			ErrFrom: "REPMAN",
		})
	}
	if len(unmonitored) > 0 {
		repman.SetState("GINF003", state.State{
			ErrType: "INFO",
			ErrKey:  "GINF003",
			ErrDesc: fmt.Sprintf(config.GlobalError["GINF003"], len(unmonitored), strings.Join(unmonitored, ", ")),
			ErrFrom: "REPMAN",
		})
	}
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

	// ── Meet / support service probe ────────────────────────────────────────
	meetErr := probeHTTPReachability(client, meethelper.MeetURL)
	if meetErr != nil {
		desc := fmt.Sprintf(config.GlobalError[meetConnectWarnErrKey], meetErr.Error())
		repman.SetState(meetConnectWarnErrKey, state.State{
			ErrType: "WARNING",
			ErrKey:  meetConnectWarnErrKey,
			ErrDesc: desc,
			ErrFrom: "REPMAN",
		})
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"Meet support service unreachable (%s): %s", meethelper.MeetURL, meetErr)
	}
}

// RefreshCreditsFromCRM is retained for future use; credit usage is now computed
// from per-app ProvAppCreditUsed totals and is no longer sourced from CRM.
func (repman *ReplicationManager) RefreshCreditsFromCRM() {
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
