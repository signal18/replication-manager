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
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
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

type SysbenchRecord struct {
	Second          int32
	Threads         int32
	TPS             float64
	QPS             float64
	ReadQPS         float64
	WriteQPS        float64
	OtherQPS        float64
	Latency         float64
	LatencyLimit    float64
	ErrorPerSec     float64
	ReconnectPerSec float64
}

type SysBenchTpcResultPerMinute struct {
	Threads int32
	Tpc     int32
	Errors  int32
}

type SysbenchRecordViolation struct {
	RecordIndex int
	RecordText  string
	Description string
}

type SysbenchAnalyzer struct {
	ignoreInvalidLines bool
}
//  30s ] thds: 4 tps: 67.00 qps: 1892.93 (r/w/o: 874.97/883.97/134.00) lat (ms,95%): 137.35 err/s 0.00 reconn/s: 0.00
const (
	recordFormat = "[ %ds ] thds: %d tps: %f qps: %f (r/w/o: %f/%f/%f) lat (ms,%f%%): %f err/s %f reconn/s: %f"
)

func ParseRecord(str string) (SysbenchRecord, error) {
	record := SysbenchRecord{}

	_, err := fmt.Sscanf(
		str,
		recordFormat,
		&record.Second,
		&record.Threads,
		&record.TPS,
		&record.QPS,
		&record.ReadQPS,
		&record.WriteQPS,
		&record.OtherQPS,
		&record.LatencyLimit,
		&record.Latency,
		&record.ErrorPerSec,
		&record.ReconnectPerSec)
	if err != nil {
		return SysbenchRecord{}, err
	}
	return record, nil
}

func (a *SysbenchAnalyzer) ParseToEnd(reader io.Reader) ([]SysbenchRecord, []SysbenchRecordViolation) {
	var records []SysbenchRecord
	var violations []SysbenchRecordViolation

	scanner := bufio.NewScanner(reader)
	for {
		if !scanner.Scan() {
			break
		}
		str := scanner.Text()
		record, err := ParseRecord(str)
		if err == nil {
			records = append(records, record)
		} else {
			if !a.ignoreInvalidLines {
				violation := SysbenchRecordViolation{
					RecordIndex: -1,
					RecordText:  str,
					Description: fmt.Sprintf("Invalid line. Err: %v", err),
				}
				violations = append(violations, violation)
			}
		}
	}

	return records, violations
}

func (cluster *Cluster) NewSysbenchAnalyzer(ignoreInvalidLines bool) SysbenchAnalyzer {
	return SysbenchAnalyzer{
		ignoreInvalidLines: ignoreInvalidLines,
	}
}

func (cluster *Cluster) PrepareBench() error {
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
		tables := "--tables=" + strconv.Itoa(cluster.Conf.SysbenchTables)
		scale := "--scale=" + strconv.Itoa(cluster.Conf.SysbenchScale)
		mode := "--oltp-test-mode=complex"
		var cmdprep *exec.Cmd
		cmdprep = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, mode, requests, threads, "prepare")

		if cluster.Conf.SysbenchV1 {
			test = cluster.Conf.SysbenchTest
			time = "--time=" + strconv.Itoa(cluster.Conf.SysbenchTime)
			tablesize = "--table-size=1000000"
			cmdprep = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "prepare")

			if cluster.Conf.SysbenchTest == "tpcc" {
				test = "./" + cluster.Conf.SysbenchTest + ".lua"
				// sysbench --mysql-user=root --mysql-password=mariadb   --mysql-db=sbtest --db-driver=mysql /usr/share/sysbench/tpcc.lua --threads=20 --tables=10 --scale=100 prepare
				cmdprep = exec.Command(cluster.Conf.SysbenchBinaryPath, test, scale, tables, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "prepare")
				cmdprep.Dir = cluster.Conf.ShareDir + "/submodule/sysbench-tpcc"  
			}
		}

		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Command: %s", strings.Replace(cmdprep.String(), cluster.GetDbPass(), "XXXX", -1))

		out, err := cmdprep.CombinedOutput()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s , %s", string(out), err)
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "%s", string(out))
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s %s", err.Error(), result)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "%s", result)
		}
	}
	return nil
}

