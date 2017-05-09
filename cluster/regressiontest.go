// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bytes"
	"log"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/tanji/replication-manager/config"
	"github.com/tanji/replication-manager/dbhelper"
)

const recoverTime = 8

var savedConf config.Config
var savedFailoverCtr int
var savedFailoverTs int64

func (cluster *Cluster) PrepareBench() error {
	if cluster.benchmarkType == "sysbench" {
		var prepare = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=1000000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=60 --oltp-test-mode=complex  --max-requests=0 --num-threads=4 prepare"
		cluster.LogPrintf("BENCH", "%s", prepare)
		var cmdprep *exec.Cmd

		cmdprep = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=1000000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time=60", "--oltp-test-mode=complex", "--max-requests=0", "--num-threads=4", "prepare")
		var outprep bytes.Buffer
		cmdprep.Stdout = &outprep

		cmdprepErr := cmdprep.Run()
		if cmdprepErr != nil {
			cluster.LogPrintf("ERROR", "%s", cmdprepErr)
			return cmdprepErr
		}
		cluster.LogPrintf("BENCH", "%s", outprep.String())
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR", "%s %s", err.Error(), result)
		} else {
			cluster.LogPrintf("BENCH", "%s", result)
		}
	}
	return nil
}

func (cluster *Cluster) CleanupBench() error {
	if cluster.benchmarkType == "sysbench" {
		var cleanup = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=10000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=60 --oltp-test-mode=complex  --max-requests=0 --num-threads=4 cleanup"
		cluster.LogPrintf("BENCHMARK : %s", cleanup)
		var cmdcls *exec.Cmd
		cmdcls = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=10000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time=60", "--oltp-test-mode=complex", "--max-requests=0", "--num-threads=4", "cleanup")
		var outcls bytes.Buffer
		cmdcls.Stdout = &outcls

		cmdclsErr := cmdcls.Run()
		if cmdclsErr != nil {
			cluster.LogPrintf("ERROR", "%s", cmdclsErr)
			return cmdclsErr
		}
		cluster.LogPrintf("BENCH", "%s", outcls.String())
	}
	if cluster.benchmarkType == "table" {

		err := dbhelper.BenchCleanup(cluster.GetMaster().Conn)
		if err != nil {
			cluster.LogPrintf("ERROR", "%s", err.Error())
		}
	}
	return nil
}

func (cluster *Cluster) ChecksumBench() bool {
	if cluster.benchmarkType == "table" {
		if cluster.CheckTableConsistency("replication_manager_schema.bench") != true {
			cluster.LogPrintf("ERROR", "Inconsitant slave")
			return false
		}
	}
	if cluster.benchmarkType == "sysbench" {
		if cluster.CheckTableConsistency("test.sbtest") != true {
			cluster.LogPrintf("ERROR", "Inconsitant slave")
			return false
		}
	}
	return true
}

func (cluster *Cluster) RunBench() error {
	if cluster.benchmarkType == "sysbench" {
		var run = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=1000000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=" + strconv.Itoa(cluster.conf.SysbenchTime) + "--oltp-test-mode=complex --max-requests=0 --num-threads=" + strconv.Itoa(cluster.conf.SysbenchThreads) + " run"
		cluster.LogPrintf("BENCH", "%s", run)
		var cmdrun *exec.Cmd

		cmdrun = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=1000000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time="+strconv.Itoa(cluster.conf.SysbenchTime), "--oltp-test-mode=complex", "--max-requests=0", "--num-threads="+strconv.Itoa(cluster.conf.SysbenchThreads), "run")
		var outrun bytes.Buffer
		cmdrun.Stdout = &outrun

		cmdrunErr := cmdrun.Run()
		if cmdrunErr != nil {
			cluster.LogPrintf("ERROR", "%s", cmdrunErr)
			return cmdrunErr
		}
		cluster.LogPrintf("BENCH", "%s", outrun.String())
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf("ERROR", "%s %s", err.Error(), result)
		}
	}
	return nil

}

func (cluster *Cluster) RunSysbench() error {
	cluster.CleanupBench()
	cluster.PrepareBench()
	cluster.RunBench()
	return nil
}

