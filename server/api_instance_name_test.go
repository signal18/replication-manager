// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"errors"
	"testing"

	"github.com/signal18/replication-manager/config"
)

func TestSetInstanceName(t *testing.T) {
	repman := &ReplicationManager{Conf: &config.Config{InstanceSubDomain: "repman1"}}
	if n := repman.instanceName(); n.Name != "" || n.SubDomain != "repman1" || n.Registered {
		t.Fatalf("before: %+v", n)
	}
	if err := repman.setInstanceName(InstanceNameForm{Domain: "acme", SubDomain: "repman1", Zone: "a/b"}); err == nil {
		t.Fatal("a location with '/' accepted")
	}
	if repman.Conf.InstanceDomain != "" {
		t.Fatal("a refused name was partly set")
	}
	if err := repman.setInstanceName(InstanceNameForm{Domain: "acme", SubDomain: "repman1", Zone: "paris"}); err != nil {
		t.Fatal(err)
	}
	if n := repman.instanceName(); n.Name != "acme/repman1/paris" {
		t.Fatalf("after: %+v", n)
	}
	if err := repman.setInstanceName(InstanceNameForm{Declined: true}); err != nil || !repman.instanceName().Declined {
		t.Fatalf("decline: %v", err)
	}
	if n := repman.instanceName(); n.Name != "acme/repman1/paris" {
		t.Fatalf("a decline changed the name: %+v", n)
	}
	// registered: the name is the registration, never changed here
	repman.Conf.Cloud18, repman.Conf.Cloud18Domain, repman.Conf.Cloud18SubDomain, repman.Conf.Cloud18SubDomainZone = true, "s18", "dev3", "ovh2"
	if n := repman.instanceName(); !n.Registered || n.Name != "s18/dev3/ovh2" {
		t.Fatalf("registered: %+v", n)
	}
	if err := repman.setInstanceName(InstanceNameForm{Domain: "x", SubDomain: "y", Zone: "z"}); !errors.Is(err, errInstanceNameRegistered) {
		t.Fatalf("registered change: %v", err)
	}
}
