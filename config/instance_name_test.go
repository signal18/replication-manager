// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package config

import "testing"

func TestInstanceStoreName(t *testing.T) {
	c := &Config{}
	if _, ok := c.InstanceStoreName(); ok {
		t.Fatal("a name with no company")
	}
	if !c.SeedInstanceName("repman1") {
		t.Fatal("nothing seeded")
	}
	if c.SeedInstanceName("other") {
		t.Fatal("seeded twice")
	}
	c.InstanceDomain = "acme"
	if _, ok := c.InstanceStoreName(); ok {
		t.Fatal("a name with no location")
	}
	c.InstanceSubDomainZone = "paris"
	if n, _ := c.InstanceStoreName(); n != "acme/repman1/paris" {
		t.Fatalf("local name %q", n)
	}
	// a registration wins; its keys are never filled with the local name
	c.Cloud18, c.Cloud18Domain, c.Cloud18SubDomain, c.Cloud18SubDomainZone = true, "s18", "dev3", "ovh2"
	if n, _ := c.InstanceStoreName(); n != "s18/dev3/ovh2" {
		t.Fatalf("registered name %q", n)
	}
}

func TestValidInstanceNamePart(t *testing.T) {
	for _, ok := range []string{"acme", "Signal18", "repman-1.lan", "a_b"} {
		if err := ValidInstanceNamePart(ok); err != nil {
			t.Fatalf("%q refused: %v", ok, err)
		}
	}
	for _, ko := range []string{"", "a/b", "a b", "..", "a..b", "é"} {
		if ValidInstanceNamePart(ko) == nil {
			t.Fatalf("%q accepted", ko)
		}
	}
}
