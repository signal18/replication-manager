// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import "github.com/tanji/replication-manager/dbhelper"
import "strconv"

//import "github.com/tanji/replication-manager/misc"
import "time"
import "sort"
import "bytes"
import "os/exec"

//import "encoding/json"
//import "net/http"
var tests = []string{
	"testSwitchOverLongTransactionNoRplCheckNoSemiSync",
	"testSwitchOverLongQueryNoRplCheckNoSemiSync",
	"testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync",
	"testSlaReplAllDelay",
	"testFailoverReplAllDelayInteractive",
	"testFailoverReplAllDelayAutoRejoinFlashback",
	"testSwitchoverReplAllDelay",
	"testSlaReplAllSlavesStopNoSemiSync",
	"testSlaReplOneSlavesStop",
	"testSwitchOverReadOnlyNoRplCheck",
	"testSwitchOverNoReadOnlyNoRplCheck",
	"testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck",
	"testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck",
	"testSwitchOverBackPreferedMasterNoRplCheckSemiSync",
	"testSwitchOverAllSlavesStopRplCheckNoSemiSync",
	"testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck",
	"testSwitchOverAllSlavesDelayRplCheckNoSemiSync",
	"testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync",
	"testSwitchOverAllSlavesDelayRplChecksNoSemiSync",
	"testFailOverAllSlavesDelayNoRplChecksNoSemiSync",
	"testFailOverAllSlavesDelayRplChecksNoSemiSync",
	"testFailOverNoRplChecksNoSemiSync",
	"testNumberFailOverLimitReach",
	"testFailOverTimeNotReach",
}

const recover_time = 8

func (cluster *Cluster) GetTests() []string {
	return tests
}

func (cluster *Cluster) testSwitchOverLongTransactionNoRplCheckNoSemiSync() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverLongTransactionNoRplCheckNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}

	SaveMasterURL := cluster.master.URL
	masterTest, _ := cluster.newServerMonitor(cluster.master.URL)
	defer masterTest.Conn.Close()
	go masterTest.Conn.Exec("start transaction")
	time.Sleep(12 * time.Second)
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}

	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverLongQueryNoRplCheckNoSemiSync() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverLongQueryNoRplCheckNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}

	SaveMasterURL := cluster.master.URL
	go dbhelper.InjectLongTrx(cluster.master.Conn, 20)
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}

	time.Sleep(20 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverLongTransactionWithoutCommitNoRplCheckNoSemiSync() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverLongTransactionNoRplCheckNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}

	SaveMasterURL := cluster.master.URL
	go dbhelper.InjectLongTrx(cluster.master.Conn, 20)
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
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

func (cluster *Cluster) testSlaReplAllSlavesStopNoSemiSync() bool {
	cluster.LogPrintf("TESTING : Starting Test %s", "testSlaReplAllSlavesStopNoSemySync")
	cluster.conf.MaxDelay = 0
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}

	cluster.sme.ResetUpTime()
	time.Sleep(3 * time.Second)
	sla1 := cluster.sme.GetUptimeFailable()
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(recover_time * time.Second)
	sla2 := cluster.sme.GetUptimeFailable()
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	if sla2 == sla1 {
		return false
	} else {
		return true
	}
}

func (cluster *Cluster) testSlaReplOneSlavesStop() bool {
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	return false
}

func (cluster *Cluster) testSwitchOverReadOnlyNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverReadOnlyNoRplCheck")
	cluster.LogPrintf("INFO : Master is %s", cluster.master.URL)
	cluster.conf.ReadOnly = true
	for _, s := range cluster.slaves {
		_, err := s.Conn.Exec("set global read_only=1")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	swChan <- true
	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master is %s ", cluster.master.URL)
	for _, s := range cluster.slaves {
		cluster.LogPrintf("INFO : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly == "OFF" {
			return false
		}
	}
	return true
}

