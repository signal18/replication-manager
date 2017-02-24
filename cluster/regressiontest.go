// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"bytes"
	"os/exec"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/tanji/replication-manager/config"
	"github.com/tanji/replication-manager/dbhelper"
)

var tests = []string{
	"testSwitchOverLongTransactionNoRplCheckNoSemiSync",
	"testSwitchOverLongQueryNoRplCheckNoSemiSync",
	"testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync",
	"testSlaReplAllDelay",
	"testFailoverReplAllDelayInteractive",
	"testFailoverAssyncAutoRejoinFlashback",
	"testFailoverSemisyncAutoRejoinFlashback",
	"testFailoverAssyncAutoRejoinNowrites",
	"testSwitchoverReplAllDelay",
	"testSlaReplAllSlavesStopNoSemiSync",
	"testSwitchOverReadOnlyNoRplCheck",
	"testSwitchOverNoReadOnlyNoRplCheck",
	"testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck",
	"testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck",
	"testSwitchOverBackPreferedMasterNoRplCheckSemiSync",
	"testSwitchOverAllSlavesStopRplCheckNoSemiSync",
	"testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck",
	"testSwitchOverAllSlavesDelayRplCheckNoSemiSync",
	"testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync",
	"testFailOverAllSlavesDelayNoRplChecksNoSemiSync",
	"testFailOverAllSlavesDelayRplChecksNoSemiSync",
	"testFailOverNoRplChecksNoSemiSync",
	"testNumberFailOverLimitReach",
	"testFailOverTimeNotReach",
}

var savedConf config.Config
var savedFailoverCtr int
var savedFailoverTs int64

const recoverTime = 8

func (cluster *Cluster) GetTests() []string {
	return tests
}

func (cluster *Cluster) testSlaReplAllDelay() bool {
	return false
}

func (cluster *Cluster) testFailoverReplAllDelayInteractive() bool {
	return false
}

func (cluster *Cluster) testSwitchoverReplAllDelay() bool {
	return false
}

