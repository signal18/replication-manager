package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// clusterstate.json's isProvisioned is the cluster's provisioned flag, not IsAllDbUp, which
// nothing sets true: every cluster was harvested and published in peer.json as not
// provisioned (#2200).
func TestStateToSaveIsProvisioned(t *testing.T) {
	cl := &Cluster{Name: "c1", Conf: &config.Config{}, StateMachine: new(state.StateMachine)}
	cl.IsProvision = true
	cl.IsAllDbUp = false
	if s := cl.stateToSave(); !s.IsProvisioned {
		t.Fatalf("a provisioned cluster must save isProvisioned true, got %+v", s)
	}
	cl.IsProvision = false
	cl.IsAllDbUp = true
	if s := cl.stateToSave(); s.IsProvisioned {
		t.Fatalf("a cluster not provisioned must save isProvisioned false, got %+v", s)
	}
}
