// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"time"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// jobsRoundTripTimeout bounds the wait for the job: the jobs script runs about every minute.
const jobsRoundTripTimeout = 5 * time.Minute

// TestJobsRoundTrip checks that the database jobs reach replication-manager, on every engine and
// orchestrator that runs a jobs container: it asks one server (a replica when there is one) for the
// error log collection, the lightest job, and expects its state to end done within
// jobsRoundTripTimeout. The database itself runs whether its jobs work or not, so the lifecycle
// and configuration tests pass even when the jobs container cannot talk to replication-manager (a
// stock MySQL or Percona image without socat and without prov-db-docker-xtrabackup-img): this one
// does not. It changes nothing on the database.
func (regtest *RegTest) TestJobsRoundTrip(cl *cluster.Cluster, conf string, test *cluster.Test) bool {
	var srv *cluster.ServerMonitor
	if slaves := cl.GetSlaves(); len(slaves) > 0 {
		srv = slaves[0]
	} else if master := cl.GetMaster(); master != nil {
		srv = master
	}
	if srv == nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: no running server to ask for a job")
		return false
	}
	before := int64(0)
	if t, ok := srv.JobResults.CheckAndGet("errorlog"); ok {
		before = t.End
	}
	asked := time.Now().Unix()
	if _, err := srv.JobBackupErrorLog(); err != nil {
		cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: error log collection not queued on %s: %s", srv.URL, err)
		return false
	}
	deadline := time.Now().Add(jobsRoundTripTimeout)
	for time.Now().Before(deadline) {
		if t, ok := srv.JobResults.CheckAndGet("errorlog"); ok && t.Done == 1 && t.End >= asked && t.End != before {
			if t.State == cluster.JobStateErrorExec || t.State == cluster.JobStateErrorAfter {
				cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "FAIL: the error log job of %s ended in error: %s", srv.URL, t.Result)
				return false
			}
			cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST", "PASS: the jobs of %s reached replication-manager, the error log job ended done", srv.URL)
			return true
		}
		time.Sleep(5 * time.Second)
	}
	cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModGeneral, "TEST",
		"FAIL: no job state from %s within %s: its jobs do not reach replication-manager (see WARN0324 and the jobs container's log)", srv.URL, jobsRoundTripTimeout)
	return false
}