func (cluster *Cluster) CleanupBench() error {
	proxies := cluster.GetProxies()
	if len(proxies) == 0 {
		return errors.New("No proxy")
	}

	prx := proxies[0]
	if cluster.benchmarkType == "sysbench" {
		test := "--test=oltp"
		if cluster.Conf.SysbenchV1 {
			test = cluster.Conf.SysbenchTest
		}
		var cleanup = cluster.Conf.SysbenchBinaryPath + test + " --db-driver=mysql --mysql-db=replication_manager_schema --mysql-user=" + cluster.GetRplUser() + " --mysql-password=" + cluster.GetRplPass() + " --mysql-host=" + prx.GetHost() + " --mysql-port=" + strconv.Itoa(prx.GetWritePort()) + " cleanup"
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "%s", strings.Replace(cleanup, cluster.GetRplPass(), "XXXXX", -1))
		var cmdcls *exec.Cmd
		cmdcls = exec.Command(cluster.Conf.SysbenchBinaryPath, test, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetRplUser(), "--mysql-password="+cluster.GetRplPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), "cleanup")
		if cluster.Conf.SysbenchTest == "tpcc" {
			tables := "--tables=" + strconv.Itoa(cluster.Conf.SysbenchTables)
			scale := "--scale=" + strconv.Itoa(cluster.Conf.SysbenchScale)
			test =  "./" + cluster.Conf.SysbenchTest + ".lua"
			cmdcls = exec.Command(cluster.Conf.SysbenchBinaryPath, test,tables,scale,  "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetRplUser(), "--mysql-password="+cluster.GetRplPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), "cleanup")
			cmdcls.Dir = cluster.Conf.ShareDir + "/submodule/sysbench-tpcc" 
		}
		var outcls bytes.Buffer
		cmdcls.Stdout = &outcls

		cmdclsErr := cmdcls.Run()
		if cmdclsErr != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", cmdclsErr)
			return cmdclsErr
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "%s", strings.Replace(outcls.String(), cluster.GetRplPass(), "XXXXX", -1))
	}
	if cluster.benchmarkType == "table" {

		err := dbhelper.BenchCleanup(cluster.GetMaster().Conn)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s", err.Error())
		}
	}
	return nil
}

func (cluster *Cluster) ChecksumBench() bool {
	if cluster.benchmarkType == "table" {
		if cluster.CheckTableConsistency("replication_manager_schema.bench") != true {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Inconsitant slave")
			return false
		}
	}
	if cluster.benchmarkType == "sysbench" {
		if cluster.CheckTableConsistency("test.sbtest") != true {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Inconsitant slave")
			return false
		}
	}
	return true
}

