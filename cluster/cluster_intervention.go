package cluster

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
)

type InterventionEntry struct {
	User             string    `json:"user"`
	Reason           string    `json:"reason"`
	Scope            string    `json:"scope"` // "cluster" or "global"
	StartedAt        time.Time `json:"startedAt"`
	EndedAt          time.Time `json:"endedAt,omitempty"`
	SuppressedAlerts int       `json:"suppressedAlerts"`
}

// The reason field contains both description and estimated time
// formatted as: "description (est. 30 minutes)"

var interventionMu sync.Mutex

func (cluster *Cluster) StartIntervention(user string, reason string, scope string) error {
	interventionMu.Lock()
	defer interventionMu.Unlock()

	if cluster.IsIntervention {
		return fmt.Errorf("intervention already active since %s by %s",
			cluster.InterventionCurrent.StartedAt.Format(time.RFC3339), cluster.InterventionCurrent.User)
	}

	entry := &InterventionEntry{
		User:      user,
		Reason:    reason,
		Scope:     scope,
		StartedAt: time.Now(),
	}

	cluster.IsIntervention = true
	cluster.InterventionCurrent = entry
	cluster.InterventionSuppressedAlerts = 0

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Intervention started by %s: %s (scope: %s)", user, reason, scope)

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

func (cluster *Cluster) IncrementSuppressedAlerts() {
	if cluster.IsIntervention {
		cluster.InterventionSuppressedAlerts++
	}
}

func (cluster *Cluster) SaveInterventionHistory() {
	path := filepath.Join(cluster.WorkingDir, cluster.Name, "interventions.json")
	os.MkdirAll(filepath.Dir(path), 0755)

	data := struct {
		Current *InterventionEntry  `json:"current,omitempty"`
		History []InterventionEntry `json:"history"`
	}{
		Current: cluster.InterventionCurrent,
		History: cluster.InterventionHistory,
	}

	if bytes, err := json.MarshalIndent(data, "", "  "); err == nil {
		os.WriteFile(path, bytes, 0644)
	}
}

func (cluster *Cluster) LoadInterventionHistory() {
	path := filepath.Join(cluster.WorkingDir, cluster.Name, "interventions.json")
	bytes, err := os.ReadFile(path)
	if err != nil {
		return
	}

	var data struct {
		Current *InterventionEntry  `json:"current,omitempty"`
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
}
