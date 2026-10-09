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
	"net/http/httptest"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestRecordPeerAPIURLIgnoresSelfAndEmpty(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{MonitorAddress: "repman.s18.svc.cloud18", APIPort: "10005"}, UUID: "me"}

	repman.recordPeerAPIURL(Heartbeat{UUID: "peer"}) // older peer, no URL
	repman.recordPeerAPIURL(Heartbeat{UUID: "me", APIURL: "https://elsewhere:10005"})
	repman.recordPeerAPIURL(Heartbeat{UUID: "other", APIURL: "https://repman.s18.svc.cloud18:10005"}) // our own URL
	if repman.peerAPIURL != "" {
		t.Fatalf("peerAPIURL = %q, want nothing recorded", repman.peerAPIURL)
	}
	repman.recordPeerAPIURL(Heartbeat{UUID: "peer", APIURL: "https://repman-dr.s18.svc.cloud18:10005"})
	if repman.peerAPIURL != "https://repman-dr.s18.svc.cloud18:10005" {
		t.Fatalf("peerAPIURL = %q", repman.peerAPIURL)
	}
	// A later answer without URL (peer downgraded) keeps the last known one.
	repman.recordPeerAPIURL(Heartbeat{UUID: "peer"})
	if repman.peerAPIURL == "" {
		t.Fatal("last known peer URL lost")
	}
}

func TestHeartbeatAnswerCarriesAPIURL(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{MonitorAddress: "repman-dr.s18.svc.cloud18", APIPort: "10005"}}
	w := httptest.NewRecorder()
	repman.handlerHeartbeat(w, httptest.NewRequest("GET", "/api/heartbeat", nil))
	var h Heartbeat
	if err := json.Unmarshal(w.Body.Bytes(), &h); err != nil {
		t.Fatal(err)
	}
	if h.APIURL != "https://repman-dr.s18.svc.cloud18:10005" {
		t.Fatalf("apiUrl = %q", h.APIURL)
	}
}

func TestBootstrapPairURLsOnlyWithActiveStandby(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{MonitorAddress: "repman-dr.s18.svc.cloud18", APIPort: "10005"}}
	repman.peerAPIURL = "https://repman.s18.svc.cloud18:10005"
	if got := repman.bootstrapPairURLs(); got != "" {
		t.Fatalf("without arbitration-external: %q, want none", got)
	}
	repman.Conf.Arbitration = true
	if got := repman.bootstrapPairURLs(); got != "https://repman-dr.s18.svc.cloud18:10005 https://repman.s18.svc.cloud18:10005" {
		t.Fatalf("pair = %q, want self then peer", got)
	}
	repman.peerAPIURL = ""
	if got := repman.bootstrapPairURLs(); got != "" {
		t.Fatalf("peer unknown: %q, want none", got)
	}
}