func (cluster *Cluster) RunAllTests(test string) bool {
	var allTests = map[string]string{}
	ret := true
	var res bool
	cluster.LogPrintf("TESTING : %s", test)

	if test == "testFailoverSemisyncAutoRejoinFlashback" || test == "ALL" {
		res = cluster.testFailoverSemisyncAutoRejoinFlashback("semisync.cnf", "testFailoverSemisyncAutoRejoinFlashback")
		allTests["1 Failover rejoin flashback <cluster.conf.RplChecks=false> <Semisync=ture> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinFlashback" || test == "ALL" {
		res = cluster.testFailoverAssyncAutoRejoinFlashback("semisync.cnf", "testFailoverAssyncAutoRejoinFlashback")
		allTests["1 Failover rejoin flashback <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinNowrites" || test == "ALL" {
		res = cluster.testFailoverAssyncAutoRejoinNowrites("semisync.cnf", "testFailoverAssyncAutoRejoinNowrites")
		allTests["1 Failover rejoin No Writes <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailoverAssyncAutoRejoinDump" || test == "ALL" {
		res = cluster.testFailoverAssyncAutoRejoinDump("semisync.cnf", "testFailoverAssyncAutoRejoinDump")
		allTests["1 Failover rejoin Dump <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testSwitchOverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverLongTransactionNoRplCheckNoSemiSync("semisync.cnf", "testSwitchOverLongTransactionNoRplCheckNoSemiSync")
		allTests["1 Switchover Concurrent Long Transaction <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testSwitchOverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverLongQueryNoRplCheckNoSemiSync("semisync.cnf", "testSwitchOverLongQueryNoRplCheckNoSemiSync")
		allTests["1 Switchover Concurrent Long Query <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverNoReadOnlyNoRplCheck("semisync.cnf", "testSwitchOverNoReadOnlyNoRplCheck")
		allTests["1 Switchover <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverReadOnlyNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverReadOnlyNoRplCheck("semisync.cnf", "testSwitchOverReadOnlyNoRplCheck")
		allTests["1 Switchover <cluster.conf.ReadOnly=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck("semisync.cnf", "testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck")
		allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck("semisync.cnf", "testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck")
		allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverBackPreferedMasterNoRplCheckSemiSync("semisync.cnf", "testSwitchOverBackPreferedMasterNoRplCheckSemiSync")
		allTests["2 Switchover Back Prefered Master <semisync=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesStopRplCheckNoSemiSync("semisync.cnf", "testSwitchOverAllSlavesStopRplCheckNoSemiSync")
		allTests["Can't Switchover All Slaves Stop  <semisync=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck("semisync.cnf", "testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck")
		allTests["Can Switchover All Slaves Stop <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesDelayRplCheckNoSemiSync("semisync.cnf", "testSwitchOverAllSlavesDelayRplCheckNoSemiSync")
		allTests["Can't Switchover All Slaves Delay <semisync=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync("semisync.cnf", "testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["Can Switchover All Slaves Delay <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = cluster.testSlaReplAllSlavesStopNoSemiSync("semisync.cnf", "testSlaReplAllSlavesStopNoSemiSync")
		allTests["SLA Decrease Can't Switchover All Slaves Stop <Semisync=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverNoRplChecksNoSemiSync("semisync.cnf", "testFailOverNoRplChecksNoSemiSync")
		allTests["1 Failover <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverAllSlavesDelayNoRplChecksNoSemiSync("semisync.cnf", "testFailOverAllSlavesDelayNoRplChecksNoSemiSync")
		allTests["1 Failover All Slave Delay <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverAllSlavesDelayRplChecksNoSemiSync("semisync.cnf", "testFailOverAllSlavesDelayRplChecksNoSemiSync")
		allTests["1 Failover All Slave Delay <cluster.conf.RplChecks=true> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testNumberFailOverLimitReach" || test == "ALL" {
		res = cluster.testNumberFailOverLimitReach("semisync.cnf", "testNumberFailOverLimitReach")
		allTests["1 Failover Number of Failover Reach <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverTimeNotReach" || test == "ALL" {
		res = cluster.testFailOverTimeNotReach("semisync.cnf", "testFailOverTimeNotReach")
		allTests["1 Failover Before Time Limit <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	keys := make([]string, 0, len(allTests))
	for key := range allTests {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, v := range keys {
		cluster.LogPrintf("TESTS : Result  %s -> %s", v, allTests[v])
	}

	cluster.CleanAll = false
	return ret
}

func (cluster *Cluster) PrepareBench() error {
	var prepare = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=1000000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=60 --oltp-test-mode=complex  --max-requests=0 --num-threads=4 prepare"
	cluster.LogPrintf("BENCHMARK : %s", prepare)
	var cmdprep *exec.Cmd

	cmdprep = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=1000000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time=60", "--oltp-test-mode=complex", "--max-requests=0", "--num-threads=4", "prepare")
	var outprep bytes.Buffer
	cmdprep.Stdout = &outprep

	cmdprepErr := cmdprep.Run()
	if cmdprepErr != nil {
		cluster.LogPrintf("ERRROR : %s", cmdprepErr)
		return cmdprepErr
	}
	cluster.LogPrintf("BENCHMARK : %s", outprep.String())
	return nil
}

func (cluster *Cluster) CleanupBench() error {
	var cleanup = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=10000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=60 --oltp-test-mode=complex  --max-requests=0 --num-threads=4 cleanup"
	cluster.LogPrintf("BENCHMARK : %s", cleanup)
	var cmdcls *exec.Cmd
	cmdcls = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=10000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time=60", "--oltp-test-mode=complex", "--max-requests=0", "--num-threads=4", "cleanup")
	var outcls bytes.Buffer
	cmdcls.Stdout = &outcls

	cmdclsErr := cmdcls.Run()
	if cmdclsErr != nil {
		cluster.LogPrintf("ERRROR : %s", cmdclsErr)
		return cmdclsErr
	}
	cluster.LogPrintf("BENCHMARK : %s", outcls.String())
	return nil
}

func (cluster *Cluster) RunBench() error {
	var run = cluster.conf.SysbenchBinaryPath + " --test=oltp --oltp-table-size=1000000 --mysql-db=test --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=127.0.0.1 --mysql-port=" + strconv.Itoa(cluster.conf.HaproxyWritePort) + " --max-time=" + strconv.Itoa(cluster.conf.SysbenchTime) + "--oltp-test-mode=complex --max-requests=0 --num-threads=" + strconv.Itoa(cluster.conf.SysbenchThreads) + " run"
	cluster.LogPrintf("BENCHMARK : %s", run)
	var cmdrun *exec.Cmd

	cmdrun = exec.Command(cluster.conf.SysbenchBinaryPath, "--test=oltp", "--oltp-table-size=1000000", "--mysql-db=test", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host=127.0.0.1", "--mysql-port="+strconv.Itoa(cluster.conf.HaproxyWritePort), "--max-time="+strconv.Itoa(cluster.conf.SysbenchTime), "--oltp-test-mode=complex", "--max-requests=0", "--num-threads="+strconv.Itoa(cluster.conf.SysbenchThreads), "run")
	var outrun bytes.Buffer
	cmdrun.Stdout = &outrun

	cmdrunErr := cmdrun.Run()
	if cmdrunErr != nil {
		cluster.LogPrintf("ERRROR : %s", cmdrunErr)
		return cmdrunErr
	}
	cluster.LogPrintf("BENCHMARK : %s", outrun.String())
	return nil

}

func (cluster *Cluster) RunSysbench() error {
	cluster.CleanupBench()
	cluster.PrepareBench()
	cluster.RunBench()
	return nil
}

func (cluster *Cluster) checkSlavesRunning() bool {
	time.Sleep(2 * time.Second)
	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("TEST : Slave  %s issue on replication  SQL Thread %s IO Thread %s ", s.URL, s.SQLThread, s.IOThread)

			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("TEST :  Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) checkTableConsistency(table string) bool {
	checksum, err := dbhelper.ChecksumTable(cluster.master.Conn, table)
	if err != nil {
		cluster.LogPrintf("Failed to take master checksum table ")
	} else {
		cluster.LogPrintf("Checksum master table test.sbtest =  %s ", checksum)
	}

	ctslave := 0
	for _, s := range cluster.slaves {
		ctslave++
		checksumslave, err := dbhelper.ChecksumTable(s.Conn, table)
		if err != nil {
			cluster.LogPrintf("Failed to take slave checksum table ")
		} else {
			cluster.LogPrintf("Checksum slave table test.sbtest =  %s ", checksum)
		}
		if checksumslave != checksum {
			cluster.LogPrintf("ERROR: Checksum on slave is different from master")
			return false
		}
	}
	if ctslave == 0 {
		cluster.LogPrintf("ERROR:  No slaves while checking consistancy")
		return false
	}
	return true
}

func (cluster *Cluster) DelayAllSlaves() error {
	cluster.LogPrintf("BENCH : Stopping slaves, injecting data & long transaction")
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
	}
	dbhelper.InjectLongTrx(cluster.master.Conn, 10)
	time.Sleep(10 * time.Second)
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	return nil
}

func (cluster *Cluster) initTestCluster(conf string, test string) bool {
	savedConf = cluster.conf
	savedFailoverCtr = cluster.failoverCtr
	savedFailoverTs = cluster.failoverTs
	if cluster.testStartCluster {
		cluster.InitClusterSemiSync()
	}
	cluster.CleanAll = true
	err := cluster.Bootstrap()
	if err != nil {
		cluster.LogPrintf("TEST : Abording test, bootstrap failed, %s", err)
		cluster.ShutdownClusterSemiSync()
		return false
	}
	//cluster.waitFailoverEndState()
	cluster.waitBootstrapDiscovery()

	if cluster.master == nil {
		cluster.LogPrintf("TEST : Abording test, no master found")
		cluster.ShutdownClusterSemiSync()
		return false
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("ERROR : Insert some events %s %s", err.Error(), result)
		cluster.ShutdownClusterSemiSync()
	}
	time.Sleep(2 * time.Second)

	cluster.LogPrintf("TESTING : Starting Test %s", test)
	return true
}

func (cluster *Cluster) closeTestCluster(conf string, test string) bool {
	if cluster.testStopCluster {
		cluster.ShutdownClusterSemiSync()
	}
	cluster.restoreConf()

	return true
}

func (cluster *Cluster) switchoverWaitTest() {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.waitSwitchover(wg)
	cluster.switchoverChan <- true
	wg.Wait()
}

func (cluster *Cluster) restoreConf() {
	cluster.conf.RplChecks = savedConf.RplChecks
	cluster.conf.ReadOnly = savedConf.ReadOnly
	cluster.conf.PrefMaster = savedConf.PrefMaster
	cluster.conf.Interactive = savedConf.Interactive
	cluster.conf.MaxDelay = savedConf.MaxDelay
	cluster.conf.FailLimit = savedConf.FailLimit
	cluster.conf.FailTime = savedConf.FailTime
	cluster.conf.Autorejoin = savedConf.Autorejoin
	cluster.conf.AutorejoinBackupBinlog = savedConf.AutorejoinBackupBinlog
	cluster.conf.AutorejoinFlashback = savedConf.AutorejoinFlashback
	cluster.conf.AutorejoinMysqldump = savedConf.AutorejoinMysqldump
	cluster.conf.AutorejoinSemisync = savedConf.AutorejoinSemisync
	cluster.failoverTs = savedFailoverTs
	cluster.failoverCtr = savedFailoverCtr
	cluster.conf.CheckFalsePositiveHeartbeat = savedConf.CheckFalsePositiveHeartbeat

}

func (cluster *Cluster) disableSemisync() error {
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
func (cluster *Cluster) enableSemisync() error {
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
func (cluster *Cluster) stopSlaves() error {
	cluster.LogPrintf("BENCH: Stopping replication")
	for _, s := range cluster.slaves {
		err := dbhelper.StopSlave(s.Conn)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) startSlaves() error {
	cluster.LogPrintf("BENCH : Sarting replication")
	for _, s := range cluster.slaves {
		err := dbhelper.StartSlave(s.Conn)
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) getTestResultLabel(res bool) string {
	if res == false {
		return "FAILED"
	} else {
		return "PASS"
	}
}
