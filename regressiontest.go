// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import "github.com/tanji/replication-manager/dbhelper"

import "time"

//import "encoding/json"
//import "net/http"

const recover_time = 8

func testSlaReplAllDelay() bool {
	return false
}

func testFailoverReplAllDelayInteractive() bool {
	return false
}

func testFailoverReplAllDelayAuto() bool {
	return false
}

func testSwitchoverReplAllDelay() bool {
	return false
}

func testSlaReplAllSlavesStopNoSemiSync() bool {
	logprintf("TESTING : Starting Test %s", "testSlaReplAllSlavesStopNoSemySync")

	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}

	sme.ResetUpTime()
	time.Sleep(3 * time.Second)
	sla1 := sme.GetUptimeFailable()
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(recover_time * time.Second)
	sla2 := sme.GetUptimeFailable()
	for _, s := range slaves {
		dbhelper.StartSlave(s.Conn)
	}
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	if sla2 == sla1 {
		return false
	} else {
		return true
	}

}

func testSlaReplOneSlavesStop() bool {
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	return false
}

func testSwitchOverReadOnly() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOverReadOnly")
	logprintf("INFO : Master is %s", master.URL)
	readonly = true
	for _, s := range slaves {
		_, err := s.Conn.Exec("set global read_only=1")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	swChan <- true
	time.Sleep(recover_time * time.Second)
	logprintf("INFO : New Master is %s ", master.URL)
	for _, s := range slaves {
		logprintf("INFO : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly == "OFF" {
			return false
		}
	}
	return true
}

func testSwitchOverNoReadOnly() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOverNoReadOnly")
	logprintf("INFO : Master is %s", master.URL)
	readonly = false
	for _, s := range servers {
		_, err := s.Conn.Exec("set global read_only=0")
		if err != nil {
			logprintf("ERROR : %s", err.Error())
		}
	}
	SaveMasterURL := master.URL
	swChan <- true
	time.Sleep(recover_time * time.Second)
	logprintf("INFO : New Master is %s ", master.URL)
	if SaveMasterURL == master.URL {
		logprintf("INFO : same server URL after switchover")
		return false
	}
	for _, s := range slaves {
		logprintf("INFO : Server  %s is %s", s.URL, s.ReadOnly)
		if s.ReadOnly != "OFF" {
			return false
		}
	}
	return true
}
func testSwitchOver2TimesReplicationOk() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOver2TimesReplicationOk")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(master.DSN, 10000)
		if err != nil {
			logprintf("BENCH : %s %s", err.Error(), result)
		}
		logprintf("INFO : New Master  %s ", master.URL)
		SaveMasterURL := master.URL
		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

		if SaveMasterURL == master.URL {
			logprintf("INFO : same server URL after switchover")
			return false
		}
	}

	for _, s := range slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			logprintf("INFO : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
		if s.MasterServerID != master.ServerID {
			logprintf("INFO :  Replication is  pointing to wrong master %s ", master.ServerID)
			return false
		}
	}
	return true
}

func testSwitchOver2TimesReplicationOkSemisync() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOver2TimesReplicationOkSemisync")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	time.Sleep(2 * time.Second)

	for i := 0; i < 2; i++ {
		result, err := dbhelper.WriteConcurrent2(master.DSN, 10000)
		if err != nil {
			logprintf("BENCH : %s %s", err.Error(), result)
		}
		logprintf("INFO : New Master  %s ", master.URL)
		SaveMasterURL := master.URL
		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

		if SaveMasterURL == master.URL {
			logprintf("INFO : same server URL after switchover")
			return false
		}
	}

	for _, s := range slaves {
		if s.IOThread != "Yes" || s.SQLThread != "Yes" {
			logprintf("INFO : Slave  %s issue on replication  SQL Thread % IO %s ", s.URL, s.SQLThread, s.IOThread)
			return false
		}
		if s.MasterServerID != master.ServerID {
			logprintf("INFO :  Replication is  pointing to wrong master %s ", master.ServerID)
			return false
		}
	}
	return true
}

func testSwitchOverBackPreferedMaster() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOverBackPreferedMaster")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='ON'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	prefMaster = master.URL
	logprintf("TESTING : Set prefMaster %s", "prefMaster")
	time.Sleep(2 * time.Second)
	SaveMasterURL := master.URL
	for i := 0; i < 2; i++ {

		logprintf("INFO : New Master  %s Failover counter %d", master.URL, i)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

	}
	if master.URL != SaveMasterURL {
		logprintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, master.URL)
		return false
	}
	return true
}

