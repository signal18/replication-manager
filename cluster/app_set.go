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
	"errors"
	"hash/crc64"
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (p *App) SetID() {
	cluster := p.ClusterGroup
	p.Id = "ap" + strconv.FormatUint(
		crc64.Checksum([]byte(cluster.Name+p.Name+":"+strconv.Itoa(p.WritePort)), cluster.crcTable),
		10)
}

func (p *App) SetLock() {
	p.Lock.Lock()
}

// TODO: clarify where this is used, can maybe be replaced with a Getter
func (app *App) SetServiceName(namespace string) {
	app.ServiceName = namespace + "/svc/" + app.Name
}

func (app *App) SetStaging(staging bool) {
	app.IsStaging = staging
}

func (app *App) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string, HostsIPV6 string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	agents := strings.Split(ProvAgents, ",")
	ipv6hosts := strings.Split(HostsIPV6, ",")
	if k < len(slapospartitions) {
		app.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		app.Agent = agents[k%len(agents)]
	}
	if k < len(ipv6hosts) {
		app.HostIPV6 = ipv6hosts[k]
	}
}

func (app *App) SetDataDir() {
	if app.Host != "" {
		app.Datadir = app.ClusterGroup.Conf.WorkingDir + "/" + app.ClusterGroup.Name + "/apps/" + app.Host
		if _, err := os.Stat(app.Datadir); os.IsNotExist(err) {
			os.MkdirAll(app.Datadir, os.ModePerm)
			os.MkdirAll(app.Datadir+"/log", os.ModePerm)
			os.MkdirAll(app.Datadir+"/var", os.ModePerm)
			os.MkdirAll(app.Datadir+"/init", os.ModePerm)
			os.MkdirAll(app.Datadir+"/bck", os.ModePerm)
		}
	}
}

func (app *App) createCookie(key string) error {
	newFile, err := os.Create(app.Datadir + "/@" + key)
	cluster := app.ClusterGroup
	defer newFile.Close()
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg, "Create cookie (%s) %s", key, err)
	}
	return err
}

func (app *App) SetProvisionCookie() error {
	return app.createCookie("cookie_prov")
}

func (app *App) SetUnprovisionCookie() error {
	return app.createCookie("cookie_unprov")
}

func (app *App) SetWaitStartCookie() error {
	return app.createCookie("cookie_waitstart")
}

func (app *App) SetWaitStopCookie() error {
	return app.createCookie("cookie_waitstop")
}

func (app *App) SetRestartCookie() error {
	return app.createCookie("cookie_restart")
}

func (app *App) SetReprovCookie() error {
	return app.createCookie("cookie_reprov")
}

func (app *App) SetConfigCookie() error {
	return app.createCookie("cookie_config")
}

func (app *App) SetConfigRefreshCookie() error {
	return app.createCookie("cookie_configrefresh")
}

func (app *App) SetNoConfigFetchCookie() error {
	return app.createCookie("cookie_noconfigfetch")
}

func (p *App) SetPrevState(state string) {
	p.PrevState = state
}

func (p *App) SetSuspect() {
	p.State = stateSuspect
}

func (p *App) SetFailCount(c int) {
	p.FailCount = c
}

func (p *App) SetCredential(credential string) {
	p.User, p.Pass = misc.SplitPair(credential)
}

func (p *App) SetState(v string) {
	p.State = v
}

func (p *App) SetCluster(c *Cluster) {
	p.ClusterGroup = c
}

func (p *App) SetDeploymentConfig(deployid string, dep *config.Deployment) error {
	if _, ok := p.ClusterGroup.Conf.Apps[p.Name]; !ok {
		return errors.New("app not found in cluster config")
	}

	p.ClusterGroup.Conf.Apps[p.Name].Deployments[deployid] = dep

	return nil
}
