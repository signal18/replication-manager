package cluster

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/helloyi/go-sshclient"
	sshcli "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseGetSSHKey(user string) string {

	repmanuser := os.Getenv("HOME")
	if repmanuser == "" {
		repmanuser = "/root"
		if user != "root" {
			repmanuser = "/home/" + user
		}
	}
	key := repmanuser + "/.ssh/id_rsa"

	if cluster.Conf.OnPremiseSSHPrivateKey != "" {
		key = cluster.Conf.OnPremiseSSHPrivateKey
	}
	return key
}

func (cluster *Cluster) OnPremiseConnect(server *ServerMonitor) (*sshclient.Client, error) {
	if cluster.IsInFailover() {
		return nil, errors.New("OnPremise provisioning cancel during failover")
	}
	if !cluster.Conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}
	user, password := misc.SplitPair(cluster.Conf.OnPremiseSSHCredential)

	key := cluster.OnPremiseGetSSHKey(user)
	if password != "" {
		client, err := sshcli.DialWithPasswd(misc.Unbracket(server.Host)+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, password)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("OnPremise Provisioning via SSH %s %s", err.Error(), key))
		}
		return client, nil
	} else {
		client, err := sshcli.DialWithKey(misc.Unbracket(server.Host)+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, key)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("OnPremise Provisioning via SSH %s %s", err.Error(), key))
		}
		return client, nil
	}
	return nil, errors.New("onpremise-ssh no key no password ")
}

func (cluster *Cluster) OnPremiseProvisionDatabaseService(server *ServerMonitor) {
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		cluster.errorChan <- err
	}
	defer client.Close()
	err = cluster.OnPremiseSetEnv(client, server)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "OnPremise start database failed in env setup : %s", err)
		cluster.errorChan <- err
	}
	dbtype := "mariadb"
	cmd := "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/debian/" + dbtype + "/bootstrap | sh"
	if cluster.Configurator.HaveDBTag("rpm") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/redhat/" + dbtype + "/bootstrap | sh"
	}
	if cluster.Configurator.HaveDBTag("package") {
		cmd = "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/package/linux/" + dbtype + "/bootstrap | sh"
	}

	out, err := client.Cmd(cmd).SmartOutput()
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

func (cluster *Cluster) OnPremiseSetEnv(client *sshclient.Client, server *ServerMonitor) error {
	adminuser := "admin"
	adminpassword := "repman"

	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	buf := strings.NewReader("export MYSQL_ROOT_PASSWORD=\"" + server.Pass + "\";export REPLICATION_MANAGER_URL=\"https://" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + server.Host + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + server.Port + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + server.ClusterGroup.Name + "\"")
	/*	REPLICATION_MANAGER_USER
		REPLICATION_MANAGER_PASSWORD
		REPLICATION_MANAGER_URL
		REPLICATION_MANAGER_CLUSTER_NAME
		REPLICATION_MANAGER_HOST_NAME
		REPLICATION_MANAGER_HOST_PORT  */
	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	var err error
	if client.Shell().SetStdio(buf, &stdout, &stderr).Start(); err != nil {
		server.ClusterGroup.LogPrintf(LvlWarn, "OnPremise start ssh setup env %s", stderr.String())
		return err
	}
	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise start database install secret env: %s", stdout.String())

	/*out := stdout.String()
	out, err := client.Cmd("export MYSQL_ROOT_PASSWORD=" + server.Pass).Cmd("export REPLICATION_MANAGER_URL=https://" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort).Cmd("export REPLICATION_MANAGER_USER=" + adminuser).Cmd("export REPLICATION_MANAGER_PASSWORD=" + adminpassword).Cmd("export REPLICATION_MANAGER_HOST_NAME=" + server.Host).Cmd("export REPLICATION_MANAGER_HOST_PORT=" + server.Port).Cmd("export REPLICATION_MANAGER_CLUSTER_NAME=" + server.ClusterGroup.Name).SmartOutput()
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "OnPremise start database : %s", err)
		return err
		server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise start database install secret env: %s", string(out))

	}*/

	return nil
}

func (cluster *Cluster) OnPremiseStartDatabaseService(server *ServerMonitor) error {

	server.SetWaitStartCookie()
	client, err := cluster.OnPremiseConnect(server)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "OnPremise start database : %s", err)
		return err
	}
	defer client.Close()
	//	err = cluster.OnPremiseSetEnv(client, server)

	//if err != nil {
	//	server.ClusterGroup.LogPrintf(LvlErr, "OnPremise start database failed in env setup : %s", err)
	//	return err
	//	}
	dbtype := "mariadb"

	cmd := cluster.Conf.HttpRoot + "/static/configurator/onpremise/repository/debian/" + dbtype + "/start"
	if cluster.Configurator.HaveDBTag("rpm") {
		cmd = cluster.Conf.HttpRoot + "/static/configurator/onpremise/repository/redhat/" + dbtype + "/start"
	}
	if cluster.Configurator.HaveDBTag("package") {
		cmd = cluster.Conf.HttpRoot + "/static/configurator/onpremise/package/linux/" + dbtype + "/start"
	}

	filerc, err := os.Open(cmd)
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlErr, "JobRunViaSSH %s", err)
		return errors.New("can't open script")
	}

	defer filerc.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(filerc)

	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := server.ClusterGroup.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	buf2 := strings.NewReader("export MYSQL_ROOT_PASSWORD=\"" + server.Pass + "\";export REPLICATION_MANAGER_URL=\"https://" + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.APIPort + "\";export REPLICATION_MANAGER_USER=\"" + adminuser + "\";export REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\";export REPLICATION_MANAGER_HOST_NAME=\"" + server.Host + "\";export REPLICATION_MANAGER_HOST_PORT=\"" + server.Port + "\";export REPLICATION_MANAGER_CLUSTER_NAME=\"" + server.ClusterGroup.Name + "\"\nsudo -E ")
	r := io.MultiReader(buf2, buf)

	var (
		stdout bytes.Buffer
		stderr bytes.Buffer
	)
	if client.Shell().SetStdio(r, &stdout, &stderr).Start(); err != nil {
		server.ClusterGroup.LogPrintf(LvlWarn, "OnPremise start database via ssh %s", stderr.String())
	}
	out := stdout.String()

	server.ClusterGroup.LogPrintf(LvlInfo, "OnPremise start scipt: %s ,out: %s ,err: %s", cmd, out, stderr.String())
	/*	out, err := client.Cmd(cmd).SmartOutput()
		if err != nil {
			server.ClusterGroup.LogPrintf(LvlErr, "OnPremise start database : %s", err)
			return err
		}

	*/
	return nil
}
