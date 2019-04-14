// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"

	"github.com/signal18/replication-manager/router/sphinx"
	"github.com/signal18/replication-manager/utils/state"
)

func connectSphinx(proxy *Proxy) (sphinx.SphinxSQL, error) {
	sphinx := sphinx.SphinxSQL{
		User:     proxy.User,
		Password: proxy.Pass,
		Host:     proxy.Host,
		Port:     proxy.Port,
	}

	var err error
	err = sphinx.Connect()
	if err != nil {
		return sphinx, err
	}
	return sphinx, nil
}

func (cluster *Cluster) initSphinx(proxy *Proxy) {
	if cluster.Conf.SphinxOn == false {
		return
	}

	sphinx, err := connectSphinx(proxy)
	if err != nil {
		cluster.sme.AddState("ERR00058", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00058"], err), ErrFrom: "MON"})
		return
	}
	defer sphinx.Connection.Close()

}

func (cluster *Cluster) refreshSphinx(proxy *Proxy) {
	if cluster.Conf.SphinxOn == false {
		return
	}

	sphinx, err := connectSphinx(proxy)
	if err != nil {
		cluster.sme.AddState("ERR00058", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00058"], err), ErrFrom: "MON"})
		return
	}
	defer sphinx.Connection.Close()
	proxy.Version = sphinx.GetVersion()

	proxy.BackendsWrite = nil
	proxy.BackendsRead = nil

	status, err := sphinx.GetStatus()
	var bke = Backend{
		Host:           cluster.Conf.ProvProxRouteAddr,
		Port:           cluster.Conf.ProvProxRoutePort,
		Status:         "UP",
		PrxName:        "",
		PrxStatus:      "UP",
		PrxConnections: status["CONNECTIONS"],
		PrxByteIn:      "0",
		PrxByteOut:     "0",
		PrxLatency:     status["AVG_QUERY_WALL"],
	}
	if err == nil {
		proxy.BackendsWrite = append(proxy.BackendsRead, bke)
	}
}

func (cluster *Cluster) setMaintenanceSphinx(proxy *Proxy, host string, port string) {
	if cluster.Conf.SphinxOn == false {
		return
	}

}
