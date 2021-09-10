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
	"bytes"
	"errors"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/dbhelper"
)

const recoverTime = 8

var savedConf config.Config
var savedFailoverCtr int
var savedFailoverTs int64

type Test struct {
	Name       string        `json:"name"`
	Result     string        `json:"result"`
	ConfigFile string        `json:"config-file"`
	ConfigInit config.Config `json:"config-init"`
	ConfigTest config.Config `json:"config-test"`
}

func (cluster *Cluster) PrepareBench() error {
	prx := cluster.GetProxies()[0]
	if prx == nil {
		return errors.New("No proxy")
	}

	if cluster.benchmarkType == "sysbench" {
		test := "--test=oltp"
		threads := "--num-threads=4"
		tablesize := "--oltp-table-size=1000000"
		requests := "--max-requests=0"
		time := "--max-time=60"
		mode := "--oltp-test-mode=complex"
		var cmdprep *exec.Cmd
		cmdprep = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.dbUser, "--mysql-password="+cluster.dbPass, "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, mode, requests, threads, "prepare")

		if cluster.Conf.SysbenchV1 {
			test = "oltp_read_write"
			tablesize = "--table-size=1000000"
			threads = "--threads=4"
			requests = "" //			--events=N
			time = "--time=60"
			mode = ""
			cmdprep = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.dbUser, "--mysql-password="+cluster.dbPass, "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "prepare")

		}

		cluster.LogPrintf(LvlInfo, "Command: %s", strings.Replace(cmdprep.String(), cluster.dbPass, "XXXX", -1))

		out, err := cmdprep.CombinedOutput()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s , %s", string(out), err)
			return err
		}
		cluster.LogPrintf("BENCH", "%s", string(out))
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s %s", err.Error(), result)
		} else {
			cluster.LogPrintf("BENCH", "%s", result)
		}
	}
	return nil
}

func (cluster *Cluster) CleanupBench() error {
	prx := cluster.GetProxies()[0]
	if prx == nil {
		return errors.New("No proxy")
	}
	if cluster.benchmarkType == "sysbench" {
		test := "--test=oltp"
		if cluster.Conf.SysbenchV1 {
			test = "oltp_read_write"
		}
		var cleanup = cluster.Conf.SysbenchBinaryPath + " --test=oltp  --db-driver=mysql --mysql-db=replication_manager_schema --mysql-user=" + cluster.rplUser + " --mysql-password=" + cluster.rplPass + " --mysql-host=" + prx.GetHost() + " --mysql-port=" + strconv.Itoa(prx.GetWritePort()) + " cleanup"
		cluster.LogPrintf("BENCH", "%s", strings.Replace(cleanup, cluster.rplPass, "XXXXX", -1))
		var cmdcls *exec.Cmd
		cmdcls = exec.Command(cluster.Conf.SysbenchBinaryPath, test, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.rplUser, "--mysql-password="+cluster.rplPass, "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), "cleanup")
		var outcls bytes.Buffer
		cmdcls.Stdout = &outcls

		cmdclsErr := cmdcls.Run()
		if cmdclsErr != nil {
			cluster.LogPrintf(LvlErr, "%s", cmdclsErr)
			return cmdclsErr
		}
		cluster.LogPrintf("BENCH", "%s", strings.Replace(outcls.String(), cluster.rplPass, "XXXXX", -1))
	}
	if cluster.benchmarkType == "table" {

		err := dbhelper.BenchCleanup(cluster.GetMaster().Conn)
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s", err.Error())
		}
	}
	return nil
}

func (cluster *Cluster) ChecksumBench() bool {
	if cluster.benchmarkType == "table" {
		if cluster.CheckTableConsistency("replication_manager_schema.bench") != true {
			cluster.LogPrintf(LvlErr, "Inconsitant slave")
			return false
		}
	}
	if cluster.benchmarkType == "sysbench" {
		if cluster.CheckTableConsistency("test.sbtest") != true {
			cluster.LogPrintf(LvlErr, "Inconsitant slave")
			return false
		}
	}
	return true
}

