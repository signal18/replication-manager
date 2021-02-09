package cluster

import (
	"errors"
	"os"

	"github.com/helloyi/go-sshclient"
	sshcli "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseConnect(server *ServerMonitor) (*sshclient.Client, error) {
	if server.ClusterGroup.IsInFailover() {
		return nil, errors.New("OnPremise Provisioning cancel during connect")
	}
	key := os.Getenv("HOME") + "/.ssh/id_rsa"
	client, err := sshcli.DialWithKey(misc.Unbracket(server.Host)+":22", "root", key)
	if err != nil {
		return nil, errors.New("OnPremise Provisioning via SSH %s" + err.Error())
	}
	return client, nil
}

func (cluster *Cluster) OnPremiseProvisionBootsrap(server *ServerMonitor, client *sshclient.Client) error {
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	out, err := client.Cmd("export MYSQL_ROOT_PASSWORD=" + server.Pass).Cmd("export REPLICATION_MANAGER_URL=" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort).Cmd("export REPLICATION_MANAGER_USER=" + adminuser).Cmd("export REPLICATION_MANAGER_PASSWORD=" + adminpassword).Cmd("export REPLICATION_MANAGER_HOST_NAME=" + server.Host).Cmd("export REPLICATION_MANAGER_HOST_PORT=" + server.Port).Cmd("export REPLICATION_MANAGER_CLUSTER_NAME=" + server.ClusterGroup.Name).SmartOutput()
	if err != nil {
		return errors.New("OnPremise Bootsrap via SSH %s" + err.Error())
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	out, err = client.Cmd("wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/opensvc/bootstrap | sh").SmartOutput()
	if err != nil {
		return errors.New("OnPremise Bootsrap via SSH %s" + err.Error())
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Bootsrap  : %s", string(out))
	return nil
}

func (cluster *Cluster) OnPremiseProvisionDatabaseService(server *ServerMonitor) {
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		cluster.errorChan <- err
	}
	defer client.Close()
	err = cluster.OnPremiseProvisionBootsrap(server, client)
	if err != nil {
		cluster.errorChan <- err
	}
	out, err := client.Cmd("rm -rf /etc/mysql").Cmd("cp -rp /bootstrap/etc/mysql /etc").Cmd("cp -rp /bootstrap/data /").Cmd("/bootstrap/init/start").SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
}

func (cluster *Cluster) OnPremiseSUnprovisionDatabaseService(server *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopDatabaseService(server *ServerMonitor) {
	//s.JobServerStop() need an agent or ssh to trigger this
	server.Shutdown()
}

func (cluster *Cluster) OnPremiseStartDatabaseService(server *ServerMonitor) {

	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		cluster.errorChan <- err
	}
	defer client.Close()
	out, err := client.Cmd("systemctl stop mysql").SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
}