func (cluster *Cluster) RunSysBench(myTest string, myThreads string, mySize string, myTime string, myMode string) error {
	prx := cluster.GetProxies()[0]
	if prx == nil {
		return errors.New("No proxy")
	}

	test := "--test=" + myTest
	threads := "--num-threads=" + myThreads
	tablesize := "--oltp-table-size=" + mySize
	requests := "--max-requests=0"
	time := "--max-time=" + myTime
	mode := "--oltp-test-mode=" + myMode
	tables := "--tables=" + strconv.Itoa(cluster.Conf.SysbenchTables)
	scale := "--scale=" + strconv.Itoa(cluster.Conf.SysbenchScale)

	var cmdrun *exec.Cmd
	cmdrun = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, mode, requests, threads, "run")
	if cluster.Conf.SysbenchV1 {
		test = cluster.Conf.SysbenchTest
		tablesize = "--table-size=" + mySize
		threads = "--threads=" + myThreads
		time = "--time=" + myTime
		cmdrun = exec.Command(cluster.Conf.SysbenchBinaryPath, test, tablesize, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "run")
		if cluster.Conf.SysbenchTest == "tpcc" {
			test = "./" + cluster.Conf.SysbenchTest + ".lua"
			cmdrun = exec.Command(cluster.Conf.SysbenchBinaryPath, test, scale, tables, "--db-driver=mysql", "--mysql-db=replication_manager_schema", "--mysql-user="+cluster.GetDbUser(), "--mysql-password="+cluster.GetDbPass(), "--mysql-host="+prx.GetHost(), "--mysql-port="+strconv.Itoa(prx.GetWritePort()), time, threads, "--report-interval=1", "run")
			cmdrun.Dir = cluster.Conf.ShareDir + "/submodule/sysbench-tpcc" 
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Command: %s", strings.Replace(cmdrun.String(), cluster.GetDbPass(), "XXXX", -1))

	out, err := cmdrun.CombinedOutput()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s , %s", string(out), err)
		return err
	}
	analyzer := cluster.NewSysbenchAnalyzer(false)
	records,viols := analyzer.ParseToEnd(bytes.NewReader(out))
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "Analyse Parse %+v %+v", records,viols)
	
	cluster.ExtractSybenchTPCM(records)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "%s", string(out))
	return nil
}

func (cluster *Cluster) ExtractSybenchTPCM(records []SysbenchRecord) error {
	var tcpm SysBenchTpcResultPerMinute
	for _, record := range records {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "TPCM loop on parse result : %d", int32(record.TPS) )
		tcpm.Tpc = tcpm.Tpc + int32(record.TPS)
		tcpm.Threads = record.Threads
		tcpm.Errors = tcpm.Errors + int32(record.ErrorPerSec)
	}
	cluster.SysBenchTpcMResults = append(cluster.SysBenchTpcMResults, tcpm)
	return nil
}

func (cluster *Cluster) WriteSybenchTPCM() error {
	// If the file doesn't exist, create it, or append to the file
	f, err := os.OpenFile(cluster.WorkingDir+"/tpcm.log", os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}

	output := cluster.Name + " " +  cluster.Conf.ProvServicePlan
	header := "Plan" 
	for _, record := range cluster.SysBenchTpcMResults {
	    cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "sybecnh loop on save result : %d", int32(record.Tpc) )
		header = header + fmt.Sprintf(",%d", record.Threads)
		output = output + fmt.Sprintf(",%d", record.Tpc)
		
	}
	if _, err := f.Write([]byte(header + "\n" + output + "\n")); err != nil {
		f.Close() // ignore error; Write error takes precedence
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return nil
}

