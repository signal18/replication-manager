package cluster

import (
	"errors"
	"os"

	sshcli "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseProvisionDatabaseService(server *ServerMonitor) {

	if server.ClusterGroup.IsInFailover() {
		cluster.errorChan <- errors.New("OnPremise Provisioning cancel during failover")
	}
	key := os.Getenv("HOME") + "/.ssh/id_rsa"
	client, err := sshcli.DialWithKey(misc.Unbracket(server.Host)+":22", "root", key)
	if err != nil {
		cluster.errorChan <- errors.New("OnPremise Provisioning via SSH %s" + err.Error())
	}
	defer client.Close()
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	out, err := client.Cmd("export REPLICATION_MANAGER_URL=" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort).Cmd("export REPLICATION_MANAGER_USER=" + adminuser).Cmd("export REPLICATION_MANAGER_PASSWORD=" + adminpassword).Cmd("export REPLICATION_MANAGER_HOST_NAME=" + server.Host).Cmd("export REPLICATION_MANAGER_HOST_PORT=" + server.Port).Cmd("export REPLICATION_MANAGER_CLUSTER_NAME=" + server.ClusterGroup.Name).SmartOutput()
	if err != nil {
		cluster.errorChan <- errors.New("OnPremise Provisioning via SSH %s" + err.Error())
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))
	out, err = client.Cmd("wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/opensvc/bootstrap | sh").SmartOutput()
	if err != nil {
		cluster.errorChan <- errors.New("OnPremise Provisioning via SSH %s" + err.Error())
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise Provisioning  : %s", string(out))

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
