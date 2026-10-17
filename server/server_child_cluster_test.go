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

// A child on the parent's image takes its dump and restore clients, as settings, only those that
// differ; on another image it keeps the default.
func TestChildDumpClients(t *testing.T) {
	parent := &config.Config{ProvDbImg: "mysql:8.4.11",
		BackupMysqldumpPath:   "/usr/share/replication-manager/mysql/8.4/mysqldump",
		BackupMysqlclientPath: "/usr/share/replication-manager/mysql/8.4/mysql",
		BackupMysqlbinlogPath: "/usr/share/replication-manager/mysql/8.4/mysqlbinlog"}
	got := childDumpClients(&config.Config{}, parent, "mysql:8.4.11")
	want := [][2]string{
		{"backup-mysqldump-path", parent.BackupMysqldumpPath},
		{"backup-mysqlclient-path", parent.BackupMysqlclientPath},
		{"backup-mysqlbinlog-path", parent.BackupMysqlbinlogPath},
	}
	if len(got) != len(want) {
		t.Fatalf("same image, default child: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("same image, default child: got %v, want %v", got, want)
		}
	}
	same := &config.Config{BackupMysqldumpPath: parent.BackupMysqldumpPath, BackupMysqlclientPath: parent.BackupMysqlclientPath, BackupMysqlbinlogPath: "/x/mysqlbinlog"}
	if got := childDumpClients(same, parent, "mysql:8.4.11"); len(got) != 1 || got[0] != [2]string{"backup-mysqlbinlog-path", parent.BackupMysqlbinlogPath} {
		t.Fatalf("only the differing path: got %v", got)
	}
	if got := childDumpClients(&config.Config{}, parent, "mysql:9.3.0"); got != nil {
		t.Fatalf("another image keeps the default: got %v", got)
	}
}

// TestChildXtrabackupImage: the parent's XtraBackup image on the parent's database image, empty
// on another image or when the parent has none.
func TestChildXtrabackupImage(t *testing.T) {
	parent := &config.Config{ProvDbImg: "mysql:8.4", ProvDbDockerXtrabackupImg: "auto"}
	if got := childXtrabackupImage(parent, "mysql:8.4"); got != "auto" {
		t.Fatalf("same image: got %q, want auto", got)
	}
	if got := childXtrabackupImage(parent, "mysql:9.1"); got != "" {
		t.Fatalf("another image: got %q, want empty", got)
	}
	if got := childXtrabackupImage(&config.Config{ProvDbImg: "mysql:8.4"}, "mysql:8.4"); got != "" {
		t.Fatalf("parent without image: got %q, want empty", got)
	}
}
