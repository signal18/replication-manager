package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/signal18/replication-manager/cluster"
)

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

	if !repman.IsGlobalIntervention {
		http.Error(w, "No active global intervention", http.StatusConflict)
		return
	}

	user := repman.GetUserFromRequest(r)

	// End intervention on all clusters
	for _, cl := range repman.Clusters {
		if cl.IsIntervention {
			cl.EndIntervention(user)
		}
	}

	repman.IsGlobalIntervention = false
	repman.GlobalInterventionEntry = nil

	w.Write([]byte("Global intervention ended on all clusters"))
}