func (cluster *Cluster) testSwitchOverNoReadOnlyNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverNoReadOnlyNoRplCheck")
	cluster.LogPrintf("INFO : Master is %s", cluster.master.URL)
	cluster.conf.ReadOnly = false
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global read_only=0")
		if err != nil {
			cluster.LogPrintf("ERROR : %s", err.Error())
		}
	}
	SaveMasterURL := cluster.master.URL
	swChan <- true
	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master is %s ", cluster.master.URL)
	if SaveMasterURL == cluster.master.URL {
		cluster.LogPrintf("INFO : same server URL after switchover")
		return false
	}
	for _, s := range cluster.slaves {
		cluster.LogPrintf("INFO : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly != "OFF" {
			return false
		}
	}
	return true
}

func (cluster *Cluster) testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOver2TimesReplicationOk")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
		if err != nil {
			cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
		}
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
		SaveMasterURL := cluster.master.URL
		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

		if SaveMasterURL == cluster.master.URL {
			cluster.LogPrintf("INFO : same server URL after switchover")
			return false
		}
	}

	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("INFO : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("INFO :  Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOver2TimesReplicationOkSemisync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
		if err != nil {
			cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
		}
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
		SaveMasterURL := cluster.master.URL
		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

		if SaveMasterURL == cluster.master.URL {
			cluster.LogPrintf("INFO : same server URL after switchover")
			return false
		}
	}

	for _, s := range cluster.slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			cluster.LogPrintf("INFO : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
		if s.MasterServerID != cluster.master.ServerID {
			cluster.LogPrintf("INFO :  Replication is  pointing to wrong master %s ", cluster.master.ServerID)
			return false
		}
	}
	return true
}

func (cluster *Cluster) testSwitchOverBackPreferedMasterNoRplCheckSemiSync() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverBackPreferedMasterNoRplCheckSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	cluster.conf.PrefMaster = cluster.master.URL
	cluster.LogPrintf("TESTING : Set cluster.conf.PrefMaster %s", "cluster.conf.PrefMaster")
	time.Sleep(2 * time.Second)
	SaveMasterURL := cluster.master.URL
	for i := 0; i < 2; i++ {

		cluster.LogPrintf("INFO : New Master  %s Failover counter %d", cluster.master.URL, i)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverAllSlavesStopRplCheckNoSemiSync() bool {
	cluster.conf.MaxDelay = 0
	cluster.conf.RplChecks = true
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesStopRplCheckNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(5 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true
		cluster.waitFailoverEnd()

		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 0
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesStopNoRplCheck")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO : Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverAllSlavesDelayRplCheckNoSemiSync() bool {
	cluster.conf.RplChecks = true
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesDelayRplCheckNoSemySync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(15 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync() bool {
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(15 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		cluster.waitFailoverEnd()
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testSwitchOverAllSlavesDelayRplChecksNoSemiSync() bool {
	cluster.conf.RplChecks = true
	cluster.conf.MaxDelay = 8
	cluster.LogPrintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(10 * time.Second)

	SaveMasterURL := cluster.master.URL
	for i := 0; i < 1; i++ {

		cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	}
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}
	return true
}

func (cluster *Cluster) testFailOverAllSlavesDelayNoRplChecksNoSemiSync() bool {

	cluster.Bootstrap()
	time.Sleep(5 * time.Second)

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverAllSlavesDelayNoRplChecksNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	SaveMasterURL := cluster.master.URL
	cluster.LogPrintf("BENCH: Stopping replication")
	for _, s := range cluster.slaves {
		dbhelper.StopSlave(s.Conn)
	}
	result, err := dbhelper.WriteConcurrent2(cluster.master.DSN, 10)
	if err != nil {
		cluster.LogPrintf("BENCH : %s %s", err.Error(), result)
	}
	cluster.LogPrintf("BENCH : Write Concurrent Insert")

	dbhelper.InjectLongTrx(cluster.master.Conn, 10)
	cluster.LogPrintf("BENCH : Inject Long Trx")
	time.Sleep(10 * time.Second)
	cluster.LogPrintf("BENCH : Sarting replication")
	for _, s := range cluster.slaves {
		dbhelper.StartSlave(s.Conn)
	}

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.master.State = stateFailed
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 4
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.master.URL)

		return false
	}

	return true
}

func (cluster *Cluster) testFailOverAllSlavesDelayRplChecksNoSemiSync() bool {

	cluster.Bootstrap()
	cluster.waitFailoverEnd()

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverAllSlavesDelayRplChecksNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	SaveMasterURL := cluster.master.URL

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
	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)

	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = true
	cluster.conf.MaxDelay = 4
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)

	time.Sleep(2 * time.Second)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  New master %s  ", SaveMasterURL, cluster.master.URL)

		return false
	}
	cluster.Bootstrap()
	cluster.waitFailoverEnd()
	return true
}

