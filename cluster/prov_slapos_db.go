package cluster

func (cluster *Cluster) SlapOSProvisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil
}

func (cluster *Cluster) SlapOSUnprovisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) SlapOSStopDatabaseService(s *ServerMonitor) {
	s.JobServerStop()
}

func (cluster *Cluster) SlapOSStartDatabaseService(s *ServerMonitor) {
	s.JobServerStart()
}
