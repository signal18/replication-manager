// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/signal18/replication-manager/config"
)

// #1988: provisioning waits for the orchestrator node list a new cluster loads on its
// own tick; without nodes it stops with the orchestrator's error.
func TestWaitOrchestratorNodes(t *testing.T) {
	c := &Cluster{Conf: &config.Config{ProvOrchestrator: config.ConstOrchestratorOpenSVC}}
	c.errorInitNodes = errors.New("cert not loaded")
	err := c.WaitOrchestratorNodes(10 * time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "cert not loaded") || !strings.Contains(err.Error(), "opensvc") {
		t.Fatalf("no node must stop with the orchestrator and its error, got %v", err)
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		c.Lock()
		c.Agents = []Agent{{HostName: "s18-fr-4"}}
		c.Unlock()
	}()
	if err := c.WaitOrchestratorNodes(5 * time.Second); err != nil {
		t.Fatalf("nodes loaded later must be waited for, got %v", err)
	}
}
