package cluster

func (cluster *Cluster) OnPremiseProvisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil
}

func (cluster *Cluster) OnPremiseSUnprovisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopDatabaseService(s *ServerMonitor) {
	//s.JobServerStop() need an agent or ssh to trigger this
	s.Shutdown()
}

func (cluster *Cluster) OnPremiseStartDatabaseService(s *ServerMonitor) {
	s.SetWaitStartCookie()
}