func (cluster *Cluster) CheckSlavesRunning() bool {
	time.Sleep(2 * time.Second)
	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("TEST", "Slave  %s issue on replication  SQL Thread %s IO Thread %s ", s.URL, s.SQLThread, s.IOThread)

			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("TEST", "Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) CheckTableConsistency(table string) bool {
	checksum, err := dbhelper.ChecksumTable(cluster.master.Conn, table)

	if err != nil {
		cluster.LogPrintf("ERROR", "Failed to take master checksum table ")
	} else {
		cluster.LogPrintf("INFO", "Checksum master table %s =  %s %s", table, checksum, cluster.master.URL)
	}
	var count int
	err = cluster.master.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
	if err != nil {
		cluster.LogPrintf("ERROR", "Could count record in bench table", err)
	} else {
		cluster.LogPrintf("INFO", "Number of rows master table %s = %d %s", table, count, cluster.master.URL)
	}
	var max int
	if cluster.benchmarkType == "table" {

		err = cluster.master.Conn.QueryRowx("select max(val) from " + table).Scan(&max)
		if err != nil {
			cluster.LogPrintf("ERROR", "Could get max val in bench table", err)
		} else {
			cluster.LogPrintf("INFO", "Max Value in bench table %s = %d %s", table, max, cluster.master.URL)
		}
	}
	ctslave := 0
	for _, s := range cluster.slaves {
		ctslave++

		checksumslave, err := dbhelper.ChecksumTable(s.Conn, table)
		if err != nil {
			cluster.LogPrintf("ERROR", "Failed to take slave checksum table ")
		} else {
			cluster.LogPrintf("INFO", "Checksum slave table %s = %s on %s ", table, checksumslave, s.URL)
		}
		err = s.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
		if err != nil {
			log.Println("ERROR: Could not check long running writes", err)
		} else {
			cluster.LogPrintf("INFO", "Number of rows slave table %s =  %d %s", table, count, s.URL)
		}
		var maxslave int
		if cluster.benchmarkType == "table" {
			err = s.Conn.QueryRowx("select max(val) from " + table).Scan(&maxslave)
			if err != nil {
				cluster.LogPrintf("ERROR", "Could get max val in bench table", err)
			} else {
				cluster.LogPrintf("INFO", "Max Value in bench table %s = %d %s", table, maxslave, s.URL)
			}
		}
		if checksumslave != checksum && cluster.benchmarkType == "sysbench" {
			cluster.LogPrintf("ERROR", "Checksum on slave is different from master")
			return false
		}
		if maxslave != max && cluster.benchmarkType == "table" {
			cluster.LogPrintf("ERROR", "Max table value on slave is different from master")
			return false
		}
	}
	if ctslave == 0 {
		cluster.LogPrintf("ERROR", "No slaves while checking consistancy")
		return false
	}
	return true
}

func (cluster *Cluster) DelayAllSlaves() error {
	cluster.LogPrintf("BENCH", "Stopping slaves, injecting data & long transaction")
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("BENCH", "%s %s", err.Error(), result)
	}
	dbhelper.InjectLongTrx(cluster.master.Conn, 10)
	time.Sleep(10 * time.Second)
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	return nil
}

func (cluster *Cluster) InitTestCluster(conf string, test string) bool {
	savedConf = cluster.conf
	savedFailoverCtr = cluster.failoverCtr
	savedFailoverTs = cluster.failoverTs
	if cluster.testStartCluster {
		cluster.InitClusterSemiSync()
	}
	cluster.CleanAll = true
	err := cluster.Bootstrap()
	if err != nil {
		cluster.LogPrintf("TEST", "Abording test, bootstrap failed, %s", err)
		cluster.ShutdownClusterSemiSync()
		return false
	}
	//cluster.waitFailoverEndState()
	cluster.WaitBootstrapDiscovery()
	cluster.initProxies()
	if cluster.master == nil {
		cluster.LogPrintf("TEST", "Abording test, no master found")
		cluster.ShutdownClusterSemiSync()
		return false
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("ERROR", "Insert some events %s %s", err.Error(), result)
		cluster.ShutdownClusterSemiSync()
	}
	time.Sleep(2 * time.Second)

	cluster.LogPrintf("TEST", "Starting Test %s", test)
	return true
}

func (cluster *Cluster) CloseTestCluster(conf string, test string) bool {
	if cluster.testStopCluster {
		cluster.ShutdownClusterSemiSync()
	}
	cluster.RestoreConf()

	return true
}

func (cluster *Cluster) SwitchoverWaitTest() {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitSwitchover(wg)
	cluster.switchoverChan <- true
	wg.Wait()
}

func (cluster *Cluster) RestoreConf() {
	cluster.conf = savedConf
	cluster.failoverTs = savedFailoverTs
	cluster.failoverCtr = savedFailoverCtr

}

func (cluster *Cluster) DisableSemisync() error {
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {

			return err
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {

			return err
		}
	}
	return nil
}
func (cluster *Cluster) EnableSemisync() error {
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {

			return err
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {

			return err
		}
	}
	return nil
}
func (cluster *Cluster) StopSlaves() error {
	cluster.LogPrintf("BENCH", "Stopping replication")
	for _, s := range cluster.slaves {
		err := dbhelper.StopSlave(s.Conn)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) StartSlaves() error {
	cluster.LogPrintf("BENCH", "Sarting replication")
	for _, s := range cluster.slaves {
		err := dbhelper.StartSlave(s.Conn)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) ForgetTopology() error {
	cluster.master = nil
	cluster.slaves = nil

	return nil
}
