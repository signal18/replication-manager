package cluster

func (cluster *Cluster) SlapOSProvisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil
}

func (cluster *Cluster) SlapOSUnprovisionDatabaseService(s *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) SlapOSStopDatabaseService(s *ServerMonitor) error {
	s.Shutdown()
	return nil
}

func (cluster *Cluster) SlapOSStartDatabaseService(s *ServerMonitor) error {
	//	s.JobServerRestart()
	s.SetWaitStartCookie()
	return nil
}
