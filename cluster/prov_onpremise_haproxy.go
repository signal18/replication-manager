package cluster

func (cluster *Cluster) OnPremiseProvisionHaProxyService(prx *HaproxyProxy) error {
	client, err := cluster.OnPremiseConnectProxy(prx)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	defer client.Close()
	err = cluster.OnPremiseProvisionBootsrapProxy(prx, client)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	out, err := client.Cmd("rm -f /etc/haproxy/haproxy.cfg").Cmd("cp -rp /bootstrap/etc/haproxy.cfg /etc/haproxy/").Cmd("systemctl start haproxy ").SmartOutput()
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	cluster.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionHaProxyService(prx *HaproxyProxy) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopHaproxyService(server DatabaseProxy) error {
	server.SetWaitStopCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	out, err := client.Cmd("systemctl stop haproxy").SmartOutput()
	if err != nil {
		return err
	}
	cluster.LogPrintf(LvlInfo, "OnPremise Stop Haproxy  : %s", string(out))
	return nil
}

func (cluster *Cluster) OnPremiseStartHaProxyService(server DatabaseProxy) error {

	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	err = cluster.OnPremiseProvisionBootsrapProxy(server, client)
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	out, err := client.Cmd("systemctl start haproxy").SmartOutput()
	if err != nil {
		return err
	}
	cluster.LogPrintf(LvlInfo, "OnPremise start HaProxy  : %s", string(out))
	return nil
}
