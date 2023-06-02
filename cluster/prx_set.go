// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"hash/crc64"
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/utils/misc"
)

func (p *Proxy) SetID() {
	cluster := p.ClusterGroup
	p.Id = "px" + strconv.FormatUint(
		crc64.Checksum([]byte(cluster.Name+p.Name+":"+strconv.Itoa(p.WritePort)), cluster.crcTable),
		10)
}

// TODO: clarify where this is used, can maybe be replaced with a Getter
func (proxy *Proxy) SetServiceName(namespace string) {
	proxy.ServiceName = namespace + "/svc/" + proxy.Name
}

func (proxy *Proxy) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string, ProxysqlHostsIPV6 string, Weights string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	agents := strings.Split(ProvAgents, ",")
	ipv6hosts := strings.Split(ProxysqlHostsIPV6, ",")
	weights := strings.Split(Weights, ",")
	if k < len(slapospartitions) {
		proxy.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		proxy.Agent = agents[k%len(agents)]
	}
	if Weights != "" {
		proxy.Weight = weights[k%len(weights)]
	}

	if k < len(ipv6hosts) {
		proxy.HostIPV6 = ipv6hosts[k]
	}
}

func (proxy *Proxy) SetDataDir() {

	proxy.Datadir = proxy.ClusterGroup.Conf.WorkingDir + "/" + proxy.ClusterGroup.Name + "/" + proxy.Host + "_" + proxy.Port
	if _, err := os.Stat(proxy.Datadir); os.IsNotExist(err) {
		os.MkdirAll(proxy.Datadir, os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/log", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/var", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/init", os.ModePerm)
		os.MkdirAll(proxy.Datadir+"/bck", os.ModePerm)
	}
}

func (proxy *Proxy) createCookie(key string) error {
	newFile, err := os.Create(proxy.Datadir + "/@" + key)
	defer newFile.Close()
	if err != nil {
		proxy.ClusterGroup.LogPrintf(LvlDbg, "Create cookie (%s) %s", key, err)
	}
	return err
}

func (proxy *Proxy) SetProvisionCookie() error {
	return proxy.createCookie("cookie_prov")
}

func (proxy *Proxy) SetUnprovisionCookie() error {
	return proxy.createCookie("cookie_unprov")
}

func (proxy *Proxy) SetWaitStartCookie() error {
	return proxy.createCookie("cookie_waitstart")
}

func (proxy *Proxy) SetWaitStopCookie() error {
	return proxy.createCookie("cookie_waitstop")
}

func (proxy *Proxy) SetRestartCookie() error {
	return proxy.createCookie("cookie_restart")
}

func (proxy *Proxy) SetReprovCookie() error {
	return proxy.createCookie("cookie_reprov")
}

func (p *Proxy) SetPrevState(state string) {
	p.PrevState = state
}

func (p *Proxy) SetSuspect() {
	p.State = stateSuspect
}

func (p *Proxy) SetFailCount(c int) {
	p.FailCount = c
}

func (p *Proxy) SetCredential(credential string) {
	p.User, p.Pass = misc.SplitPair(credential)
}

func (p *Proxy) SetState(v string) {
	p.State = v
}

func (p *Proxy) SetCluster(c *Cluster) {
	p.ClusterGroup = c
}