func (cluster *Cluster) RunBench() error {

	if cluster.benchmarkType == "sysbench" {
		return cluster.RunSysBench("oltp", strconv.Itoa(cluster.Conf.SysbenchThreads), "1000000", strconv.Itoa(cluster.Conf.SysbenchTime), "complex")
	}
	if cluster.benchmarkType == "table" {
		result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s %s", err.Error(), result)
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

func (cluster *Cluster) RunSysbenchTPCPerMinuteIncreaseThreads() error {
	cluster.CleanupBench()
	cluster.PrepareBench()
	threads := 1
	for threads <= 256 {
		cluster.RunSysBench("tpcc", strconv.Itoa(threads), "1000000", "60", "complex")
		threads = threads * 2
	}
	cluster.WriteSybenchTPCM()
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
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Slave  %s issue on replication  SQL Thread %s IO Thread %s ", s.URL, ss.SlaveSQLRunning.String, ss.SlaveIORunning.String)

			return false
		}
		if ss.MasterServerID != cluster.master.ServerID {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "TEST", "Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) CheckTableConsistency(table string) bool {
	checksum, err := dbhelper.ChecksumTable(cluster.master.Conn, table)

	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to take master checksum table ")
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum master table %s =  %s %s", table, checksum, cluster.master.URL)
	}
	var count int
	err = cluster.master.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could count record in bench table", err)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Number of rows master table %s = %d %s", table, count, cluster.master.URL)
	}
	var max int
	if cluster.benchmarkType == "table" {

		err = cluster.master.Conn.QueryRowx("select max(val) from " + table).Scan(&max)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could get max val in bench table", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Max Value in bench table %s = %d %s", table, max, cluster.master.URL)
		}
	}
	ctslave := 0
	for _, s := range cluster.slaves {
		ctslave++

		checksumslave, err := dbhelper.ChecksumTable(s.Conn, table)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Failed to take slave checksum table ")
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Checksum slave table %s = %s on %s ", table, checksumslave, s.URL)
		}
		err = s.Conn.QueryRowx("select count(*) from " + table).Scan(&count)
		if err != nil {
			log.Println("ERROR: Could not check long running writes", err)
		} else {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Number of rows slave table %s =  %d %s", table, count, s.URL)
		}
		var maxslave int
		if cluster.benchmarkType == "table" {
			err = s.Conn.QueryRowx("select max(val) from " + table).Scan(&maxslave)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Could get max val in bench table", err)
			} else {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Max Value in bench table %s = %d %s", table, maxslave, s.URL)
			}
		}
		if checksumslave != checksum && cluster.benchmarkType == "sysbench" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Checksum on slave is different from master")
			return false
		}
		if maxslave != max && cluster.benchmarkType == "table" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Max table value on slave is different from master")
			return false
		}
	}
	if ctslave == 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "No slaves while checking consistancy")
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

	// Default action from cluster configuration
	// If ProvDbStartFetchConfig is set to true, we want the server to fetch configuration on rolling restart
	// If ProvDbStartFetchConfig is set to false, we want the server to NOT fetch configuration on rolling restart
	if cluster.Conf.ProvDbStartFetchConfig && server.HasNoConfigFetchCookie() {
		server.DelNoConfigFetchCookie()
	} else if !cluster.Conf.ProvDbStartFetchConfig && !server.HasNoConfigFetchCookie() {
		server.SetNoConfigFetchCookie()
	}

	err := cluster.StartDatabaseService(server)
	wg2.Wait()
	return err
}

func (cluster *Cluster) DelayAllSlaves() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "Stopping slaves, injecting data & long transaction")
	for _, s := range cluster.slaves {
		_, err := s.StopSlaveSQLThread()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Stopping slave on %s %s", s.URL, err)
		}
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 1000)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s %s", err.Error(), result)
	}
	err = dbhelper.InjectLongTrx(cluster.master.Conn, 12)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "InjectLongTrx %s", err.Error())
	}
	result, err = dbhelper.WriteConcurrent2(cluster.master.DSN, 1000)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "%s %s", err.Error(), result)
	}
	for _, s := range cluster.slaves {
		_, err := s.StartSlave()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Staring slave on %s %s", s.URL, err)
		}
	}
	time.Sleep(5 * time.Second)
	return nil
}

func (cluster *Cluster) InitBenchTable() error {

	result, err := dbhelper.WriteConcurrent2(cluster.GetMaster().DSN, 10)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Insert some events %s %s", err.Error(), result)
		return err
	}
	return nil
}

func (cluster *Cluster) InitTestCluster(conf string, test *Test) bool {
	test.ConfigInit = *cluster.Conf
	savedConf = *cluster.Conf
	savedFailoverCtr = cluster.FailoverCtr
	savedFailoverTs = cluster.FailoverTs
	cluster.CleanAll = true
	if !cluster.IsProvision {
		err := cluster.Bootstrap()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlErr, "Abording test, bootstrap failed, %s", err)
			cluster.Unprovision()
			return false
		}
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Starting Test %s", test.Name)
	return true
}

func (cluster *Cluster) CloseTestCluster(conf string, test *Test) bool {
	test.ConfigTest = *cluster.Conf
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
	*cluster.Conf = savedConf
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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "Stopping replication")
	for _, s := range cluster.slaves {
		_, err := s.StopSlave()
		if err != nil {
			return err
		}
	}
	return nil
}

func (cluster *Cluster) StartSlaves() error {
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, "BENCH", "Sarting replication")
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
