package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

type InterventionEntry struct {
	User             string    `json:"user"`
	Reason           string    `json:"reason"`
	Scope            string    `json:"scope"`    // "cluster" or "global"
	StartedAt        time.Time `json:"startedAt"`
	EndedAt          time.Time `json:"endedAt,omitempty"`
	ScheduledAt      time.Time `json:"scheduledAt,omitempty"` // future start time (zero = immediate)
	AutoEndAt        time.Time `json:"autoEndAt,omitempty"`   // auto-unmute time (zero = manual)
	SuppressedAlerts int       `json:"suppressedAlerts"`
}

var interventionMu sync.Mutex

func (cluster *Cluster) StartIntervention(user string, reason string, scope string) error {
	return cluster.StartInterventionAt(user, reason, scope, time.Now(), time.Time{})
}

func (cluster *Cluster) StartInterventionAt(user string, reason string, scope string, startAt time.Time, autoEndAt time.Time) error {
	interventionMu.Lock()
	defer interventionMu.Unlock()

	if cluster.IsIntervention {
		return fmt.Errorf("intervention already active since %s by %s",
			cluster.InterventionCurrent.StartedAt.Format(time.RFC3339), cluster.InterventionCurrent.User)
	}

	// If scheduled in the future, store as pending
	if startAt.After(time.Now().Add(30 * time.Second)) {
		entry := &InterventionEntry{
			User:        user,
			Reason:      reason,
			Scope:       scope,
			ScheduledAt: startAt,
			AutoEndAt:   autoEndAt,
		}
		cluster.InterventionPending = entry
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Intervention scheduled by %s at %s: %s", user, startAt.Format(time.RFC3339), reason)
		cluster.SetState("WARN0172", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["WARN0172"], startAt.Format(time.RFC3339), user, reason), ErrFrom: "INTERVENTION"})
		cluster.SaveInterventionHistory()
		return nil
	}

	entry := &InterventionEntry{
		User:      user,
		Reason:    reason,
		Scope:     scope,
		StartedAt: time.Now(),
		AutoEndAt: autoEndAt,
	}

	cluster.IsIntervention = true
	cluster.InterventionCurrent = entry
	cluster.InterventionSuppressedAlerts = 0

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Intervention started by %s: %s (scope: %s)", user, reason, scope)
	cluster.SetState("WARN0173", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["WARN0173"], entry.StartedAt.Format(time.RFC3339), user, reason), ErrFrom: "INTERVENTION"})

	cluster.SaveInterventionHistory()
	return nil
}

func (cluster *Cluster) EndIntervention(user string) error {
	interventionMu.Lock()
	defer interventionMu.Unlock()

	if !cluster.IsIntervention {
		return fmt.Errorf("no active intervention")
	}

	cluster.InterventionCurrent.EndedAt = time.Now()
	cluster.InterventionCurrent.SuppressedAlerts = cluster.InterventionSuppressedAlerts

	duration := cluster.InterventionCurrent.EndedAt.Sub(cluster.InterventionCurrent.StartedAt)

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Intervention ended by %s. Duration: %s. Suppressed alerts: %d",
		user, duration.Round(time.Second), cluster.InterventionSuppressedAlerts)

	cluster.InterventionHistory = append(cluster.InterventionHistory, *cluster.InterventionCurrent)
	cluster.IsIntervention = false
	cluster.InterventionCurrent = nil
	cluster.InterventionSuppressedAlerts = 0

	cluster.SaveInterventionHistory()
	return nil
}

// CheckInterventionSchedule checks if a pending intervention should start
// or if an active intervention should auto-end. Called from the monitoring loop.
func (cluster *Cluster) CheckInterventionSchedule() {
	// Re-set warning states each tick (state machine is cleared per tick)
	if cluster.InterventionPending != nil && !cluster.IsIntervention {
		pending := cluster.InterventionPending
		cluster.SetState("WARN0172", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["WARN0172"], pending.ScheduledAt.Format(time.RFC3339), pending.User, pending.Reason), ErrFrom: "INTERVENTION"})
	}
	if cluster.IsIntervention && cluster.InterventionCurrent != nil {
		cur := cluster.InterventionCurrent
		cluster.SetState("WARN0173", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(config.ClusterError["WARN0173"], cur.StartedAt.Format(time.RFC3339), cur.User, cur.Reason), ErrFrom: "INTERVENTION"})
	}

	// Check pending → start
	if cluster.InterventionPending != nil && !cluster.IsIntervention {
		if time.Now().After(cluster.InterventionPending.ScheduledAt) {
			pending := cluster.InterventionPending
			cluster.InterventionPending = nil
			cluster.StartInterventionAt(pending.User, pending.Reason, pending.Scope, time.Now(), pending.AutoEndAt)
		}
	}

	// Check active → auto-end
	if cluster.IsIntervention && cluster.InterventionCurrent != nil {
		if !cluster.InterventionCurrent.AutoEndAt.IsZero() && time.Now().After(cluster.InterventionCurrent.AutoEndAt) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
				"Intervention auto-unmute triggered (scheduled end: %s)", cluster.InterventionCurrent.AutoEndAt.Format(time.RFC3339))
			cluster.EndIntervention("auto-unmute")
		}
	}
}

func (cluster *Cluster) IncrementSuppressedAlerts() {
	if cluster.IsIntervention {
		cluster.InterventionSuppressedAlerts++
	}
}

func (cluster *Cluster) SaveInterventionHistory() {
	path := filepath.Join(cluster.WorkingDir, "interventions.json")
	os.MkdirAll(filepath.Dir(path), 0755)

	data := struct {
		Current *InterventionEntry  `json:"current,omitempty"`
		Pending *InterventionEntry  `json:"pending,omitempty"`
		History []InterventionEntry `json:"history"`
	}{
		Current: cluster.InterventionCurrent,
		Pending: cluster.InterventionPending,
		History: cluster.InterventionHistory,
	}

	if bytes, err := json.MarshalIndent(data, "", "  "); err == nil {
		os.WriteFile(path, bytes, 0644)
	}
}

func (cluster *Cluster) LoadInterventionHistory() {
	path := filepath.Join(cluster.WorkingDir, "interventions.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var data struct {
		Current *InterventionEntry  `json:"current,omitempty"`
		Pending *InterventionEntry  `json:"pending,omitempty"`
		History []InterventionEntry `json:"history"`
	}

	if err := json.Unmarshal(bytes, &data); err != nil {
		return
	}

	cluster.InterventionHistory = data.History
	if data.Current != nil {
		cluster.IsIntervention = true
		cluster.InterventionCurrent = data.Current
	}
	if data.Pending != nil {
		cluster.InterventionPending = data.Pending
	}
}
