package cluster

import (
	"errors"
	"os"
	"strconv"

	"github.com/helloyi/go-sshclient"
	sshcli "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseConnect(server *ServerMonitor) (*sshclient.Client, error) {
	if cluster.IsInFailover() {
		return nil, errors.New("OnPremise provisioning cancel during failover")
	}
	if cluster.Conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}
	user, _ := misc.SplitPair(cluster.Conf.OnPremiseSSHCredential)

	key := os.Getenv("HOME") + "/.ssh/id_rsa"
	client, err := sshcli.DialWithKey(misc.Unbracket(server.Host)+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, key)
	if err != nil {
		return nil, errors.New("OnPremise Provisioning via SSH %s" + err.Error())
	}
	return client, nil
}

func (cluster *Cluster) OnPremiseProvisionDatabaseService(server *ServerMonitor) {
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		cluster.errorChan <- err
	}
	defer client.Close()
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	out, err := client.Cmd("export MYSQL_ROOT_PASSWORD=" + server.Pass).Cmd("export REPLICATION_MANAGER_URL=" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort).Cmd("export REPLICATION_MANAGER_USER=" + adminuser).Cmd("export REPLICATION_MANAGER_PASSWORD=" + adminpassword).Cmd("export REPLICATION_MANAGER_HOST_NAME=" + server.Host).Cmd("export REPLICATION_MANAGER_HOST_PORT=" + server.Port).Cmd("export REPLICATION_MANAGER_CLUSTER_NAME=" + server.ClusterGroup.Name).SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	dbtype := "mariadb"
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cmd := "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/debian/" + dbtype + "/bootstrap | sh"
	if cluster.Configurator.HaveDBTag("rpm") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/redhat/" + dbtype + "/bootstrap | sh"
	}
	if cluster.Configurator.HaveDBTag("package") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/package/linux/" + dbtype + "/bootstrap | sh"
	}

	out, err = client.Cmd(cmd).SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cluster.errorChan <- nil
}

func (cluster *Cluster) OnPremiseUnprovisionDatabaseService(server *ServerMonitor) {

	cluster.errorChan <- nil

}

func (cluster *Cluster) OnPremiseStopDatabaseService(server *ServerMonitor) error {
	//s.JobServerStop() need an agent or ssh to trigger this
	server.Shutdown()
	return nil
}

func (cluster *Cluster) OnPremiseStartDatabaseService(server *ServerMonitor) error {

	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		return err
	}
	defer client.Close()
	adminuser := "admin"
	adminpassword := "repman"

	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	out, err := client.Cmd("export MYSQL_ROOT_PASSWORD=" + server.Pass).Cmd("export REPLICATION_MANAGER_URL=" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort).Cmd("export REPLICATION_MANAGER_USER=" + adminuser).Cmd("export REPLICATION_MANAGER_PASSWORD=" + adminpassword).Cmd("export REPLICATION_MANAGER_HOST_NAME=" + server.Host).Cmd("export REPLICATION_MANAGER_HOST_PORT=" + server.Port).Cmd("export REPLICATION_MANAGER_CLUSTER_NAME=" + server.ClusterGroup.Name).SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	dbtype := "mariadb"
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	cmd := "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/debian/" + dbtype + "/start | sh"
	if cluster.Configurator.HaveDBTag("rpm") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/redhat/" + dbtype + "/start | sh"
	}
	if cluster.Configurator.HaveDBTag("package") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/package/linux/" + dbtype + "/start | sh"
	}
	out, err = client.Cmd(cmd).SmartOutput()
	if err != nil {
		cluster.errorChan <- err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	return nil
}
