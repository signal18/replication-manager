// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import "testing"

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

func TestAPIPublicHost(t *testing.T) {
	for in, want := range map[string]string{
		"https://dbaas-fr-2.signal18.io/": "dbaas-fr-2.signal18.io",
		"https://repman.example:10005/x":  "repman.example",
		"dbaas-fr-1.example":              "dbaas-fr-1.example",
		"":                                "",
	} {
		if got := apiPublicHost(in); got != want {
			t.Fatalf("%q: %q, want %q", in, got, want)
		}
	}
}
