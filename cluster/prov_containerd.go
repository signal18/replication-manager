// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/containerd/oci"
)

func (cluster *Cluster) ContainerdFoundProxyAgent(proxy *Proxy) (string, error) {

	clusteragents := strings.Split(cluster.Conf.ProvProxAgents, ",")

	for i, srv := range cluster.Proxies {
		if srv.Id == proxy.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return "", errors.New("Indice not found in proxies agent list")
}

func (cluster *Cluster) ContainerdFoundDatabaseAgent(proxy *ServerMonitor) (string, error) {

	clusteragents := strings.Split(cluster.Conf.ProvProxAgents, ",")

	for i, srv := range cluster.Proxies {
		if srv.Id == proxy.Id {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return "", errors.New("Indice not found in proxies agent list")
}

func (cluster *Cluster) ContainerdConnect(agent string) (*containerd.Client, error) {
	options := []containerd.ClientOpt{
		containerd.WithDefaultNamespace("test"),
	}

	client, err := containerd.New("/var/run/replication-manager-containerd.sock", options...)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (cluster *Cluster) ContainerdClose(client *containerd.Client) {
	defer client.Close()
}

func (cluster *Cluster) ContainerdProvisionCluster() error {
	err := cluster.ContainerdProvisionDatabases()
	err = cluster.ContainerdProvisionProxies()
	return err
}

func (cluster *Cluster) ContainerdProvisionDatabases() error {
	for _, s := range cluster.Servers {
		go cluster.ContainerdProvisionDatabaseService(s)
	}
	for _, s := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Provisionning error %s on  %s", err, cluster.Name+"/"+s.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Provisionning done for database %s", cluster.Name+"/"+s.Name)
			}
		}
	}
	return nil
}

func (cluster *Cluster) ContainerdUnprovision() {
	for _, db := range cluster.Servers {
		go cluster.ContainerdUnprovisionDatabaseService(db)
	}
	for _, db := range cluster.Servers {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning error %s on  %s", err, db.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for database %s", db.Name)
			}
		}
	}
	for _, prx := range cluster.Proxies {
		go cluster.ContainerdUnprovisionProxyService(prx)
	}
	for _, prx := range cluster.Proxies {
		select {
		case err := <-cluster.errorChan:
			if err != nil {
				cluster.LogPrintf(LvlErr, "Unprovisionning proxy error %s on  %s", err, prx.Name)
			} else {
				cluster.LogPrintf(LvlInfo, "Unprovisionning done for proxy %s", prx.Name)
			}
		}
	}
}

func (cluster *Cluster) ContainerdProvisionDatabaseService(s *ServerMonitor) {
	node, err := cluster.ContainerdFoundDatabaseAgent(s)
	svc, _ := cluster.ContainerdConnect(node)
	defer cluster.ContainerdClose(svc)
	ctx := namespaces.WithNamespace(context.Background(), cluster.Name)
	image, err := svc.Pull(ctx, "docker.io/library/"+s.ClusterGroup.Conf.ProvDbImg, containerd.WithPullUnpack)
	if err != nil {
		cluster.errorChan <- err
		return
	}
	log.Printf("Successfully pulled %s image\n", image.Name())
	container, err := svc.NewContainer(
		ctx,
		s.Name+"/"+cluster.Name,
		containerd.WithNewSnapshot("db-server-snapshot", image),
		containerd.WithNewSpec(oci.WithImageConfig(image)),
	)
	s.IDContainerd = container.ID()
	if err != nil {
		cluster.errorChan <- err
		return
	}

	cluster.WaitDatabaseStart(s)

	cluster.errorChan <- err
}

func (cluster *Cluster) ContainerdUnprovisionDatabaseService(db *ServerMonitor) {
	node, _ := cluster.ContainerdFoundDatabaseAgent(db)
	cl, _ := cluster.ContainerdConnect(node)
	defer cluster.ContainerdClose(cl)

	ctx := namespaces.WithNamespace(context.Background(), cluster.Name)
	containers, err := cl.Containers(ctx, "")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get containerd container list %s, %s", cluster.Name+"/"+db.Name, err)
	} else {
		for _, c := range containers {
			if cluster.Name+"/"+db.Name == c.ID() {
				err := c.Delete(ctx, containerd.WithSnapshotCleanup)
				if err != nil {
					cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", cluster.Name+"/"+db.Name, err)
				}
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) ContainerdUnprovisionProxyService(prx *Proxy) {
	node, _ := cluster.ContainerdFoundProxyAgent(prx)
	cl, _ := cluster.ContainerdConnect(node)
	defer cluster.ContainerdClose(cl)

	ctx := namespaces.WithNamespace(context.Background(), cluster.Name)
	containers, err := cl.Containers(ctx, "")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get containerd container list %s, %s", cluster.Name+"/"+prx.Name, err)
	} else {
		for _, c := range containers {
			if cluster.Name+"/"+prx.Name == c.ID() {
				err := c.Delete(ctx, containerd.WithSnapshotCleanup)
				if err != nil {
					cluster.LogPrintf(LvlErr, "Can't unprovision Proxy %s, %s", cluster.Name+"/"+prx.Name, err)
				}
			}
		}
	}
	cluster.errorChan <- nil
}

func (cluster *Cluster) ContainerdStopDatabaseService(db *ServerMonitor) error {
	node, _ := cluster.ContainerdFoundDatabaseAgent(db)
	cl, _ := cluster.ContainerdConnect(node)
	defer cluster.ContainerdClose(cl)

	ctx := namespaces.WithNamespace(context.Background(), cluster.Name)
	containers, err := cl.Containers(ctx, "")
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't get containerd container list %s, %s", cluster.Name+"/"+db.Name, err)
	} else {
		for _, c := range containers {
			if cluster.Name+"/"+db.Name == c.ID() {
				err := c.Delete(ctx, containerd.WithSnapshotCleanup)
				if err != nil {
					cluster.LogPrintf(LvlErr, "Can't unprovision database %s, %s", cluster.Name+"/"+db.Name, err)
				}
			}
		}
	}
	return nil
}

func (cluster *Cluster) ContainerdStartDatabaseService(server *ServerMonitor) error {
	cluster.ContainerdProvisionDatabaseService(server)
	return nil
}

func (cluster *Cluster) ContainerdProvisionProxies() error {

	for _, prx := range cluster.Proxies {
		cluster.ContainerdProvisionProxyService(prx)
	}

	return nil
}

func (cluster *Cluster) ContainerdProvisionProxyService(prx *Proxy) error {
	node, _ := cluster.ContainerdFoundProxyAgent(prx)

	cl, err := cluster.ContainerdConnect(node)
	if err != nil {
		cluster.LogPrintf(LvlErr, "Can't connect containerd  %s, %s on node %s", cluster.Name+"/"+prx.Name, err, node)
		return err
	}
	defer cluster.ContainerdClose(cl)

	srvlist := make([]string, len(cluster.Servers))
	for i, s := range cluster.Servers {
		srvlist[i] = s.Host
	}

	if prx.Type == proxyMaxscale {

	}
	if prx.Type == proxySpider {

	}
	if prx.Type == proxyHaproxy {

	}
	if prx.Type == proxySphinx {

	}
	if prx.Type == proxySqlproxy {

	}
	return nil
}
