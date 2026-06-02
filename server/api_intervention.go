package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/signal18/replication-manager/cluster"
)

// RefreshGlobalInterventionState counts active and pending interventions across all clusters
// and restores the global intervention flag on restart.
func (repman *ReplicationManager) RefreshGlobalInterventionState() {
	activeCount := 0
	globalActiveCount := 0
	globalPendingCount := 0
	for _, cl := range repman.Clusters {
		if cl.IsIntervention {
			activeCount++
			if cl.InterventionCurrent != nil && cl.InterventionCurrent.Scope == "global" {
				globalActiveCount++
			}
		}
		if cl.InterventionPending != nil && cl.InterventionPending.Scope == "global" {
			globalPendingCount++
		}
	}
	repman.ActiveInterventionCount = activeCount

	// Restore global flag from active interventions (restart recovery)
	if !repman.IsGlobalIntervention && globalActiveCount > 0 && globalActiveCount == len(repman.Clusters) {
		repman.IsGlobalIntervention = true
		for _, cl := range repman.Clusters {
			if cl.InterventionCurrent != nil && cl.InterventionCurrent.Scope == "global" {
				repman.GlobalInterventionEntry = cl.InterventionCurrent
				break
			}
		}
	}

	// Restore global pending flag (restart recovery)
	if !repman.IsGlobalIntervention && !repman.IsGlobalInterventionPending && globalPendingCount > 0 && globalPendingCount == len(repman.Clusters) {
		repman.IsGlobalInterventionPending = true
		for _, cl := range repman.Clusters {
			if cl.InterventionPending != nil && cl.InterventionPending.Scope == "global" {
				repman.GlobalInterventionEntry = cl.InterventionPending
				break
			}
		}
	}

	// Transition: pending became active on all clusters
	if repman.IsGlobalInterventionPending && globalActiveCount > 0 && globalPendingCount == 0 {
		repman.IsGlobalInterventionPending = false
		repman.IsGlobalIntervention = true
		for _, cl := range repman.Clusters {
			if cl.InterventionCurrent != nil && cl.InterventionCurrent.Scope == "global" {
				repman.GlobalInterventionEntry = cl.InterventionCurrent
				break
			}
		}
	}

	// Transition: active intervention ended on all clusters
	if repman.IsGlobalIntervention && globalActiveCount == 0 {
		repman.IsGlobalIntervention = false
		repman.GlobalInterventionEntry = nil
	}
}

func (repman *ReplicationManager) handlerMuxGlobalInterventionStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if valid, _ := repman.IsValidClusterACL(r, repman.currentCluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var body struct {
		Reason  string `json:"reason"`
		StartAt string `json:"startAt"`
		EndAt   string `json:"endAt"`
	}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	if body.Reason == "" {
		body.Reason = "Global intervention"
	}

	if repman.IsGlobalIntervention {
		http.Error(w, fmt.Sprintf("Global intervention already active since %s by %s",
			repman.GlobalInterventionEntry.StartedAt.Format(time.RFC3339),
			repman.GlobalInterventionEntry.User), http.StatusConflict)
		return
	}
	if repman.IsGlobalInterventionPending {
		http.Error(w, fmt.Sprintf("Global intervention already scheduled at %s by %s",
			repman.GlobalInterventionEntry.ScheduledAt.Format(time.RFC3339),
			repman.GlobalInterventionEntry.User), http.StatusConflict)
		return
	}

	startAt := time.Now()
	if body.StartAt != "" {
		if t, err := time.Parse(time.RFC3339, body.StartAt); err == nil {
			startAt = t
		}
	}
	var autoEndAt time.Time
	if body.EndAt != "" {
		if t, err := time.Parse(time.RFC3339, body.EndAt); err == nil {
			autoEndAt = t
		}
	}

	user := repman.GetUserFromRequest(r)
	isScheduled := startAt.After(time.Now().Add(30 * time.Second))

	if isScheduled {
		repman.IsGlobalInterventionPending = true
		repman.GlobalInterventionEntry = &cluster.InterventionEntry{
			User:        user,
			Reason:      body.Reason,
			Scope:       "global",
			ScheduledAt: startAt,
			AutoEndAt:   autoEndAt,
		}
	} else {
		repman.IsGlobalIntervention = true
		repman.GlobalInterventionEntry = &cluster.InterventionEntry{
			User:      user,
			Reason:    body.Reason,
			Scope:     "global",
			StartedAt: startAt,
			AutoEndAt: autoEndAt,
		}
	}

	// Start or schedule intervention on all clusters
	for _, cl := range repman.Clusters {
		cl.StartInterventionAt(user, body.Reason, "global", startAt, autoEndAt)
	}

	if isScheduled {
		w.Write([]byte(fmt.Sprintf("Global intervention scheduled at %s", startAt.Format(time.RFC3339))))
	} else {
		w.Write([]byte("Global intervention started on all clusters"))
	}
}

func (repman *ReplicationManager) handlerMuxGlobalInterventionEnd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if valid, _ := repman.IsValidClusterACL(r, repman.currentCluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	user := repman.GetUserFromRequest(r)

	closed := 0
	cancelled := 0
	// End active interventions on all clusters
	for _, cl := range repman.Clusters {
		if cl.IsIntervention {
			cl.EndIntervention(user)
			closed++
		}
		if cl.InterventionPending != nil {
			cl.InterventionPending = nil
			cl.SaveInterventionHistory()
			cancelled++
		}
	}

	repman.IsGlobalIntervention = false
	repman.IsGlobalInterventionPending = false
	repman.GlobalInterventionEntry = nil
	repman.ActiveInterventionCount = 0

	if closed == 0 && cancelled == 0 {
		http.Error(w, "No active or pending interventions", http.StatusConflict)
		return
	}

	w.Write([]byte(fmt.Sprintf("Closed %d active, cancelled %d pending interventions", closed, cancelled)))
}
