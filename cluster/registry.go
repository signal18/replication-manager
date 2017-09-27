// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster
import "github.com/signal18/replication-manager/registry"
import "github.com/signal18/replication-manager/registry/mock"
import "github.com/signal18/replication-manager/registry/consul"

func (cluster *Cluster) registerConsul() error {

l, err := net.Listen("tcp", ":0")
if err != nil {
  // blurgh?!!
  panic(err.Error())
}
cfg := consul.DefaultConfig()
cfg.Address = l.Addr().String()

cl, _ := consul.NewClient(cfg)

var opt registry.Options

opt.Addrs:= strings.Split(cluster.conf.RegistryHosts, ",")
	//r := mock.NewRegistry()
var s registry.Service
s.Name ="master." / cluster.Name

c:= consul.NewRegistry(opt)
c.Register

	return nil
}
