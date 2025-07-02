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

func (app *App) SetID() {
	cluster := app.ClusterGroup
	app.Id = "ap" + strconv.FormatUint(
		crc64.Checksum([]byte(cluster.Name+app.Name), cluster.crcTable),
		10)
}

// TODO: clarify where this is used, can maybe be replaced with a Getter
func (app *App) SetServiceName(namespace string) {
	app.ServiceName = namespace + "/svc/" + app.Name
}

func (app *App) SetPlacement(k int, ProvAgents string, SlapOSDBPartitions string) {
	slapospartitions := strings.Split(SlapOSDBPartitions, ",")
	agents := strings.Split(ProvAgents, ",")
	if k < len(slapospartitions) {
		app.SlapOSDatadir = slapospartitions[k]
	}
	if ProvAgents != "" {
		app.Agent = agents[k%len(agents)]
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

func (app *App) SetPrevState(state string) {
	app.PrevState = state
}

func (app *App) SetSuspect() {
	app.State = stateSuspect
}

func (app *App) SetFailCount(c int) {
	app.FailCount = c
}

func (app *App) SetCredential(credential string) {
	app.User, app.Pass = misc.SplitPair(credential)
}

func (app *App) SetState(v string) {
	app.State = v
}

func (app *App) SetCluster(c *Cluster) {
	app.ClusterGroup = c
}

func (app *App) SetSetting(key, value string) error {
	switch key {
	case "prov-app-docker-img":
		app.AppConfig.ProvAppDockerImg = value
	case "prov-app-agents":
		app.AppConfig.ProvAppAgents = value
	case "prov-app-template":
		app.AppConfig.ProvAppTemplate = value
	case "app-port":
		app.AppConfig.AppPort = value
	case "app-db-user":
		app.AppConfig.AppDbUser = value
	case "app-db-pass":
		app.AppConfig.AppDbPass = value
	case "app-db-schema":
		app.AppConfig.AppDbSchema = value
	case "prov-app-credit-planned":
		creditPlanSize, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("invalid credit planned value: " + value)
		}
		if creditPlanSize < 1 {
			return errors.New("credit planned must be greater than or equal to 1")
		}
		if err := app.SetAppProvisionByCredit(creditPlanSize); err != nil {
			return err
		}
	case "prov-app-ha-topology":
		numagents := len(app.GetAppAgents())
		switch value {
		case "flex", "failover":
			if app.AppConfig.ProvAppHATopology != value {
				app.AppConfig.ProvAppHATopology = value
				if value == "flex" {
					app.AppConfig.ProvAppCreditPlanned = app.AppConfig.ProvAppCreditPlanned * numagents
					if app.AppConfig.ProvAppCreditPlanned > app.ClusterGroup.Conf.Cloud18ApplicationCreditsUsed {
						app.ClusterGroup.Conf.Cloud18ApplicationCreditsUsed = app.ClusterGroup.Conf.Cloud18ApplicationCreditsUsed - app.AppConfig.ProvAppCreditUsed + app.AppConfig.ProvAppCreditPlanned
					}
				} else {
					if numagents > 0 {
						app.AppConfig.ProvAppCreditPlanned = app.AppConfig.ProvAppCreditPlanned / numagents
					} else {
						app.AppConfig.ProvAppCreditPlanned = 0
					}
				}
				app.SetReprovCookie()
			}
		default:
			return errors.New("invalid value for ha topology")
		}
	default:
		return errors.New("unknown setting: " + key)
	}

	return nil
}

func (app *App) SwitchSetting(key string) error {
	switch key {
	default:
		return errors.New("unknown setting: " + key)
	}

	return nil
}

func (app *App) SetMaintenance(maintenance bool) {
	if maintenance {
		app.State = stateMaintenance
	} else {
		app.State = stateAppRunning
	}
}

func (app *App) SetDefaultRoute(cloud18Domain, cloud18SubDomain, cloud18SubDomainZone, clusterName string) {
	if len(app.AppConfig.Deployment.Routes) == 0 {
		app.AppConfig.Deployment.Routes = make([]config.Route, 0)

		app.AppConfig.Deployment.Routes = append(app.AppConfig.Deployment.Routes, config.Route{
			CName:    app.Name + "." + clusterName + "." + cloud18SubDomain + "-" + cloud18SubDomainZone + "." + cloud18Domain + ".cloud18.io",
			Port:     app.AppConfig.AppPort,
			Protocol: "https",
		})
	}

}

func (app *App) UpdateVariable(vIndex int, field, newValue string) error {
	switch field {
	case "name":
		app.AppConfig.Deployment.Variables[vIndex].Name = newValue
	case "value":
		newValue, _ = app.ClusterGroup.ParseAppTemplate(newValue, app.AppClusterSubstitute)
		app.AppConfig.Deployment.Variables[vIndex].Value = newValue
	case "type":
		app.AppConfig.Deployment.Variables[vIndex].Type = newValue
	default:
		return errors.New("unknown variable field: " + field)
	}

	return nil
}

func (app *App) UpdateConditionalVariables(vIndex int, condValue config.AVSlice) error {
	old := app.AppConfig.Deployment.Variables[vIndex].Conditional
	addFunc := func(new config.AgentVariable) config.AgentVariable {
		new.Value, _ = app.ClusterGroup.ParseAppTemplate(new.Value, app.AppClusterSubstitute)
		return new
	}
	updateFunc := func(old, new config.AgentVariable) config.AgentVariable {
		new.Value, _ = app.ClusterGroup.ParseAppTemplate(new.Value, app.AppClusterSubstitute)
		return new
	}

	app.AppConfig.Deployment.Variables[vIndex].Conditional = old.Merge(condValue, addFunc, updateFunc)
	return nil
}

func (app *App) SetAppProvisionByCredit(creditPlanSize int) error {

	if creditPlanSize == app.AppConfig.ProvAppCreditPlanned {
		// No change in credit planned, nothing to do
		return nil
	}

	provCredit := creditPlanSize
	num_agents := len(app.GetAppAgents())

	if app.AppConfig.ProvAppHATopology == config.OpenSVCTopologyFlex {
		if num_agents == 0 {
			return errors.New("no agents available for flex provisioning")
		}
		if creditPlanSize%num_agents != 0 {
			return errors.New("credit planned must be a multiple of the number of agents for flex provisioning")
		}

		// For flex provisioning, we divide the credit planned by the number of agents
		provCredit = creditPlanSize / num_agents
	}

	baseCore, err := config.ParseUnitMeasurementToInt("0", app.ClusterGroup.Conf.ProvAppCpuCores, true)
	if err != nil {
		return err
	}
	baseMemory, err := config.ParseUnitMeasurementToInt("M", app.ClusterGroup.Conf.ProvAppMem, true)
	if err != nil {
		return err
	}
	baseDisk, err := config.ParseUnitMeasurementToInt("G", app.ClusterGroup.Conf.ProvAppDisk, true)
	if err != nil {
		return err
	}

	app.AppConfig.ProvAppCreditPlanned = creditPlanSize
	app.AppConfig.ProvAppCpuCores = strconv.Itoa(provCredit * baseCore)
	app.AppConfig.ProvAppMem = strconv.Itoa(provCredit * baseMemory)
	app.AppConfig.ProvAppDisk = strconv.Itoa(provCredit * baseDisk)

	// only reduce available credits if the credit planned is greater than the old credit, else do nothing and will update the used credits when application is re-provisioned
	if creditPlanSize >= app.AppConfig.ProvAppCreditUsed {
		app.ClusterGroup.Conf.Cloud18ApplicationCreditsUsed = app.ClusterGroup.Conf.Cloud18ApplicationCreditsUsed - app.AppConfig.ProvAppCreditUsed + creditPlanSize
	}

	app.SetReprovCookie()

	return nil
}

func (app *App) ApplyPlannedCredits() {
	// If the planned credits are not equal to the used credits, we need to update the used credits (usually happens when the app is re-provisioned with lower credits)
	if app.AppConfig.ProvAppCreditPlanned != app.AppConfig.ProvAppCreditUsed {
		app.AppConfig.ProvAppCreditUsed = app.AppConfig.ProvAppCreditPlanned
	}
}
