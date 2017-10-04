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
	"errors"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/registry"
)

func (cluster *Cluster) initConsul() error {
	var opt registry.Options
	//opt := consul.DefaultConfig()
	if cluster.conf.RegistryConsul == false {
		return nil
	}
	opt.Addrs = strings.Split(cluster.conf.RegistryHosts, ",")
	//DefaultRegistry()
	//opt := registry.DefaultRegistry

	if cluster.GetMaster() == nil {
		return errors.New("No master discovered")
	}
	port, _ := strconv.Atoi(cluster.GetMaster().Port)
	writesrv := map[string][]*registry.Service{
		"write": []*registry.Service{
			{
				Name:    "write_" + cluster.GetName(),
				Version: "0.0.0",
				Nodes: []*registry.Node{
					{
						Id:      "write_" + cluster.GetName(),
						Address: cluster.GetMaster().Host,
						Port:    port,
					},
				},
			},
		},
	}

	reg := registry.NewRegistry()
	for _, srv := range cluster.servers {
		var readsrv registry.Service
		readsrv.Name = "read_" + cluster.GetName()
		readsrv.Version = "0.0.0"
		var readnodes []*registry.Node
		var node registry.Node
		node.Id = srv.Id
		node.Address = srv.Host
		port, _ = strconv.Atoi(srv.Port)
		node.Port = port
		readnodes = append(readnodes, &node)
		readsrv.Nodes = readnodes
		cluster.LogPrintf("INFO", "Register consul read service  %s", srv.Id)
		if err := reg.Deregister(&readsrv); err != nil {
			cluster.LogPrintf("ERROR", "Unexpected deregister error: %v", err)
		}
		if err := reg.Register(&readsrv); err != nil {
			cluster.LogPrintf("ERROR", "Unexpected deregister error: %v", err)
		}

	}
	cluster.LogPrintf("INFO", "Register consul master ID %s with host %s", "write_"+cluster.GetName(), cluster.GetMaster().DSN)
	delservice, err := reg.GetService("write_" + cluster.GetName())
	if err != nil {
		for _, service := range delservice {

			if err := reg.Deregister(service); err != nil {
				cluster.LogPrintf("ERROR", "Unexpected deregister error: %v", err)
			}
		}
	}
	//reg := registry.NewRegistry()
	for _, v := range writesrv {
		for _, service := range v {

			if err := reg.Register(service); err != nil {
				cluster.LogPrintf("ERROR", "Unexpected register error: %v", err)
			}

		}
	}

	return nil
}