func testSwitchOverAllSlavesStop() bool {
	rplchecks = true
	logprintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesStop")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)

	SaveMasterURL := master.URL
	for i := 0; i < 1; i++ {

		logprintf("INFO :  Master  is %d", master.URL)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

	}
	for _, s := range slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if master.URL != SaveMasterURL {
		logprintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, master.URL)
		return false
	}
	return true
}

func testSwitchOverAllSlavesStopNoChecks() bool {
	rplchecks = false
	logprintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesStopNoChecks")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)

	SaveMasterURL := master.URL
	for i := 0; i < 1; i++ {

		logprintf("INFO :  Master  is %d", master.URL)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

	}
	for _, s := range slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if master.URL == SaveMasterURL {
		logprintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, master.URL)
		return false
	}
	return true
}

func testSwitchOverAllSlavesDelay() bool {
	rplchecks = true
	maxDelay = 8
	logprintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesDelay")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(15 * time.Second)

	SaveMasterURL := master.URL
	for i := 0; i < 1; i++ {

		logprintf("INFO :  Master  is %d", master.URL)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

	}
	for _, s := range slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if master.URL != SaveMasterURL {
		logprintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, master.URL)
		return false
	}
	return true
}

func testSwitchOverAllSlavesDelayNoChecks() bool {
	rplchecks = false
	maxDelay = 8
	logprintf("TESTING : Starting Test %s", "testSwitchOverAllSlavesDelayNoChecks")
	for _, s := range servers {
		_, err := s.Conn.Exec("set global rpl_semi_sync_master_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
		_, err = s.Conn.Exec("set global rpl_semi_sync_slave_enabled='OFF'")
		if err != nil {
			logprintf("TESTING : %s", err)
		}
	}
	for _, s := range slaves {
		dbhelper.StopSlave(s.Conn)
	}
	time.Sleep(15 * time.Second)

	SaveMasterURL := master.URL
	for i := 0; i < 1; i++ {

		logprintf("INFO :  Master  is %d", master.URL)

		swChan <- true

		time.Sleep(recover_time * time.Second)
		logprintf("INFO : New Master  %s ", master.URL)

	}
	for _, s := range slaves {
		dbhelper.StartSlave(s.Conn)
	}
	time.Sleep(2 * time.Second)
	if master.URL == SaveMasterURL {
		logprintf("INFO : Saved Prefered master %s <>  from saved %s  ", SaveMasterURL, master.URL)
		return false
	}
	return true
}

func getTestResultLabel(res bool) string {
	if res == false {
		return "FAILED"
	} else {
		return "PASS"
	}
}

func runAllTests() bool {

	var allTests = map[string]string{}
	cleanall = true
	ret := true
	var res bool

	res = testSwitchOverNoReadOnly()
	allTests["1 Switchover <readonly=false> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverReadOnly()
	allTests["1 Switchover <readonly=true> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOver2TimesReplicationOk()
	allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=false> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOver2TimesReplicationOkSemisync()
	allTests["2 Switchover Replication Ok <2 threads benchmark> <semisync=true> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverBackPreferedMaster()
	allTests["2 Switchover Back Prefered Master <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverAllSlavesStop()
	allTests["Can't Switchover All Slaves Stop  <semisync=false> <rplchecks=true>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverAllSlavesDelay()
	allTests["Can't Switchover All Slaves Delay <semisync=false> <rplchecks=true>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverAllSlavesDelayNoChecks()
	allTests["Can Switchover All Slaves Delay <semisync=false> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSwitchOverAllSlavesStopNoChecks()
	allTests["Can Switchover All Slaves Stop <semisync=false> <rplchecks=false>"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	res = testSlaReplAllSlavesStopNoSemiSync()
	allTests["SLA Decrease Can't Failover All Slaves Stop (Semisync=false)"] = getTestResultLabel(res)
	if res == false {
		ret = res
	}

	//bootstrap()

	for k, v := range allTests {
		logprintf("TESTS : Result  %s -> %s", k, v)
	}

	cleanall = false
	return ret
}
