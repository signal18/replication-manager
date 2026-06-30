package server

import (
	"sort"
	"testing"
)

// TestAuthorityClusterName_Empty returns "" when no clusters are configured.
func TestAuthorityClusterName_Empty(t *testing.T) {
	repman := &ReplicationManager{}
	if got := repman.authorityClusterName(); got != "" {
		t.Errorf("expected empty authority name with no clusters, got %q", got)
	}
}

// TestAuthorityClusterName_Single returns the only cluster when there is one.
func TestAuthorityClusterName_Single(t *testing.T) {
	repman := &ReplicationManager{ImmutableClusterList: []string{"only-cluster"}}
	if got := repman.authorityClusterName(); got != "only-cluster" {
		t.Errorf("expected only-cluster, got %q", got)
	}
}

// TestAuthorityClusterName_Deterministic verifies that authority selection is
// stable: ImmutableClusterList is sorted, so both peers independently arrive at
// the same first entry regardless of discovery order.
func TestAuthorityClusterName_Deterministic(t *testing.T) {
	// Simulate two peers discovering the same clusters in different orders.
	peer1 := []string{"gamma", "alpha", "beta"}
	peer2 := []string{"beta", "gamma", "alpha"}

	sort.Strings(peer1)
	sort.Strings(peer2)

	r1 := &ReplicationManager{ImmutableClusterList: peer1}
	r2 := &ReplicationManager{ImmutableClusterList: peer2}

	a1 := r1.authorityClusterName()
	a2 := r2.authorityClusterName()

	if a1 != a2 {
		t.Errorf("authority cluster mismatch between peers: %q vs %q", a1, a2)
	}
	if a1 != "alpha" {
		t.Errorf("expected sorted-first cluster alpha, got %q", a1)
	}
}

// TestRepmanRoleActive verifies SetRepmanRoleActive sets Status to active.
func TestRepmanRoleActive(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = nil
	repman.Status = ConstMonitorStandby
	repman.SetRepmanRoleActive()
	if repman.Status != ConstMonitorActif {
		t.Errorf("expected %q after SetRepmanRoleActive, got %q", ConstMonitorActif, repman.Status)
	}
}

// TestRepmanRoleStandby verifies SetRepmanRoleStandby sets Status to standby.
func TestRepmanRoleStandby(t *testing.T) {
	repman := &ReplicationManager{}
	repman.Clusters = nil
	repman.Status = ConstMonitorActif
	repman.SetRepmanRoleStandby()
	if repman.Status != ConstMonitorStandby {
		t.Errorf("expected %q after SetRepmanRoleStandby, got %q", ConstMonitorStandby, repman.Status)
	}
}