func (cluster *Cluster) testFailOverNoRplChecksNoSemiSync() bool {
	cluster.conf.MaxDelay = 0
	cluster.Bootstrap()
	cluster.waitFailoverEnd()

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverNoRplChecksNoSemiSync")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.conf.FailLimit = 5
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 0
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 4
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)

		return false
	}

	return true
}

func (cluster *Cluster) testNumberFailOverLimitReach() bool {
	cluster.conf.MaxDelay = 0
	cluster.Bootstrap()
	cluster.waitFailoverEnd()

	cluster.LogPrintf("TESTING : Starting Test %s", "testNumberFailOverLimitReach")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.conf.FailLimit = 3
	cluster.conf.FailTime = 0
	cluster.failoverCtr = 3
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 20
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)

		return false
	}

	return true
}

func (cluster *Cluster) testFailOverTimeNotReach() bool {
	cluster.conf.MaxDelay = 0
	cluster.Bootstrap()
	cluster.waitFailoverEnd()

	cluster.LogPrintf("TESTING : Starting Test %s", "testFailOverTimeNotReach")
	for _, s := range cluster.servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			cluster.LogPrintf("TESTING : %s", err)
		}
	}
	SaveMasterURL := cluster.master.URL

	cluster.LogPrintf("INFO :  Master is %s", cluster.master.URL)
	cluster.master.State = stateFailed
	cluster.conf.Interactive = false
	cluster.master.FailCount = cluster.conf.MaxFail
	cluster.failoverTs = time.Now().Unix()
	cluster.conf.FailLimit = 3
	cluster.conf.FailTime = 20
	cluster.failoverCtr = 1
	cluster.conf.RplChecks = false
	cluster.conf.MaxDelay = 20
	cluster.checkfailed()

	cluster.waitFailoverEnd()
	cluster.LogPrintf("INFO : New Master  %s ", cluster.master.URL)
	if cluster.master.URL != SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)

		return false
	}

	return true
}

func (cluster *Cluster) getTestResultLabel(res bool) string {
	if res == false {
		return "FAILED"
	} else {
		return "PASS"
	}
}