func (cluster *Cluster) RunBench() error {
	prx := cluster.GetProxies()[0]
	if prx == nil {
		return errors.New("No proxy")
	}

	if cluster.benchmarkType == "sysbench" {

		test := "--test=oltp"
		threads := "--num-threads=" + strconv.Itoa(cluster.Conf.SysbenchThreads)
		tablesize := "--oltp-table-size=1000000"
		requests := "--max-requests=0"
		time := "--max-time=" + strconv.Itoa(cluster.Conf.SysbenchTime)
		mode := "--oltp-test-mode=complex"
		var cmdrun *exec.Cmd
		cmdrun = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.dbUser, "--mysql-password="+cluster.dbPass, "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, mode, requests, threads, "run")
		if cluster.Conf.SysbenchV1 {
			test = "oltp_read_write"
			tablesize = "--table-size=1000000"
			threads = "--threads=" + strconv.Itoa(cluster.Conf.SysbenchThreads)
			requests = "" //			--events=N
			time = "--time=" + strconv.Itoa(cluster.Conf.SysbenchTime)
			cmdrun = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.dbUser, "--mysql-password="+cluster.dbPass, "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "run")
		}
		cluster.LogPrintf(LvlInfo, "Command: %s", strings.Replace(cmdrun.String(), cluster.dbPass, "XXXX", -1))

		out, err := cmdrun.CombinedOutput()
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s , %s", string(out), err)
			return err
		}
		cluster.LogPrintf("BENCH", "%s", string(out))
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogPrintf(LvlErr, "%s %s", err.Error(), result)
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
		ss, errss := s.GetSlaveStatus(s.ReplicationSourceName)
		if errss != nil {
			return false
		}
		if ss.SlaveIORunning.String != "Yes" || ss.SlaveSQLRunning.String != "Yes" {
			cluster.LogPrintf("TEST", "Slave  %s issue on replication  SQL Thread %s IO Thread %s ", s.URL, ss.SlaveSQLRunning.String, ss.SlaveIORunning.String)

			return false
		}
		if ss.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("TEST", "Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) CheckTableConsistency(table string) bool {
	checksum, err := dbhelper.ChecksumTable(cluster.master.Conn, table)

	if err != nil {
		cluster.LogPrintf(LvlErr, "Failed to take master checksum table ")
	} else {
		cluster.LogPrintf(LvlInfo, "Checksum master table %s =  %s %s", table, checksum, cluster.master.URL)
	}
	var count int
	err = cluster.master.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Could count record in bench table", err)
	} else {
		cluster.LogPrintf(LvlInfo, "Number of rows master table %s = %d %s", table, count, cluster.master.URL)
	}
	var max int
	if cluster.benchmarkType == "table" {

		err = cluster.master.Conn.QueryRowx("select max(val) from " + table).Scan(&max)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Could get max val in bench table", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Max Value in bench table %s = %d %s", table, max, cluster.master.URL)
		}
	}
	ctslave := 0
	for _, s := range cluster.slaves {
		ctslave++

		checksumslave, err := dbhelper.ChecksumTable(s.Conn, table)
		if err != nil {
			cluster.LogPrintf(LvlErr, "Failed to take slave checksum table ")
		} else {
			cluster.LogPrintf(LvlInfo, "Checksum slave table %s = %s on %s ", table, checksumslave, s.URL)
		}
		err = s.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
		if err != nil {
			log.Println("ERROR: Could not check long running writes", err)
		} else {
			cluster.LogPrintf(LvlInfo, "Number of rows slave table %s =  %d %s", table, count, s.URL)
		}
		var maxslave int
		if cluster.benchmarkType == "table" {
			err = s.Conn.QueryRowx("select max(val) from " + table).Scan(&maxslave)
			if err != nil {
				cluster.LogPrintf(LvlErr, "Could get max val in bench table", err)
			} else {
				cluster.LogPrintf(LvlInfo, "Max Value in bench table %s = %d %s", table, maxslave, s.URL)
			}
		}
		if checksumslave != checksum && cluster.benchmarkType == "sysbench" {
			cluster.LogPrintf(LvlErr, "Checksum on slave is different from master")
			return false
		}
		if maxslave != max && cluster.benchmarkType == "table" {
			cluster.LogPrintf(LvlErr, "Max table value on slave is different from master")
			return false
		}
	}
	if ctslave == 0 {
		cluster.LogPrintf(LvlErr, "No slaves while checking consistancy")
		return false
	}
	return true
}
func (cluster *Cluster) FailoverAndWait() {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.StopDatabaseService(cluster.GetMaster())
	wg.Wait()
}

