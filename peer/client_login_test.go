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
)

// #2327: the peer poll logs in with the instance's realm token; the password goes only to a peer
// refusing the token with a 401 (an older release), never after another answer (403, 5xx), and the
// session token the client holds is never sent with a login.
func TestPeerLoginWithBearer(t *testing.T) {
	var mode string
	var passwordSent bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if strings.Contains(string(b), "clear-pw") {
			passwordSent = true
		}
		auth := r.Header.Get("Authorization")
		switch {
		case auth == "Bearer stale-session":
			t.Error("a login must not carry the client's session token")
			w.WriteHeader(http.StatusTeapot)
		case auth == "Bearer realm" && mode == "newer":
			w.Write([]byte(`{"token":"s-token"}`))
		case auth == "Bearer realm" && mode == "forbidden":
			w.WriteHeader(http.StatusForbidden)
		case auth == "Bearer realm":
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		case strings.Contains(string(b), "clear-pw"):
			w.Write([]byte(`{"token":"s-password"}`))
		default:
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	for _, c := range []struct {
		mode, bearer, password, wantToken string
		wantErr, wantPassword             bool
	}{
		{"newer", "realm", "clear-pw", "Bearer s-token", false, false},
		{"older", "realm", "clear-pw", "Bearer s-password", false, true},
		{"older", "realm", "", "", true, false},
		{"forbidden", "realm", "clear-pw", "", true, false},
		{"older", "", "clear-pw", "Bearer s-password", false, true},
	} {
		mode, passwordSent = c.mode, false
		pc := NewPeerClient(srv.URL, 5*time.Second)
		pc.SetHeader("Authorization", "Bearer stale-session")
		if c.wantToken == "" {
			pc = NewPeerClient(srv.URL, 5*time.Second)
		}
		err := pc.PeerLoginWithBearer(c.bearer, "sysops@x.io", c.password)
		if (err != nil) != c.wantErr || passwordSent != c.wantPassword {
			t.Errorf("%+v: err=%v passwordSent=%v", c, err, passwordSent)
		}
		if c.wantToken != "" && pc.headers["Authorization"] != c.wantToken {
			t.Errorf("%+v: session %q", c, pc.headers["Authorization"])
		}
	}
}
