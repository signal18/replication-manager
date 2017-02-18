package cluster

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
	switchoverChan <- true
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
