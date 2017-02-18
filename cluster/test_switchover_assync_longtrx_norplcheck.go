package cluster

import "time"

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

		switchoverChan <- true

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
