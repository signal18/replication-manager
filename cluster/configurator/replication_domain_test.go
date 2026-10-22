// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package configurator

import (
	"testing"

	"github.com/signal18/replication-manager/config"
)

// One rule for a cluster's GTID domain: the domain of its name, which the rendered
// configuration and the clusters create-child/create-staging build share; an explicit
// prov-db-domain wins. Two children of one parent get two domains.
func TestReplicationDomainOfName(t *testing.T) {
	if ReplicationDomainOfName("belair-rt") != ReplicationDomainOfName("belair-rt") {
		t.Fatal("the domain of a name is stable")
	}
	if ReplicationDomainOfName("belair-rt") == ReplicationDomainOfName("belair-mcs") {
		t.Fatal("two children of a parent share a domain")
	}

	c := &Configurator{ClusterConfig: config.Config{MasterConn: "child", ProvDomain: "0"}}
	if got := c.GetConfigReplicationDomain("belair-rt"); got != ReplicationDomainOfName("belair-rt") {
		t.Fatalf("rendered domain %s, rule %s", got, ReplicationDomainOfName("belair-rt"))
	}
	c.ClusterConfig.ProvDomain = "4"
	if got := c.GetConfigReplicationDomain("belair"); got != "4" {
		t.Fatalf("an explicit prov-db-domain wins: %s", got)
	}
}