func (cluster *Cluster) FailoverNow() {
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go cluster.WaitFailover(wg)
	cluster.SetMasterStateFailed()
	cluster.SetInteractive(false)
	cluster.GetMaster().FailCount = cluster.GetMaxFail()
	wg.Wait()
}

func (cluster *Cluster) StartDatabaseWaitRejoin(server *ServerMonitor) error {
	wg2 := new(sync.WaitGroup)
	wg2.Add(1)
	go cluster.WaitRejoin(wg2)
	err := cluster.StartDatabaseService(server)
	wg2.Wait()
	return err
}

func (cluster *Cluster) DelayAllSlaves() error {
	cluster.LogPrintf("BENCH", "Stopping slaves, injecting data & long transaction")
	for _, s := range cluster.slaves {
		_, err := s.StopSlaveSQLThread()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Stopping slave on %s %s", s.URL, err)
		}
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 1000)
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s %s", err.Error(), result)
	}
	err = dbhelper.InjectLongTrx(cluster.master.Conn, 12)
	if err != nil {
		cluster.LogPrintf(LvlErr, "InjectLongTrx %s", err.Error())
	}
	result, err = dbhelper.WriteConcurrent2(cluster.master.DSN, 1000)
	if err != nil {
		cluster.LogPrintf(LvlErr, "%s %s", err.Error(), result)
	}
	for _, s := range cluster.slaves {
		_, err := s.StartSlave()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Staring slave on %s %s", s.URL, err)
		}
	}
	time.Sleep(5 * time.Second)
	return nil
}

func (cluster *Cluster) InitBenchTable() error {

	result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Insert some events %s %s", err.Error(), result)
		return err
	}
	return nil
}

func (cluster *Cluster) InitTestCluster(conf string, test *Test) bool {
	test.ConfigInit = cluster.Conf
	savedConf = cluster.Conf
	savedFailoverCtr = cluster.FailoverCtr
	savedFailoverTs = cluster.FailoverTs
	cluster.CleanAll = true
	if cluster.testStopCluster {
		err := cluster.Bootstrap()
		if err != nil {
			cluster.LogPrintf(LvlErr, "Abording test, bootstrap failed, %s", err)
			cluster.Unprovision()
			return false
		}
	}
	cluster.LogPrintf(LvlInfo, "Starting Test %s", test.Name)
	return true
}

func (cluster *Cluster) CloseTestCluster(conf string, test *Test) bool {
	test.ConfigTest = cluster.Conf
	if cluster.testStopCluster {
		cluster.Unprovision()
		cluster.WaitClusterStop()
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
	cluster.Conf = savedConf
	cluster.FailoverTs = savedFailoverTs
	cluster.FailoverCtr = savedFailoverCtr

}

func (cluster *Cluster) DisableSemisync() error {
	for _, s := range cluster.Servers {
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
	for _, s := range cluster.Servers {
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
		_, err := s.StopSlave()
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) StartSlaves() error {
	cluster.LogPrintf("BENCH", "Sarting replication")
	for _, s := range cluster.slaves {
		_, err := s.StartSlave()
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) ForgetTopology() error {
	cluster.master = nil
	cluster.vmaster = nil
	cluster.slaves = nil
	return nil
}
