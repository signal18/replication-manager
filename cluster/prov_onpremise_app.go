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
	"github.com/signal18/replication-manager/cluster/app"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) OnPremiseConnectApp(server app.AppInterface) (*sshclient.Client, error) {

	if cluster.IsInFailover() {
		return nil, errors.New("OnPremise Provisioning cancel during connect")
	}
	if !cluster.Conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}

	user, password := misc.SplitPair(cluster.Conf.GetDecryptedValue("onpremise-ssh-credential"))

	key := cluster.OnPremiseGetSSHKey()
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

func (cluster *Cluster) OnPremiseProvisionAppService(appi app.AppInterface) error {
	appi.GetAppConfig()

	if apl, ok := appi.(*app.AppWebDevOps); ok {
		err := apl.OnPremiseProvisionService(cluster.errorChan)
		cluster.errorChan <- err
		return err
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseUnprovisionAppService(appi app.AppInterface) error {
	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.OnPremiseUnprovisionService(cluster.errorChan)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStartAppService(appi app.AppInterface) error {
	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.OnPremiseStartService(cluster.errorChan)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStopAppService(appi app.AppInterface) error {

	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.OnPremiseStopService(cluster.errorChan)
	}

	return nil
}
