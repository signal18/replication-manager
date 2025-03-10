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

func (cluster *Cluster) OnPremiseConnectApp(apl *app.App) (*sshclient.Client, error) {

	if cluster.IsInFailover() {
		return nil, errors.New("OnPremise Provisioning cancel during connect")
	}
	if !cluster.Conf.OnPremiseSSH {
		return nil, errors.New("onpremise-ssh disable ")
	}

	user, password := misc.SplitPair(cluster.Conf.GetDecryptedValue("onpremise-ssh-credential"))

	key := cluster.OnPremiseGetSSHKey()
	if password != "" {
		client, err := sshcli.DialWithPasswd(misc.Unbracket(apl.GetHost())+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, password)
		if err != nil {
			return nil, errors.New(fmt.Sprintf("OnPremise Provisioning via SSH %s %s", err.Error(), key))
		}
		return client, nil
	} else {
		client, err := sshcli.DialWithKey(misc.Unbracket(apl.GetHost())+":"+strconv.Itoa(cluster.Conf.OnPremiseSSHPort), user, key)
		if err != nil {
			return nil, errors.New("OnPremise Provisioning via SSH %s" + err.Error())
		}
		return client, nil
	}
}

func (cluster *Cluster) OnPremiseProvisionAppService(apl *app.App) error {
	err := apl.OnPremiseProvisionService(cluster.errorChan)
	cluster.errorChan <- err
	return err
}

func (cluster *Cluster) OnPremiseUnprovisionAppService(apl *app.App) error {
	apl.OnPremiseUnprovisionService(cluster.errorChan)

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStartAppService(apl *app.App) error {
	apl.OnPremiseStartService(cluster.errorChan)

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OnPremiseStopAppService(apl *app.App) error {
	apl.OnPremiseStopService(cluster.errorChan)

	return nil
}
