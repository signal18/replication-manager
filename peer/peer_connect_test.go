// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package peer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// restartingPeer gives a session at each login and accepts /api/clusters only with the session of
// its current life: a restart (life++) ends every session it gave, as a real peer's restart does.
type restartingPeer struct {
	mu     sync.Mutex
	life   int
	logins int
	refuse bool
}

func (p *restartingPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/api/login":
		if p.refuse || r.Header.Get("Authorization") != "Bearer realm" {
			http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized)
			return
		}
		p.logins++
		fmt.Fprintf(w, `{"token":"life%d"}`, p.life)
	case "/api/clusters":
		if r.Header.Get("Authorization") != fmt.Sprintf("Bearer life%d", p.life) {
			http.Error(w, "Unauthenticated resource", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`[]`))
	default:
		http.NotFound(w, r)
	}
}

// A peer that restarted (its sessions ended) is connected again at the next poll: the dead session
// is dropped on the 401 and the reason is the peer's ConnectionError until then, never a session
// sent forever; a refused login is the reason too.
func TestConnectPeerAfterPeerRestart(t *testing.T) {
	p := &restartingPeer{}
	srv := httptest.NewServer(p)
	defer srv.Close()
	pm := NewPeerManager(60)
	pc := NewPeerClient(srv.URL, 5*time.Second)
	realm := func() (string, bool) { return "realm", true }

	if why := pm.connectPeer(pc, "sysops@x.io", "", realm); why != "" || p.logins != 1 {
		t.Fatalf("first poll: logged in and connected, got %q (logins %d)", why, p.logins)
	}
	if why := pm.connectPeer(pc, "sysops@x.io", "", realm); why != "" || p.logins != 1 {
		t.Fatalf("next poll: the session is reused, no new login, got %q (logins %d)", why, p.logins)
	}

	p.mu.Lock()
	p.life++ // the peer restarts: the session the client holds is dead
	p.mu.Unlock()
	why := pm.connectPeer(pc, "sysops@x.io", "", realm)
	if !strings.HasPrefix(why, "clusters:") || pc.headers["Authorization"] != "" {
		t.Fatalf("the dead session is reported and dropped, got %q (session %q)", why, pc.headers["Authorization"])
	}
	if why := pm.connectPeer(pc, "sysops@x.io", "", realm); why != "" || p.logins != 2 {
		t.Fatalf("the poll after the restart logs in again and is connected, got %q (logins %d)", why, p.logins)
	}

	p.mu.Lock()
	p.life++
	p.refuse = true // and now refuses the login
	p.mu.Unlock()
	pm.connectPeer(pc, "sysops@x.io", "", realm) // the dead session, dropped
	if why := pm.connectPeer(pc, "sysops@x.io", "", realm); !strings.HasPrefix(why, "login:") {
		t.Fatalf("a refused login is the reason the peer is not connected, got %q", why)
	}

	ns := &PeerNodeStatus{}
	pm.setNodeConnectionError(ns, strings.Repeat("x", 2*maxConnectionErrorLen))
	if len(ns.ConnectionError) != maxConnectionErrorLen {
		t.Fatalf("the reason kept is bounded (F4): %d", len(ns.ConnectionError))
	}
	// a non-ASCII reason is cut on a character boundary (served as JSON)
	pm.setNodeConnectionError(ns, strings.Repeat("é", maxConnectionErrorLen))
	if !utf8.ValidString(ns.ConnectionError) || len(ns.ConnectionError) > maxConnectionErrorLen {
		t.Fatalf("the cut reason is valid UTF-8 and bounded: %d bytes, valid %v", len(ns.ConnectionError), utf8.ValidString(ns.ConnectionError))
	}
	// health failing clears the connect reason (no connect was tried); a reconnect clears it
	pm.setNodeError(ns, "failed to get health status: timeout")
	if ns.ConnectionError != "" {
		t.Fatalf("health failing clears the connect reason, got %q", ns.ConnectionError)
	}
	pm.setNodeError(ns, "")
	pm.setNodeConnectionError(ns, "login: refused")
	pm.setNodeConnectionError(ns, "")
	if ns.ConnectionError != "" {
		t.Fatal("a reconnect clears the reason")
	}
}
