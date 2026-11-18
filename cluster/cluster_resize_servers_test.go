// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"strconv"
	"testing"

	"github.com/signal18/replication-manager/config"
)

// A cluster's resize acts on its own servers only. A replica of a child or staging
// cluster listed in its topology belongs to that other cluster: the 2026-11-17 reconcile
// sent the parent's SET GLOBALs to such a replica and tried to shrink it through the
// parent's own db1 service.
func TestResizeServersSkipsOtherClusterReplica(t *testing.T) {
	cl, own, done := k8sResizeTestServer(t, "parent", "db1")
	defer done()
	own.SourceClusterName = cl.Name
	legacy := &ServerMonitor{URL: "db2:3306"}
	child := &ServerMonitor{URL: "db1.child:3306", SourceClusterName: "parent-staging"}
	cl.Servers = append(cl.Servers, nil, legacy, child)

	got := cl.resizeServers()
	if len(got) != 2 || got[0] != own || got[1] != legacy {
		urls := []string{}
		for _, s := range got {
			urls = append(urls, s.URL)
		}
		t.Fatalf("resizeServers = %v, want [%s %s] (own and unnamed, never the child replica)", urls, own.URL, legacy.URL)
	}
}

// The reconcile decision ignores another cluster's replica: its buffer pool, sized by its
// own cluster, never makes the parent's resize due.
func TestDynamicMemoryResizeDueIgnoresOtherClusterReplica(t *testing.T) {
	cl, s, done := k8sResizeTestServer(t, "parent", "db1")
	defer done()
	cl.Conf.ProvMem = "8192"
	cl.Conf.ProvMemSharedPct = "threads:16,innodb:48,myisam:10,aria:10,rocksdb:1,tokudb:0,s3:1,archive:1,querycache:0,tidesdb:1"
	cl.Configurator.ClusterConfig = *cl.Conf
	target, err := strconv.ParseInt(cl.Configurator.GetConfigInnoDBBPSize(), 10, 64)
	if err != nil || target <= 0 {
		t.Fatalf("fixture: target %v %v", target, err)
	}
	s.State = stateMaster
	s.SourceClusterName = cl.Name
	s.Variables = config.NewStringsMap()
	s.Variables.Set("INNODB_BUFFER_POOL_SIZE", strconv.FormatInt(target*1024*1024, 10))
	child := &ServerMonitor{URL: "db1.child:3306", SourceClusterName: "parent-staging", State: stateSlave}
	child.Variables = config.NewStringsMap()
	child.Variables.Set("INNODB_BUFFER_POOL_SIZE", strconv.FormatInt(8192*1024*1024, 10))
	cl.Servers = append(cl.Servers, child)

	if _, due := cl.dynamicMemoryResizeDue(); due {
		t.Fatal("own server at target: the child replica's buffer pool must not make the resize due")
	}
}
