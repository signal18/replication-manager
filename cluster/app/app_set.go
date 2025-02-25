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
package app

import (
	"hash/crc64"
	"os"
	"strconv"
	"strings"

	"github.com/signal18/replication-manager/config"
)

func (app *App) SetID(crctable *crc64.Table) {
	app.Id = "app" + strconv.FormatUint(
		crc64.Checksum([]byte(app.Clustername+app.Name+":"+app.Port), crctable),
		10)
}

func (app *App) SetLock() {
	app.Lock.Lock()
}

// TODO: clarify where this is used, can maybe be replaced with a Getter
func (app *App) SetServiceName(namespace string) {
	app.ServiceName = namespace + "/svc/" + app.Name
}

func (app *App) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string, ProxysqlHostsIPV6 string, Weights string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	agents := strings.Split(ProvAgents, ",")
	ipv6hosts := strings.Split(ProxysqlHostsIPV6, ",")
	weights := strings.Split(Weights, ",")
	if k < len(slapospartitions) {
		app.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		app.Agent = agents[k%len(agents)]
	}
	if Weights != "" {
		app.Weight = weights[k%len(weights)]
	}

	if k < len(ipv6hosts) {
		app.HostIPV6 = ipv6hosts[k]
	}
}

func (app *App) SetDataDir() {
	if app.Host != "" {
		app.Datadir = app.ClusterConfig.WorkingDir + "/" + app.Clustername + "/" + app.Host + "_" + app.Port
		if _, err := os.Stat(app.Datadir); os.IsNotExist(err) {
			os.MkdirAll(app.Datadir, os.ModePerm)
			os.MkdirAll(app.Datadir+"/log", os.ModePerm)
			os.MkdirAll(app.Datadir+"/var", os.ModePerm)
			os.MkdirAll(app.Datadir+"/init", os.ModePerm)

		}
	}
}

func (app *App) createCookie(key string) error {
	newFile, err := os.Create(app.Datadir + "/@" + key)
	defer newFile.Close()
	if err != nil {
		app.Logger.Debugf(app.Clustername, config.ConstLogModProxy, config.LvlDbg, "Create cookie (%s) %s", key, err)
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

func (app *App) SetPrevState(state string) {
	app.PrevState = state
}

func (app *App) SetSuspect() {
	app.State = stateSuspect
}

func (app *App) SetFailCount(c int) {
	app.FailCount = c
}

func (app *App) SetState(v string) {
	app.State = v
}

func (app *App) SetClustername(c string) {
	app.Clustername = c
}

func (app *App) SetProvAppImage(value string) error {
	app.AppConfig.ProvAppDockerImg = value
	return nil
}
func (app *App) SetProvAppAgents(value string) error {
	app.AppConfig.ProvAppAgents = value
	return nil
}
func (app *App) SetAppDiskSize(value string) error {
	app.AppConfig.ProvAppDiskSize = value
	return nil
}
func (app *App) SetAppCores(value string) error {
	app.AppConfig.ProvAppCpuCores = value
	return nil
}
func (app *App) SetAppMemorySize(value string) error {
	app.AppConfig.ProvAppMemory = value
	return nil
}
func (app *App) SetAppVolumeData(value string) error {
	app.AppConfig.ProvAppVolumeData = value
	return nil
}
func (app *App) SetAppDockerRunArgs(value string) error {
	app.AppConfig.ProvAppDockerRunArgs = value
	return nil
}
