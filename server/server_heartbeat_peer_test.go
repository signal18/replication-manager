// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func peerHeartbeatHandler(uid int, status string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(Heartbeat{UID: uid, Status: status})
	})
}

func newHeartbeatTestRepman(uid int, status string) *ReplicationManager {
	repman := &ReplicationManager{Conf: &config.Config{}, peerHeartbeatFailures: make(map[string]string)}
	repman.Conf.MonitoringTicker = 1
	repman.Conf.ArbitrationSasUniqueId = uid
	repman.Status = status
	return repman
}

func trustTestServer(t *testing.T, ts *httptest.Server) {
	t.Helper()
	peerHeartbeatTransport = ts.Client().Transport
	t.Cleanup(func() { peerHeartbeatTransport = nil })
}

// The client case: an https-only peer written without a scheme. The plain
// http call gets a 400, the https retry answers, the scheme is kept for the
// peer and there is no split brain.
func TestPeerHeartbeatSwitchesToHTTPSWhenPeerServesTLSOnly(t *testing.T) {
	ts := httptest.NewTLSServer(peerHeartbeatHandler(2, ConstMonitorActif))
	defer ts.Close()
	trustTestServer(t, ts)
	peer := strings.TrimPrefix(ts.URL, "https://")
	repman := newHeartbeatTestRepman(1, ConstMonitorStandby)

	if split := repman.HeartbeatPeerSplitBrain(peer, false); split {
		t.Fatalf("split brain reported for a reachable https peer: %v", repman.peerHeartbeatFailures)
	}
	if got := repman.peerHeartbeatScheme(peer); got != "https://" {
		t.Fatalf("scheme kept for the peer = %q, want https://", got)
	}
	// Next round goes straight to https.
	if split := repman.HeartbeatPeerSplitBrain(peer, false); split {
		t.Fatal("split brain on the second round")
	}
}

// An explicit scheme is never changed: the failure says the scheme is wrong.
func TestPeerHeartbeatExplicitWrongSchemeExplainsWhy(t *testing.T) {
	ts := httptest.NewServer(peerHeartbeatHandler(2, ConstMonitorActif))
	defer ts.Close()
	peer := "https://" + strings.TrimPrefix(ts.URL, "http://")
	repman := newHeartbeatTestRepman(1, ConstMonitorStandby)

	if split := repman.HeartbeatPeerSplitBrain(peer, false); !split {
		t.Fatal("an https call to an http peer must fail")
	}
	reason := repman.peerHeartbeatFailures[peer]
	if !strings.Contains(reason, "scheme mismatch") || !strings.Contains(reason, "fix the scheme in arbitration-peer-hosts") {
		t.Fatalf("failure reason does not explain the scheme: %q", reason)
	}
	if _, ok := repman.arbPeerScheme.Load(peer); ok {
		t.Fatal("an explicit scheme must not be remembered as switched")
	}
}

// A peer that answers something else than a heartbeat keeps the reason, and
// the reason is cleared once it answers again.
func TestPeerHeartbeatFailureReasonRecordedAndCleared(t *testing.T) {
	healthy := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_ = json.NewEncoder(w).Encode(Heartbeat{UID: 2, Status: ConstMonitorActif})
	}))
	defer ts.Close()
	peer := strings.TrimPrefix(ts.URL, "http://")
	repman := newHeartbeatTestRepman(1, ConstMonitorStandby)

	if split := repman.HeartbeatPeerSplitBrain(peer, false); !split {
		t.Fatal("a 503 must count as unreachable")
	}
	if reason := repman.peerHeartbeatFailures[peer]; !strings.Contains(reason, "HTTP 503") || !strings.Contains(reason, "/api/heartbeat") {
		t.Fatalf("reason = %q, want the URL and HTTP 503", reason)
	}
	healthy = true
	if split := repman.HeartbeatPeerSplitBrain(peer, true); split {
		t.Fatal("split brain after the peer recovered")
	}
	if _, ok := repman.peerHeartbeatFailures[peer]; ok {
		t.Fatal("the failure reason must be cleared once the peer answers")
	}
}

// Both Standby on the same unique id: nobody claims Active, and it is flagged.
func TestPeerHeartbeatSameUniqueIDFlagged(t *testing.T) {
	ts := httptest.NewServer(peerHeartbeatHandler(0, ConstMonitorStandby))
	defer ts.Close()
	repman := newHeartbeatTestRepman(0, ConstMonitorStandby)

	repman.HeartbeatPeerSplitBrain(strings.TrimPrefix(ts.URL, "http://"), false)
	if !repman.peerHeartbeatSameUID {
		t.Fatal("same arbitration-external-unique-id not flagged")
	}
	if repman.Status != ConstMonitorStandby {
		t.Fatalf("status = %s: equal ids must not claim Active", repman.Status)
	}
}

func TestFetchPeerHeartbeatHTTPSToHTTPIsSchemeMismatch(t *testing.T) {
	ts := httptest.NewServer(peerHeartbeatHandler(2, ConstMonitorActif))
	defer ts.Close()
	_, err := fetchPeerHeartbeat("https://"+strings.TrimPrefix(ts.URL, "http://")+"/api/heartbeat", 2e9)
	if !errors.Is(err, errPeerSchemeMismatch) {
		t.Fatalf("err = %v, want a scheme mismatch", err)
	}
}

// The API port has no /api/heartbeat: its 404 points to the http-port.
func TestFetchPeerHeartbeatNotFoundNamesTheHTTPPort(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	defer ts.Close()
	_, err := fetchPeerHeartbeat(ts.URL+"/api/heartbeat", 2e9)
	if err == nil || !strings.Contains(err.Error(), "http-port") {
		t.Fatalf("err = %v, want the http-port hint", err)
	}
}
