// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

// One name, the Cloud18 domain, sub-domain and zone, registered or not: the Cloud18 switch
// (unregister, a failed GitLab login) does not change it.
func TestInstanceStoreName(t *testing.T) {
	c := &Config{}
	if _, ok := c.InstanceStoreName(); ok {
		t.Fatal("a name with no part")
	}
	c.Cloud18Domain, c.Cloud18SubDomain = "acme", "repman1"
	if _, ok := c.InstanceStoreName(); ok {
		t.Fatal("a name with no zone")
	}
	c.Cloud18SubDomainZone = "paris"
	if n, _ := c.InstanceStoreName(); n != "acme/repman1/paris" {
		t.Fatalf("unregistered name %q", n)
	}
	c.Cloud18 = true
	if n, _ := c.InstanceStoreName(); n != "acme/repman1/paris" {
		t.Fatalf("registered name %q", n)
	}
	c.Cloud18 = false
	if n, _ := c.InstanceStoreName(); n != "acme/repman1/paris" {
		t.Fatalf("unregistered again %q", n)
	}
	c.Cloud18SubDomain = "a/b"
	if _, ok := c.InstanceStoreName(); ok {
		t.Fatal("an invalid part named the instance")
	}
}

func TestValidInstanceNamePart(t *testing.T) {
	for _, ok := range []string{"acme", "Signal18", "repman-1.lan", "a_b"} {
		if err := ValidInstanceNamePart(ok); err != nil {
			t.Fatalf("%q refused: %v", ok, err)
		}
	}
	for _, ko := range []string{"", "a/b", "a b", ".", "..", "a..b", ".a", "a.", "é"} {
		if ValidInstanceNamePart(ko) == nil {
			t.Fatalf("%q accepted", ko)
		}
	}
}
