package cluster

func (cluster *Cluster) OnPremiseProvisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil
}

func (cluster *Cluster) OnPremiseSUnprovisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopDatabaseService(s *ServerMonitor) {
	s.JobServerStop()
}

func (cluster *Cluster) OnPremiseStartDatabaseService(s *ServerMonitor) {
	s.JobServerStart()
}
