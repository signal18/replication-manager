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

func (proxy *Proxy) SetServiceName(namespace string, name string) {
	proxy.ServiceName = namespace + "/svc/" + name
}

func (proxy *Proxy) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string, ProxysqlHostsIPV6 string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	agents := strings.Split(ProvAgents, ",")
	ipv6hosts := strings.Split(ProxysqlHostsIPV6, ",")
	if k < len(slapospartitions) {
		proxy.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		proxy.Agent = agents[k%len(agents)]
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

func (proxy *Proxy) SetProvisionCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_prov")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
}

func (proxy *Proxy) SetWaitStartCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_waitstart")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
}

func (proxy *Proxy) SetWaitStopCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_waitstop")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
}

func (proxy *Proxy) SetRestartCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_restart")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
}

func (proxy *Proxy) SetReprovCookie() {
	newFile, err := os.Create(proxy.Datadir + "/@cookie_reprov")
	if err != nil {
		fmt.Println("Error:", err)
	}
	newFile.Close()
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
