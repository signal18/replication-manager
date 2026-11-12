// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"testing"
)

func TestRevokeUserAPITokens(t *testing.T) {
	repman, cl := newTokenTestManager(t)
	a1, _ := repman.createAPIToken("alice", APITokenForm{Label: "a1"}, "")
	a2, _ := repman.createAPIToken("alice", APITokenForm{Label: "a2"}, "")
	b, _ := repman.createAPIToken("bob", APITokenForm{Label: "b"}, "")
	n, err := repman.revokeUserAPITokens("alice", "password rotation by admin")
	if err != nil || n != 2 {
		t.Fatalf("revoked %d, %v", n, err)
	}
	for _, tok := range []*APIToken{a1, a2} {
		if _, ok := repman.parseAPITokenFromRequest(bearerRequest(tok.Token, "/api/clusters/c1")); ok {
			t.Fatalf("%s still valid", tok.Label)
		}
		if _, ok := cl.GetTokenPrincipal(tok.ID); ok {
			t.Fatalf("%s principal kept", tok.Label)
		}
	}
	if _, ok := repman.parseAPITokenFromRequest(bearerRequest(b.Token, "/api/clusters/c1")); !ok {
		t.Fatal("another user's token was revoked")
	}
	if n, _ := repman.revokeUserAPITokens("alice", "again"); n != 0 {
		t.Fatalf("revoked twice: %d", n)
	}
}

func TestValidUsername(t *testing.T) {
	for u, want := range map[string]bool{"ops": true, "a@b.io": true, "": false, "a,b": false, "a:b": false, "a\nb": false} {
		if validUsername(u) != want {
			t.Fatalf("%q: want %v", u, want)
		}
	}
}
