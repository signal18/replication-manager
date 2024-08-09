package cluster

import (
	"bytes"
	"errors"
	"os"

	"github.com/signal18/replication-manager/config"
)

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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionProxySQLService(prx *ProxySQLProxy) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopProxySQLService(server DatabaseProxy) error {
	var strOut string
	var err error
	server.SetWaitStopCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	if cluster.Conf.OnPremiseSSHStopProxyScript == "" {
		out, err := client.Cmd("systemctl stop proxysql").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var r, stdout, stderr bytes.Buffer

		srcpath := cluster.Conf.OnPremiseSSHStopProxyScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return err2

		}
		defer filerc.Close()
		r.ReadFrom(filerc)

		if err = client.Shell().SetStdio(&r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise stop ProxySQL  : %s", strOut)
	return nil
}

func (cluster *Cluster) OnPremiseStartProxySQLService(server DatabaseProxy) error {
	var strOut string
	var err error
	server.SetWaitStartCookie()

	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()

	if cluster.Conf.OnPremiseSSHStartProxyScript == "" {
		out, err := client.Cmd("systemctl start proxysql").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var r, stdout, stderr bytes.Buffer

		srcpath := cluster.Conf.OnPremiseSSHStartProxyScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return errors.New("Cancel dbjob can't open script")

		}
		defer filerc.Close()
		r.ReadFrom(filerc)

		if err = client.Shell().SetStdio(&r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise start ProxySQL  : %s", strOut)
	return nil
}
