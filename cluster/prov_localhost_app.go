// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"github.com/signal18/replication-manager/cluster/app"
)

func (cluster *Cluster) LocalhostProvisionAppService(appi app.AppInterface) error {
	appi.GetAppConfig()

	if apl, ok := appi.(*app.AppWebDevOps); ok {
		err := apl.LocalhostProvisionService(cluster.errorChan)
		cluster.errorChan <- err
		return err
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostUnprovisionAppService(appi app.AppInterface) error {
	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.LocalhostUnprovisionService(cluster.errorChan)
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStartAppService(appi app.AppInterface) error {
	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.LocalhostStartService()
	}

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopAppService(appi app.AppInterface) error {
	if apl, ok := appi.(*app.AppWebDevOps); ok {
		apl.LocalhostStopService()
	}

	return nil
}
