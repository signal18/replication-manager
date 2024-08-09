package cluster

import (
	"bytes"
	"io"
	"os"
	"strings"

	"github.com/signal18/replication-manager/config"
)

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
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionHaProxyService(prx *HaproxyProxy) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopHaproxyService(server DatabaseProxy) error {
	var strOut string
	var err error
	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	if cluster.Conf.OnPremiseSSHStopProxyScript == "" {
		out, err := client.Cmd("systemctl stop haproxy").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var stdout, stderr bytes.Buffer

		srcpath := cluster.Conf.OnPremiseSSHStopProxyScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return err2
		}
		defer filerc.Close()

		envBuf := strings.NewReader(server.GetSshEnv())
		r := io.MultiReader(envBuf, filerc)

		if err = client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Stop Haproxy  : %s", strOut)
	return nil
}

func (cluster *Cluster) OnPremiseStartHaProxyService(server DatabaseProxy) error {
	var strOut string
	var err error
	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnectProxy(server)
	if err != nil {
		return err
	}
	defer client.Close()
	if cluster.Conf.OnPremiseSSHStartProxyScript == "" {
		out, err := client.Cmd("systemctl start haproxy").SmartOutput()
		if err != nil {
			return err
		}
		strOut = string(out)
	} else {
		var stdout, stderr bytes.Buffer

		srcpath := cluster.Conf.OnPremiseSSHStartProxyScript
		filerc, err2 := os.Open(srcpath)
		if err2 != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModProxy, config.LvlErr, "Failed to load start script %s for SSH, err : %s", srcpath, err2.Error())
			return err2
		}
		defer filerc.Close()

		envBuf := strings.NewReader(server.GetSshEnv())
		r := io.MultiReader(envBuf, filerc)

		if err = client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
			return err
		}
		strOut = stdout.String()
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise start HaProxy  : %s", strOut)
	return nil
}
