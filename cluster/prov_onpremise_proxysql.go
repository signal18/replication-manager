package cluster

import "github.com/signal18/replication-manager/config"

func (cluster *Cluster) OnPremiseProvisionProxySQLService(prx *ProxySQLProxy) error {
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
	out, err := client.Cmd("rm -f /etc/proxysql.cnf").Cmd("cp -rp /bootstrap/etc/proxysql.cnf /etc").Cmd("proxysql –initial ").SmartOutput()
	if err != nil {
		cluster.errorChan <- err
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionProxySQLService(prx *ProxySQLProxy) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopProxySQLService(server DatabaseProxy) error {
	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	out, err := client.Cmd("systemctl stop proxysql").SmartOutput()
	if err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "OnPremise stop ProxySQL  : %s", string(out))
	return nil
}

func (cluster *Cluster) OnPremiseStartProxySQLService(server DatabaseProxy) error {

	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	out, err := client.Cmd("systemctl start proxysql").SmartOutput()
	if err != nil {
		return err
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, LvlInfo, "OnPremise start ProxySQL  : %s", string(out))
	return nil
}
