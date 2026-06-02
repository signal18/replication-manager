package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/signal18/replication-manager/cluster"
)

// RefreshGlobalInterventionState counts active interventions across all clusters
// and restores the global intervention flag if all clusters have a global-scope intervention.
func (repman *ReplicationManager) RefreshGlobalInterventionState() {
	count := 0
	globalCount := 0
	for _, cl := range repman.Clusters {
		if cl.IsIntervention {
			count++
			if cl.InterventionCurrent != nil && cl.InterventionCurrent.Scope == "global" {
				globalCount++
			}
		}
	}
	repman.ActiveInterventionCount = count

	// Restore global flag if all clusters have a global-scope intervention (restart recovery)
	if !repman.IsGlobalIntervention && globalCount > 0 && globalCount == len(repman.Clusters) {
		repman.IsGlobalIntervention = true
		// Use the first cluster's entry as the global entry
		for _, cl := range repman.Clusters {
			if cl.InterventionCurrent != nil && cl.InterventionCurrent.Scope == "global" {
				repman.GlobalInterventionEntry = cl.InterventionCurrent
				break
			}
		}
	}
}

func (repman *ReplicationManager) handlerMuxGlobalInterventionStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if valid, _ := repman.IsValidClusterACL(r, repman.currentCluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	var body struct {
		Reason string `json:"reason"`
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

	user := repman.GetUserFromRequest(r)

	repman.IsGlobalIntervention = true
	repman.GlobalInterventionEntry = &cluster.InterventionEntry{
		User:      user,
		Reason:    body.Reason,
		Scope:     "global",
		StartedAt: time.Now(),
	}

	// Start intervention on all clusters
	for _, cl := range repman.Clusters {
		cl.StartIntervention(user, body.Reason, "global")
	}

	w.Write([]byte("Global intervention started on all clusters"))
}

func (repman *ReplicationManager) handlerMuxGlobalInterventionEnd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	if valid, _ := repman.IsValidClusterACL(r, repman.currentCluster); !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}

	user := repman.GetUserFromRequest(r)

	closed := 0
	// End intervention on all clusters (both global and per-cluster)
	for _, cl := range repman.Clusters {
		if cl.IsIntervention {
			cl.EndIntervention(user)
			closed++
		}
	}

	repman.IsGlobalIntervention = false
	repman.GlobalInterventionEntry = nil
	repman.ActiveInterventionCount = 0

	if closed == 0 {
		http.Error(w, "No active interventions", http.StatusConflict)
		return
	}

	w.Write([]byte(fmt.Sprintf("Closed %d interventions across all clusters", closed)))
}
