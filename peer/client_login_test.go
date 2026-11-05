// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package peer

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// fakeLoginPeer answers /api/login as a peer of the given mode would, and records whether the
// password reached it.
type fakeLoginPeer struct {
	mode         string // "newer" takes the realm token, "refusing" knows tokens and refuses it, "older" knows only passwords, "forbidden", "empty"
	passwordSent bool
	staleSent    bool
}

func (f *fakeLoginPeer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	if strings.Contains(string(b), "clear-pw") {
		f.passwordSent = true
	}
	auth := r.Header.Get("Authorization")
	switch {
	case strings.Contains(auth, "stale-session"):
		f.staleSent = true
		w.WriteHeader(http.StatusTeapot)
	case auth == "Bearer realm" && f.mode == "newer":
		w.Write([]byte(`{"token":"s-token"}`))
	case auth == "Bearer realm" && f.mode == "empty":
		w.Write([]byte(`{}`))
	case auth == "Bearer realm" && f.mode == "forbidden":
		w.WriteHeader(http.StatusForbidden)
	case auth == "Bearer realm" && f.mode == "refusing":
		http.Error(w, "Error logging in: "+config.ConstCloud18TokenRefused, http.StatusUnauthorized)
	case auth == "Bearer realm":
		http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized) // an older release
	case strings.Contains(string(b), "clear-pw"):
		w.Write([]byte(`{"token":"s-password"}`))
	default:
		http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized)
	}
}

// #2327: the peer poll logs in with the instance's realm token; the password goes only to a peer
// refusing the token with the 401 of an older release, never to a peer that knows tokens and
// refused it, never after another answer (403, 5xx); a login never carries the session token the
// client holds, whatever the case of its header; an answer without a session token is an error.
func TestPeerLoginWithBearer(t *testing.T) {
	f := &fakeLoginPeer{}
	srv := httptest.NewServer(f)
	defer srv.Close()

	for _, c := range []struct {
		mode, bearer, password, wantSession string
		wantErr, wantPassword               bool
	}{
		{"newer", "realm", "clear-pw", "Bearer s-token", false, false},
		{"older", "realm", "clear-pw", "Bearer s-password", false, true},
		{"older", "realm", "", "", true, false},
		{"refusing", "realm", "clear-pw", "", true, false},
		{"forbidden", "realm", "clear-pw", "", true, false},
		{"empty", "realm", "clear-pw", "", true, false},
		{"older", "", "clear-pw", "Bearer s-password", false, true},
		{"older", "", "", "", true, false},
	} {
		f.mode, f.passwordSent, f.staleSent = c.mode, false, false
		pc := NewPeerClient(srv.URL, 5*time.Second)
		// a session the client still holds, under a lower-case header name: never sent with a login
		pc.SetHeader("authorization", "Bearer stale-session")
		err := pc.PeerLoginWithBearer(c.bearer, "sysops@x.io", c.password)
		if (err != nil) != c.wantErr || f.passwordSent != c.wantPassword || f.staleSent {
			t.Errorf("%+v: err=%v passwordSent=%v staleSent=%v", c, err, f.passwordSent, f.staleSent)
		}
		if c.wantSession != "" && pc.headers["Authorization"] != c.wantSession {
			t.Errorf("%+v: session %q", c, pc.headers["Authorization"])
		}
	}
}

// #2327 review: the poll's login sends no password when the realm refused the instance's account
// (the token source says so), even to an older peer that would take a password.
func TestLoginPeerRefusedAccount(t *testing.T) {
	f := &fakeLoginPeer{mode: "older"}
	srv := httptest.NewServer(f)
	defer srv.Close()

	refused := func() (string, bool) { return "", false }
	if err := loginPeer(NewPeerClient(srv.URL, 5*time.Second), "sysops@x.io", "clear-pw", refused); err == nil || f.passwordSent {
		t.Fatalf("a refused account: no login, no password (err=%v, passwordSent=%v)", err, f.passwordSent)
	}
	unreachable := func() (string, bool) { return "", true }
	pc := NewPeerClient(srv.URL, 5*time.Second)
	if err := loginPeer(pc, "sysops@x.io", "clear-pw", unreachable); err != nil || !f.passwordSent || pc.headers["Authorization"] != "Bearer s-password" {
		t.Fatalf("the realm out of reach: the password goes, as before (err=%v)", err)
	}
	if err := loginPeer(NewPeerClient(srv.URL, 5*time.Second), "sysops@x.io", "clear-pw", nil); err != nil {
		t.Fatalf("no token source (an instance not registered): the password, as before: %v", err)
	}
}
