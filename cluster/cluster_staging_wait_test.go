// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strings"
	"testing"
	"time"
)

// The staging seed waits for the replica it detaches to be in sync with no timer, its
// catching up the creation's step, and stops only on a broken replication; a replica
// rebuilt by a reload is read again by URL.
func TestWaitStagingReplicaInSync(t *testing.T) {
	c, replica := newStagingTestServer(t)
	c.Servers = []*ServerMonitor{replica}

	replica.State = stateSlave
	if err := c.waitStagingReplicaInSync(replica.URL); err != nil {
		t.Fatalf("an in-sync replica: %s", err)
	}

	replica.State = stateSlaveErr
	if err := c.waitStagingReplicaInSync(replica.URL); err == nil || !strings.Contains(err.Error(), "replication broken") {
		t.Fatalf("a broken replication stops the seed: %v", err)
	}

	if err := c.waitStagingReplicaInSync("gone:3306"); err == nil {
		t.Fatal("a replica no longer in the cluster stops the seed")
	}

	// catching up: the step says so, then the replica rebuilt in sync (a reload) ends the wait
	c.SetStagingCreation(StagingCreation{Kind: "Staging", Parent: "prod", Step: "seed from prod"})
	replica.State = stateSlaveLate
	done := make(chan error, 1)
	go func() { done <- c.waitStagingReplicaInSync(replica.URL) }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(c.GetStagingCreation().Step, "catching up") {
		if time.Now().After(deadline) {
			t.Fatalf("step %q", c.GetStagingCreation().Step)
		}
		time.Sleep(50 * time.Millisecond)
	}
	rebuilt := *replica
	rebuilt.State = stateSlave
	c.Servers = []*ServerMonitor{&rebuilt}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("caught up: %s", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not end once the replica was in sync")
	}
}
