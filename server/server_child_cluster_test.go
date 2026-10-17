// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"strings"
	"testing"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// #1988: the parent's logical backup is the first check. A parent without one is
// refused before any other check, with what is missing, and nothing is planned.
func TestPlanStagingClusterNeedsParentBackupFirst(t *testing.T) {
	sm := new(state.StateMachine)
	sm.Init()
	parent := &cluster.Cluster{
		StateMachine: sm,
		Name:         "prod",
		Conf: &config.Config{
			WorkingDir:       t.TempDir(),
			ProvOrchestrator: config.ConstOrchestratorOnPremise, // would be refused too, later
			TopologyStaging:  true,                              // would be refused too, later
		},
		WorkingDir: t.TempDir(),
	}
	repman := &ReplicationManager{Clusters: map[string]*cluster.Cluster{"prod": parent}, ClusterList: []string{"prod"}, Conf: &config.Config{}}

	plan, err := repman.PlanChildCluster(parent, ChildRequest{}, childModeStaging)
	if err == nil {
		t.Fatalf("a parent without backup server was planned: %+v", plan)
	}
	if !strings.Contains(err.Error(), "no backup server") {
		t.Fatalf("the first refusal must be the missing backup, got %q", err)
	}
	if plan.Name != "" || plan.Backup != "" {
		t.Fatalf("nothing is planned on a refusal, got %+v", plan)
	}
}

func TestChildProxyTypeWithoutProxy(t *testing.T) {
	if got := childProxyType(&cluster.Cluster{Name: "prod", Conf: &config.Config{}}); got != "" {
		t.Fatalf("a parent without proxy gives a staging cluster without proxy, got %q", got)
	}
}

// A child on the parent's image takes its dump and restore clients; on another image it keeps
// the default.
func TestChildDumpClients(t *testing.T) {
	parent := &config.Config{ProvDbImg: "mysql:8.4.11",
		BackupMysqldumpPath:   "/usr/share/replication-manager/mysql/8.4/mysqldump",
		BackupMysqlclientPath: "/usr/share/replication-manager/mysql/8.4/mysql",
		BackupMysqlbinlogPath: "/usr/share/replication-manager/mysql/8.4/mysqlbinlog"}
	child := &config.Config{}
	if !childDumpClients(child, parent, "mysql:8.4.11") {
		t.Fatal("paths set: a change reported")
	}
	if childDumpClients(child, parent, "mysql:8.4.11") {
		t.Fatal("the same paths again: no change")
	}
	if child.BackupMysqldumpPath != parent.BackupMysqldumpPath || child.BackupMysqlclientPath != parent.BackupMysqlclientPath || child.BackupMysqlbinlogPath != parent.BackupMysqlbinlogPath {
		t.Fatalf("same image: %+v", child)
	}
	other := &config.Config{}
	if childDumpClients(other, parent, "mysql:9.3.0") {
		t.Fatal("another image: no change")
	}
	if other.BackupMysqldumpPath != "" || other.BackupMysqlclientPath != "" || other.BackupMysqlbinlogPath != "" {
		t.Fatalf("another image keeps the default: %+v", other)
	}
}