func (cluster *Cluster) RunAllTests(test string) bool {
	var allTests = map[string]string{}

	ret := true
	var res bool
	cluster.LogPrintf("TESTING : %s", test)
	if test == "testFailoverReplAllDelayAutoRejoinFlashback" || test == "ALL" {
		res = cluster.testFailoverReplAllDelayAutoRejoinFlashback()
		allTests["1 Failover all slaves delay rejoin flashback<cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	cluster.CleanAll = true
	cluster.Bootstrap()
	cluster.waitFailoverEnd()
	if test == "testSwitchOverLongTransactionNoRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverLongTransactionNoRplCheckNoSemiSync()
		allTests["1 Switchover Concurrent Long Transaction <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}

	if test == "testSwitchOverLongQueryNoRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverLongQueryNoRplCheckNoSemiSync()
		allTests["1 Switchover Concurrent Long Query <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverNoReadOnlyNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverNoReadOnlyNoRplCheck()
		allTests["1 Switchover <cluster.conf.ReadOnly=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverReadOnlyNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverReadOnlyNoRplCheck()
		allTests["1 Switchover <cluster.conf.ReadOnly=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOver2TimesReplicationOkNoSemiSyncNoRplCheck()
		allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOver2TimesReplicationOkSemiSyncNoRplCheck()
		allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverBackPreferedMasterNoRplCheckSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverBackPreferedMasterNoRplCheckSemiSync()
		allTests["2 Switchover Back Prefered Master <semisync=true> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesStopRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesStopRplCheckNoSemiSync()
		allTests["Can't Switchover All Slaves Stop  <semisync=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesStopNoSemiSyncNoRplCheck()
		allTests["Can Switchover All Slaves Stop <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesDelayRplCheckNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesDelayRplCheckNoSemiSync()
		allTests["Can't Switchover All Slaves Delay <semisync=false> <cluster.conf.RplChecks=true>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testSwitchOverAllSlavesDelayNoRplChecksNoSemiSync()
		allTests["Can Switchover All Slaves Delay <semisync=false> <cluster.conf.RplChecks=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testSlaReplAllSlavesStopNoSemiSync" || test == "ALL" {
		res = cluster.testSlaReplAllSlavesStopNoSemiSync()
		allTests["SLA Decrease Can't Switchover All Slaves Stop <Semisync=false>"] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverNoRplChecksNoSemiSync()
		allTests["1 Failover <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverAllSlavesDelayNoRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverAllSlavesDelayNoRplChecksNoSemiSync()
		allTests["1 Failover All Slave Delay <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverAllSlavesDelayRplChecksNoSemiSync" || test == "ALL" {
		res = cluster.testFailOverAllSlavesDelayRplChecksNoSemiSync()
		allTests["1 Failover All Slave Delay <cluster.conf.RplChecks=true> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testNumberFailOverLimitReach" || test == "ALL" {
		res = cluster.testNumberFailOverLimitReach()
		allTests["1 Failover Number of Failover Reach <cluster.conf.RplChecks=false> <Semisync=false> "] = cluster.getTestResultLabel(res)
		if res == false {
			ret = res
		}
	}
	if test == "testFailOverTimeNotReach" || test == "ALL" {
		res = cluster.testFailOverTimeNotReach()
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

func (cluster *Cluster) waitFailoverEnd() {
	for cluster.sme.IsInFailover() {
		time.Sleep(time.Second)
	}
	time.Sleep(recover_time * time.Second)
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

func (cluster *Cluster) DelayAllSlaves() error {
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

func (cluster *Cluster) testFailoverReplAllDelayAutoRejoinFlashback() bool {
	cluster.SetFailSync(false)
	cluster.SetInteractive(false)
	cluster.SetRplChecks(false)
	cluster.SetRejoin(true)
	cluster.SetRejoinFlashback(true)
	cluster.SetRejoinDump(false)
	cluster.CleanAll = true
	cluster.InitClusterSemiSync()

	err := cluster.Bootstrap()
	if err != nil {
		cluster.LogPrintf("INFO : Abording test, bootstrap failed")
	}
	cluster.waitFailoverEnd()
	if cluster.master == nil {
		cluster.LogPrintf("INFO : Abording test, no master found")
		return false
	}

	SaveMasterURL := cluster.master.URL
	SaveMaster := cluster.master
	cluster.DelayAllSlaves()
	cluster.killMariaDB(cluster.master)
	cluster.waitFailoverEnd()
	if cluster.master.URL == SaveMasterURL {
		cluster.LogPrintf("INFO : Old master %s ==  Next master %s  ", SaveMasterURL, cluster.master.URL)
		return false
	}

	cluster.startMariaDB(SaveMaster)

	cluster.ShutdownClusterSemiSync()

	return true
}
