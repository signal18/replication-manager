// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"
)

// A session JWT carries the password encrypted and is checked with "password"; a password
// a client typed (login, HTTP Basic) is checked as received. The encrypted value read from
// a JWT or a saved configuration must never log in.
func TestSubmittedPasswordIsNeverDecrypted(t *testing.T) {
	const pubURL = "/api/monitor"
	c := newAuthGuardCluster()
	c.Conf.SecretKey = []byte("0123456789abcdef0123456789abcdef")
	c.APIUsers["admin"] = APIUser{User: "admin", Password: "s3cret-admin-pw"}

	encrypted := c.Conf.GetEncryptedString("s3cret-admin-pw")
	if !strings.HasPrefix(encrypted, "hash_") || encrypted == "s3cret-admin-pw" {
		t.Fatalf("test setup: password not encrypted (%q)", encrypted)
	}

	if !c.IsValidACL("admin", encrypted, pubURL, "password") {
		t.Fatal("a session JWT (encrypted password) must still authenticate")
	}
	if c.IsValidACL("admin", encrypted, pubURL, AuthMethodSubmittedPassword) {
		t.Fatal("a submitted encrypted value must not log in")
	}
	if !c.IsValidACL("admin", "s3cret-admin-pw", pubURL, AuthMethodSubmittedPassword) {
		t.Fatal("the typed password must log in")
	}
	if c.IsValidACL("admin", "wrong", pubURL, AuthMethodSubmittedPassword) {
		t.Fatal("a wrong password must not log in")
	}
}
