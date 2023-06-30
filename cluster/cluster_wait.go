// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"errors"
	"os"
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster/nbc"
)

func (cluster *Cluster) WaitFailoverEndState() {
	for cluster.StateMachine.IsInFailover() {
		time.Sleep(time.Second)
		cluster.LogPrintf(LvlInfo, "Waiting for failover stopped.")
	}
	time.Sleep(recoverTime * time.Second)
}

func (cluster *Cluster) WaitFailoverEnd() error {
	cluster.WaitFailoverEndState()
	return nil

}

func (cluster *Cluster) WaitFailover(wg *sync.WaitGroup) {
	cluster.LogPrintf(LvlInfo, "Waiting failover end")
	defer wg.Done()
	exitloop := 0
	cluster.failoverCond = nbc.New()
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting failover end")
			exitloop++
		case <-cluster.failoverCond.Recv:
			cluster.LogPrintf(LvlInfo, "Failover end receive from channel failoverCond")
			return
		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Failover end")
	} else {
		cluster.LogPrintf(LvlErr, "Failover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitSwitchover(wg *sync.WaitGroup) {
	cluster.LogPrintf(LvlInfo, "Waiting switchover end")
	defer wg.Done()
	exitloop := 0
	cluster.switchoverCond = nbc.New()
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting switchover end")
			exitloop++
		case <-cluster.switchoverCond.Recv:
			exitloop = 9999999
		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Switchover end")
	} else {
		cluster.LogPrintf(LvlErr, "Switchover end timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitRejoin(wg *sync.WaitGroup) {

	defer wg.Done()
	logline := cluster.LogPrintf(LvlInfo, "Waiting Rejoin")
	exitloop := 0
	cluster.rejoinCond = nbc.New()
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))

	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {

		select {
		case <-ticker.C:
			cluster.LogUpdate(logline, LvlInfo, "Waiting Rejoin %d", exitloop)
			exitloop++
		case <-cluster.rejoinCond.Recv:
			exitloop = 9999999

		}

	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Rejoin Finished")

	} else {
		cluster.LogPrintf(LvlErr, "Rejoin timeout")
		return
	}
	return
}

func (cluster *Cluster) WaitClusterStop() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	cluster.LogPrintf(LvlInfo, "Waiting for cluster shutdown")
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for cluster shutdown")
			exitloop++
			// All cluster down
			if cluster.StateMachine.IsInState("ERR00021") == true {
				exitloop = 9999999
			}
			if cluster.HasAllDbDown() {
				exitloop = 9999999
			}

		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Cluster is shutdown")
	} else {
		cluster.LogPrintf(LvlErr, "Cluster shutdown timeout")
		return errors.New("Failed to stop the cluster")
	}
	return nil
}

func (cluster *Cluster) WaitProxyEqualMaster() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	cluster.LogPrintf(LvlInfo, "Waiting for proxy to join master")
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for proxy to join master %d", exitloop)
			exitloop++
			// All cluster down
			if cluster.IsProxyEqualMaster() == true {
				exitloop = 9999999
			}
		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Proxy can join master")
	} else {
		cluster.LogPrintf(LvlErr, "Proxy to join master timeout")
		return errors.New("Failed to join master via proxy")
	}
	return nil
}

func (cluster *Cluster) WaitMariaDBStop(server *ServerMonitor) error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting MariaDB shutdown")
			exitloop++
			_, err := os.FindProcess(server.Process.Pid)
			if err != nil {
				exitloop = 9999999
			}

		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "MariaDB shutdown")
	} else {
		cluster.LogPrintf(LvlInfo, "MariaDB shutdown timeout")
		return errors.New("Failed to Stop MariaDB")
	}
	return nil
}

func (cluster *Cluster) WaitDatabaseStart(server *ServerMonitor) error {
	return server.WaitDatabaseStart()
}

func (cluster *Cluster) WaitDatabaseSuspect(server *ServerMonitor) error {
	cluster.LogPrintf(LvlInfo, "Wait state suspect on %s", server.URL)
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:

			exitloop++

			err := server.Refresh()
			if err != nil {

				exitloop = 9999999
			} else {
				cluster.LogPrintf(LvlInfo, "Waiting state suspect on %s failed with error %s ", server.URL, err)
			}
		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Waiting state suspect reach on %s", server.URL)
	} else {
		cluster.LogPrintf(LvlInfo, "Wait state suspect timeout on %s", server.URL)
		return errors.New("Failed to wait state suspect")
	}
	return nil
}

func (cluster *Cluster) WaitDatabaseFailed(server *ServerMonitor) error {
	cluster.LogPrintf(LvlInfo, "Waiting state failed on %s", server.URL)
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:

			exitloop++

			if server.IsInStateFailed() {
				exitloop = 9999999
			} else {
				cluster.LogPrintf(LvlInfo, "Waiting state failed on %s %d current state:%s", server.URL, exitloop, server.State)
			}
		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Waiting state failed reach on %s", server.URL)
	} else {
		cluster.LogPrintf(LvlInfo, "Wait state failed timeout on %s", server.URL)
		return errors.New("Failed to wait state failed")
	}
	return nil
}

func (cluster *Cluster) WaitBootstrapDiscovery() error {
	cluster.LogPrintf(LvlInfo, "Waiting Bootstrap and discovery")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting Bootstrap and discovery")
			exitloop++
			if cluster.StateMachine.IsDiscovered() {
				exitloop = 9999999
			}

		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Cluster is bootstraped and discovered")
	} else {
		cluster.LogPrintf(LvlErr, "Bootstrap timeout")
		return errors.New("Failed Bootstrap timeout")
	}
	return nil
}

func (cluster *Cluster) waitMasterDiscovery() error {
	cluster.LogPrintf(LvlInfo, "Waiting Master Found")
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting Master Found")
			exitloop++
			if cluster.GetMaster() != nil {
				exitloop = 9999999
			}

		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "Master founded")
	} else {
		cluster.LogPrintf(LvlErr, "Master found timeout")
		return errors.New("Failed Master search timeout")
	}
	return nil
}

func (cluster *Cluster) AllDatabaseCanConn() bool {
	for _, s := range cluster.Servers {
		if s.IsDown() {
			return false
		}
	}
	return true
}

func (cluster *Cluster) WaitDatabaseCanConn() error {
	exitloop := 0
	ticker := time.NewTicker(time.Millisecond * time.Duration(cluster.Conf.MonitoringTicker*1000))

	cluster.LogPrintf(LvlInfo, "Waiting for cluster to start")
	for int64(exitloop) < cluster.Conf.MonitorWaitRetry {
		select {
		case <-ticker.C:
			cluster.LogPrintf(LvlInfo, "Waiting for cluster to start")
			exitloop++
			if cluster.AllDatabaseCanConn() && cluster.HasAllDbUp() {
				exitloop = 9999999
			}

		}
	}
	if exitloop == 9999999 {
		cluster.LogPrintf(LvlInfo, "All databases can connect")
	} else {
		cluster.LogPrintf(LvlErr, "Timeout waiting for database to be connected")
		return errors.New("Connections to databases failure")
	}
	return nil
}
