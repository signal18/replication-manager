// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// A standby whose active peer answers the peer heartbeat leaves the failover to it (#2040):
// it must not ask the arbitrator (asking first won the lease under a healthy active), so the
// ActiveArbitration check fails without any arbitrator call and ERR00022 says why. With
// arbitration off, on the active, or in a split brain (active peer lost), nothing changes.
func TestStandbyLeavesFailoverToLiveActive(t *testing.T) {
	for _, c := range []struct {
		name        string
		arbitration bool
		status      string
		splitBrain  bool
		defers      bool
	}{
		{"standby, active peer alive", true, ConstMonitorStandby, false, true},
		{"standby, active peer lost (split brain)", true, ConstMonitorStandby, true, false},
		{"active", true, ConstMonitorActif, false, false},
		{"active in split brain", true, ConstMonitorActif, true, false},
		{"no arbitration", false, ConstMonitorStandby, false, false},
	} {
		cl := &Cluster{Conf: &config.Config{Arbitration: c.arbitration}, Status: c.status, IsSplitBrain: c.splitBrain, StateMachine: &state.StateMachine{}}
		cl.StateMachine.Init()
		if got := cl.standbyLeavesFailoverToActive(); got != c.defers {
			t.Errorf("%s: standbyLeavesFailoverToActive = %v, want %v", c.name, got, c.defers)
		}
	}
}

// The gate CheckFailed applies (failoverArbitrationCheck), against a stub arbitrator that
// always answers winner and counts its calls: a standby with a live active peer never asks it
// and does not fail over; the same standby in a split brain asks and may fail over; a healthy
// master is never sent to the arbitrator. With arbitration off, or on an active whose licence
// expired, the gate passes without asking (isActiveArbitration's early returns).
func TestFailoverArbitrationCheckStandbyNeverAsks(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"arbitration":"winner"}`))
	}))
	defer srv.Close()
	newCluster := func(status string, split bool, masterState string, arbitration, licenceExpired bool) *Cluster {
		cl := &Cluster{Name: "t", Status: status, IsSplitBrain: split, LicenseExpired: licenceExpired, StateMachine: &state.StateMachine{}, Conf: &config.Config{
			Arbitration: arbitration, ArbitrationSasHosts: srv.URL, ArbitrationSasSecret: "s", ArbitrationReadTimout: 800,
		}}
		cl.StateMachine.Init()
		cl.master = &ServerMonitor{URL: "db1:3306", State: masterState}
		return cl
	}
	for _, c := range []struct {
		name        string
		status      string
		split       bool
		masterState string
		arbitration bool
		licence     bool
		want        bool
		wantCalls   int32
		err22       bool
	}{
		{"standby, live active, master failed", ConstMonitorStandby, false, stateFailed, true, false, false, 0, true},
		{"standby, split brain, master failed", ConstMonitorStandby, true, stateFailed, true, false, true, 1, false},
		{"active, master failed", ConstMonitorActif, false, stateFailed, true, false, true, 1, false},
		{"standby, healthy master", ConstMonitorStandby, false, stateMaster, true, false, true, 0, false},
		{"active, licence expired, master failed", ConstMonitorActif, false, stateFailed, true, true, true, 0, false},
		{"standby, arbitration off, master failed", ConstMonitorStandby, false, stateFailed, false, false, true, 0, false},
	} {
		calls.Store(0)
		cl := newCluster(c.status, c.split, c.masterState, c.arbitration, c.licence)
		if got := cl.failoverArbitrationCheck(); got != c.want {
			t.Errorf("%s: failoverArbitrationCheck = %v, want %v", c.name, got, c.want)
		}
		if got := calls.Load(); got != c.wantCalls {
			t.Errorf("%s: arbitrator called %d times, want %d", c.name, got, c.wantCalls)
		}
		if got := cl.StateMachine.CurState.Search("ERR00022"); got != c.err22 {
			t.Errorf("%s: ERR00022 open %v, want %v", c.name, got, c.err22)
		}
	}
}
