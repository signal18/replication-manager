package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

func semiSyncVariables(kv map[string]string) *config.StringsMap {
	m := config.NewStringsMap()
	for k, v := range kv {
		m.Store(k, v)
	}
	return m
}

func TestIsSemiSyncReplicaEnabled(t *testing.T) {
	cases := []struct {
		name string
		vars *config.StringsMap
		want bool
	}{
		{"nil variables (never read)", nil, false},
		{"empty variables (plugin not loaded)", semiSyncVariables(nil), false},
		{"legacy name ON (MariaDB, MySQL before 8.0.26)", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "ON"}), true},
		{"legacy name OFF", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "OFF"}), false},
		{"new name ON (MySQL / Percona 8.0.26+, 8.4)", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "ON"}), true},
		{"new name OFF", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "OFF"}), false},
		{"numeric 1", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "1"}), true},
		{"numeric 0", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "0"}), false},
		{"mixed case value", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "on"}), true},
		{"both names, contradiction reads enabled", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "OFF", "RPL_SEMI_SYNC_REPLICA_ENABLED": "ON"}), true},
		{"both names OFF", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "OFF", "RPL_SEMI_SYNC_REPLICA_ENABLED": "OFF"}), false},
		{"source side only is not the replica side", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_MASTER_ENABLED": "ON", "RPL_SEMI_SYNC_SOURCE_ENABLED": "ON"}), false},
		{"unrelated value", semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": ""}), false},
	}
	for _, c := range cases {
		s := &ServerMonitor{Variables: c.vars}
		if got := s.IsSemiSyncReplicaEnabled(); got != c.want {
			t.Errorf("%s: IsSemiSyncReplicaEnabled=%v want %v", c.name, got, c.want)
		}
	}
	// A zero-value map (no sync.Map) must not panic either.
	if (&ServerMonitor{Variables: &config.StringsMap{}}).IsSemiSyncReplicaEnabled() {
		t.Error("zero StringsMap must read as not enabled")
	}
}

// oldSemiSyncBlock is the condition #2438 replaces, kept here only to prove the new one is a superset.
func oldSemiSyncBlock(s *ServerMonitor) bool { return s.HaveSemiSync && !s.SemiSyncSlaveStatus }

func TestIsSemiSyncReplicaOutOfSync(t *testing.T) {
	enabled := semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "ON"})
	disabled := semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "OFF"})
	cases := []struct {
		name        string
		vars        *config.StringsMap
		haveSemi    bool
		statusOn    bool
		wantBlocked bool
	}{
		// the #2438 case: replica IO thread stopped, status OFF, so HaveSemiSync is false too
		{"enabled, status OFF, HaveSemiSync false (IO thread stopped)", enabled, false, false, true},
		{"enabled, status ON (healthy, also while the IO thread reconnects)", enabled, true, true, false},
		{"enabled, status ON, HaveSemiSync not refreshed yet", enabled, false, true, false},
		{"disabled, status OFF: ordinary asynchronous replica", disabled, false, false, false},
		{"missing variables, status OFF: not blocked (as before)", semiSyncVariables(nil), false, false, false},
		{"nil variables, status OFF: not blocked (as before)", nil, false, false, false},
		// kept from the former test: master side ON on a replica whose replica side is OFF
		{"disabled, master side ON, replica status OFF: still blocked", disabled, true, false, true},
		{"never refreshed (HaveSemiSync starts true), status OFF: still blocked", nil, true, false, true},
		{"disabled, status ON (operating without the variable): not blocked", disabled, true, true, false},
	}
	for _, c := range cases {
		s := &ServerMonitor{Variables: c.vars, HaveSemiSync: c.haveSemi, SemiSyncSlaveStatus: c.statusOn}
		if got := s.IsSemiSyncReplicaOutOfSync(); got != c.wantBlocked {
			t.Errorf("%s: IsSemiSyncReplicaOutOfSync=%v want %v", c.name, got, c.wantBlocked)
		}
	}
}

// No candidate refused by the former condition may be accepted by the new one.
func TestIsSemiSyncReplicaOutOfSyncKeepsFormerBlocks(t *testing.T) {
	varSets := []*config.StringsMap{
		nil,
		semiSyncVariables(nil),
		semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "ON"}),
		semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "OFF"}),
		semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "ON"}),
		semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_REPLICA_ENABLED": "OFF"}),
	}
	for vi, vars := range varSets {
		for _, have := range []bool{false, true} {
			for _, status := range []bool{false, true} {
				s := &ServerMonitor{Variables: vars, HaveSemiSync: have, SemiSyncSlaveStatus: status}
				if oldSemiSyncBlock(s) && !s.IsSemiSyncReplicaOutOfSync() {
					t.Errorf("variables #%d have=%v status=%v: refused before, accepted now", vi, have, status)
				}
			}
		}
	}
}

func TestIsSlaveBlockedBySemiSyncFlags(t *testing.T) {
	enabled := semiSyncVariables(map[string]string{"RPL_SEMI_SYNC_SLAVE_ENABLED": "ON"})
	outOfSync := func() *ServerMonitor {
		return &ServerMonitor{Variables: enabled, HaveSemiSync: false, SemiSyncSlaveStatus: false}
	}
	inSync := func() *ServerMonitor {
		return &ServerMonitor{Variables: enabled, HaveSemiSync: true, SemiSyncSlaveStatus: true}
	}
	for _, c := range []struct {
		name               string
		failSync, rplCheck bool
		sl                 *ServerMonitor
		want               bool
	}{
		{"failover-at-sync and check-replication-state: out of sync blocked", true, true, outOfSync(), true},
		{"failover-at-sync off: override, not blocked", false, true, outOfSync(), false},
		{"check-replication-state off: override, not blocked", true, false, outOfSync(), false},
		{"both off: not blocked", false, false, outOfSync(), false},
		{"in sync is never blocked", true, true, inSync(), false},
	} {
		cl := &Cluster{Conf: &config.Config{FailSync: c.failSync, RplChecks: c.rplCheck}}
		if got := cl.isSlaveBlockedBySemiSync(c.sl); got != c.want {
			t.Errorf("%s: blocked=%v want %v", c.name, got, c.want)
		}
	}
	// switchover-at-sync does not drive this test: the shared test of electSwitchoverCandidate follows failover-at-sync.
	cl := &Cluster{Conf: &config.Config{SwitchSync: true, RplChecks: true}}
	if cl.isSlaveBlockedBySemiSync(outOfSync()) {
		t.Error("switchover-at-sync alone must not block through isSlaveBlockedBySemiSync")
	}
}
