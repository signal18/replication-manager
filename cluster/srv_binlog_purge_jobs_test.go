package cluster

import (
	"testing"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
	"github.com/signal18/replication-manager/utils/version"
)

// A replica on MariaDB 11.4 and up is purged by the replica job: max_binlog_total_size
// purges nothing on a server no replica is connected to, so its binlogs filled the data
// disk (dev3, 2026-10-21). The master keeps max_binlog_total_size, older versions the jobs.
func TestBinlogPurgeJobs(t *testing.T) {
	c := &Cluster{Name: "t", StateMachine: new(state.StateMachine), Conf: &config.Config{ForceBinlogPurgeReplicas: true}}
	c.StateMachine.Init()
	newServer := func(major, minor int) *ServerMonitor {
		return &ServerMonitor{ClusterGroup: c, DBVersion: &version.Version{Flavor: "MariaDB", Major: major, Minor: minor},
			BinaryLogFilesCount: 3, HaveBinlogSlaveUpdates: true}
	}
	cases := []struct {
		name                             string
		major, minor                     int
		isMaster                         bool
		sizeLimit, masterJob, replicaJob bool
	}{
		{"11.8 master", 11, 8, true, true, false, false},
		{"11.8 replica", 11, 8, false, true, false, true},
		{"10.11 master", 10, 11, true, false, true, false},
		{"10.11 replica", 10, 11, false, false, false, true},
	}
	for _, tc := range cases {
		s := newServer(tc.major, tc.minor)
		sizeLimit, masterJob, replicaJob := s.binlogPurgeJobs(tc.isMaster)
		if sizeLimit != tc.sizeLimit || masterJob != tc.masterJob || replicaJob != tc.replicaJob {
			t.Errorf("%s: got size limit %v, master job %v, replica job %v; want %v, %v, %v",
				tc.name, sizeLimit, masterJob, replicaJob, tc.sizeLimit, tc.masterJob, tc.replicaJob)
		}
	}

	// force-binlog-purge-replicas off, or a replica keeping only two binlogs: no replica job.
	s := newServer(11, 8)
	c.Conf.ForceBinlogPurgeReplicas = false
	if _, _, replicaJob := s.binlogPurgeJobs(false); replicaJob {
		t.Errorf("force-binlog-purge-replicas off must not purge a replica")
	}
	c.Conf.ForceBinlogPurgeReplicas = true
	s.BinaryLogFilesCount = 2
	if _, _, replicaJob := s.binlogPurgeJobs(false); replicaJob {
		t.Errorf("a replica with two binlogs left must not be purged")
	}
}
