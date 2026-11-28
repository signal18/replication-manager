// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package regtest

import (
	"sync"
	"time"

	"github.com/signal18/replication-manager/cluster"
	clusterpkg "github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

func (regtest *RegTest) TestFailoverAssyncAutoRejoinFlashback(cluster *cluster.Cluster, conf string, test *cluster.Test) bool {

	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)
	cluster.DisableSemisync()
	SaveMaster := cluster.GetMaster()
	SaveMasterURL := SaveMaster.URL
	// the load runs through the failover and the rejoin; its end is waited for before the
	// checksum, which compares the master against replicas that must have applied all of it
	// The prepare (DDL and its bulk insert) ends before the master is cut: lost events holding
	// DDL are not flashbackable and the rejoin skips the flashback (seen when the master died
	// during the prepare), so only the row load of the run is lost and rewound.
	if err := cluster.ResetBench(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Benchmark prepare failed: %s", err)
		return false
	}
	load := make(chan error, 1)
	go func() { load <- cluster.RunBench() }()
	time.Sleep(4 * time.Second)
	// A light load is shipped whole before a clean stop of the master: nothing is lost and the
	// flashback never runs (seen with two sysbench threads, 0 lost transactions). The replicas
	// stop receiving (IO thread) for a few seconds of load first, so the master commits
	// transactions no replica has: the lost transactions the flashback must rewind. The
	// failover repoints every replica and starts its link.
	for _, s := range cluster.GetSlaves() {
		if _, err := s.ReplicationLink().StopCopyEvents(clusterpkg.LinkByTest); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Stop the IO thread on %s: %s", s.URL, err)
			return false
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "IO threads of the replicas stopped, the load goes on on %s only", SaveMasterURL)
	time.Sleep(3 * time.Second)
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(SaveMaster)
	wg.Wait()
	/// give time to start the failover

	if cluster.GetMaster() != nil && cluster.GetMaster().URL == SaveMasterURL {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", " Old master %s ==  Next master %s  ", SaveMasterURL, cluster.GetMaster().URL)
		return false
	}

	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	cluster.StartDatabaseService(SaveMaster)
	wg2.Wait()

	// A checksum taken while sysbench still writes compares a moving master (about 90 s after
	// the failover the load was still running): wait for the load to end, bounded by its
	// duration and a margin for its prepare, never a blind sleep.
	select {
	case err := <-load:
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Sysbench ended: %s", err)
		}
	case <-time.After(time.Duration(cluster.Conf.SysbenchTime)*time.Second + 10*time.Minute):
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Sysbench still running %ds after its start: no consistency check under load", cluster.Conf.SysbenchTime)
		return false
	}
	if cluster.CheckTableConsistency(cluster.BenchTable()) != true {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Inconsitant slave")
		return false
	}

	return true
}
