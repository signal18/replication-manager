// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"fmt"
	"strconv"

	"github.com/helloyi/go-sshclient"
	sshcli "github.com/helloyi/go-sshclient"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseProvisionBootsrapProxy(server DatabaseProxy, client *sshclient.Client) error {
	adminuser := "admin"
	adminpassword := "repman"
	if user, ok := cluster.APIUsers[adminuser]; ok {
		adminpassword = user.Password
	}
	envs := "export REPLICATION_MANAGER_URL=\"" + cluster.Conf.APIPublicURL + "\""
	envs += " REPLICATION_MANAGER_USER=\"" + adminuser + "\""
	envs += " REPLICATION_MANAGER_PASSWORD=\"" + adminpassword + "\""
	envs += " REPLICATION_MANAGER_HOST_NAME=\"" + server.GetHost() + "\""
	envs += " REPLICATION_MANAGER_HOST_PORT=\"" + server.GetPort() + "\""
	envs += " REPLICATION_MANAGER_CLUSTER_NAME=\"" + cluster.Name + "\""
        cmd := envs + "&& "
	cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/debian/" + server.GetType() + "/bootstrap | sh"
	if cluster.Configurator.HaveDBTag("rpm") {
		cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/repository/redhat/" + server.GetType() + "/bootstrap | sh"
	}
	if cluster.Configurator.HaveDBTag("package") {
		cmd += "wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/onpremise/package/linux/" + server.GetType() + "/bootstrap | sh"
	}

	out, err := client.Cmd(cmd).SmartOutput()
	if err != nil {
		return errors.New("OnPremise Bootsrap via SSH %s" + err.Error())
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "OnPremise Bootsrap  : %s", string(out))
	return nil
}

func (cluster *Cluster) OnPremiseConnectProxy(server DatabaseProxy) (*sshclient.Client, error) {

	if cluster.IsInFailover() {
		return nil, errors.New("OnPremise Provisioning cancel during connect")
	}
	if ! cluster.Conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}

	user, password := misc.SplitPair(cluster.Conf.GetDecryptedValue("onpremise-ssh-credential"))

	key := cluster.OnPremiseGetSSHKey(user)
	if password != "" {
		client, err := sshcli.DialWithPasswd(misc.Unbracket(server.GetHost())+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, password)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("OnPremise Provisioning via SSH %s %s", err.Error(), key))
		}
		return client, nil
	} else {
		client, err := sshcli.DialWithKey(misc.Unbracket(server.GetHost())+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, key)
		if err != nil {
			return nil, errors.New("OnPremise Provisioning via SSH %s" + err.Error())
		}
		return client, nil
	}
}

func (cluster *Cluster) OnPremiseProvisionProxyService(pri DatabaseProxy) error {
	pri.GetProxyConfig()

	if prx, ok := pri.(*MariadbShardProxy); ok {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Bootstrap MariaDB Sharding Cluster")
		// Source name will equal to cluster name
		srv, _ := cluster.newServerMonitor(prx.Host+":"+prx.GetPort(), prx.User, prx.Pass, true, "", cluster.Name)
		err := srv.Refresh()
		if err == nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Can connect to requested signal18 sharding proxy")
			//that's ok a sharding proxy can be decalre in multiple cluster , should not block provisionning
			cluster.errorChan <- err
			return nil
		}
		srv.ClusterGroup = cluster
		cluster.OnPremiseProvisionDatabaseService(srv)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Bootstrap MariaDB Sharding Cluster Failed")
			cluster.errorChan <- err
			return err
		}
		srv.Close()
		cluster.ShardProxyBootstrap(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		err := cluster.OnPremiseProvisionProxySQLService(prx)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Bootstrap Proxysql Failed")
			cluster.errorChan <- err
			return err
		}
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		err := cluster.OnPremiseProvisionHaProxyService(prx)
		cluster.errorChan <- err
		return err
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionProxyService(pri DatabaseProxy) error {
	if prx, ok := pri.(*MariadbShardProxy); ok {
		cluster.OnPremiseUnprovisionDatabaseService(prx.ShardProxy)
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.OnPremiseUnprovisionHaProxyService(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.OnPremiseUnprovisionProxySQLService(prx)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStartProxyService(pri DatabaseProxy) error {
	if prx, ok := pri.(*MariadbShardProxy); ok {
		cluster.OnPremiseStartDatabaseService(prx.ShardProxy)
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.OnPremiseStartHaProxyService(prx)
	}

	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.OnPremiseStartProxySQLService(prx)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStopProxyService(pri DatabaseProxy) error {

	if prx, ok := pri.(*MariadbShardProxy); ok {
		prx.ShardProxy.Shutdown()
	}

	if prx, ok := pri.(*HaproxyProxy); ok {
		cluster.OnPremiseStopHaproxyService(prx)
	}
	if prx, ok := pri.(*ProxySQLProxy); ok {
		cluster.OnPremiseStopProxySQLService(prx)
	}

	return nil
}
