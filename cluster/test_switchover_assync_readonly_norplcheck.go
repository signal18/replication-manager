package cluster

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
	switchoverChan <- true
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
