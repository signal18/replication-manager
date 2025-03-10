// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"github.com/signal18/replication-manager/cluster/app"
)

func (cluster *Cluster) LocalhostProvisionAppService(apl *app.App) error {
	err := apl.LocalhostProvisionService(cluster.errorChan)
	cluster.errorChan <- err
	return err
}

func (cluster *Cluster) LocalhostUnprovisionAppService(apl *app.App) error {
	apl.LocalhostUnprovisionService(cluster.errorChan)
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStartAppService(apl *app.App) error {
	apl.LocalhostStartService()

	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) LocalhostStopAppService(apl *app.App) error {
	apl.LocalhostStopService()

	return nil
}
