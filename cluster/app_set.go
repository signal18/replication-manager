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
	"fmt"
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

// effectiveSizingMode returns the sizing mode that governs this app.
// Resolution order: app mode → cluster mode → legacy (empty string).
func (app *App) effectiveSizingMode() string {
	if app.AppConfig.ProvAppSizingMode != "" {
		return app.AppConfig.ProvAppSizingMode
	}
	if app.ClusterGroup.Conf.ProvAppSizingMode != "" {
		return app.ClusterGroup.Conf.ProvAppSizingMode
	}
	return ""
}

// IsManualCreditMode reports whether this app is currently governed by manual
// resource sizing (as opposed to unit/App-Unit or legacy sizing).
func (app *App) IsManualCreditMode() bool {
	return app.effectiveSizingMode() == config.AppSizingModeManual
}

// ManualCreditExcess computes the app's current configured resources (CPU
// cores, memory MB, disk GB) above the entitlement included by its planned
// credits. Only positive excess is tracked; zero outside manual mode. Pure,
// runtime-only computation — never persisted.
//
// ProvAppCpuCores/ProvAppMem/ProvAppDisk are per-agent-node values while
// credits/entitlement are whole-app totals, so per-node resources are scaled
// by agent count before comparing, or multi-agent apps undercount excess.
//
// Resources/agents are read through the cluster.GetApp{Cores,Memory,Disk,Agents}
// fallback resolvers rather than raw AppConfig fields, since apps that leave
// these empty inherit cluster-level defaults at provisioning time — reading
// raw fields would treat an inherited default as zero and undercount excess.
func (app *App) ManualCreditExcess() (cpuExcessCores, memExcessMB, diskExcessGB int) {
	if !app.IsManualCreditMode() {
		return 0, 0, 0
	}

	cluster := app.ClusterGroup
	cores, _ := strconv.Atoi(cluster.GetAppCores(app.AppConfig))
	memMB, _ := config.ParseUnitMeasurementToInt("M", cluster.GetAppMemory(app.AppConfig), false)
	diskGB, _ := config.ParseUnitMeasurementToInt("G", cluster.GetAppDisk(app.AppConfig), false)

	numAgents := 0
	for _, agent := range strings.Split(cluster.GetAppAgents(app.AppConfig), ",") {
		if strings.TrimSpace(agent) != "" {
			numAgents++
		}
	}
	if numAgents == 0 {
		numAgents = 1
	}
	totalCores := cores * numAgents
	totalMemMB := memMB * numAgents
	totalDiskGB := diskGB * numAgents

	credits := app.AppConfig.ProvAppCreditPlanned
	includedCPU := credits * config.AppUnitCpuCores
	includedMemMB := credits * config.AppUnitMemMB
	includedDiskGB := credits * config.AppUnitDiskGB

	return max(totalCores-includedCPU, 0), max(totalMemMB-includedMemMB, 0), max(totalDiskGB-includedDiskGB, 0)
}

// preservedLegacyInUnitPolicy reports whether the effective unit policy is coming
// from the cluster while this app itself has not been explicitly stamped as a
// unit-managed app. In that case, the app's stored CPU/memory/disk values are
// treated as preserved legacy values until the first unit-mode write.
func (app *App) preservedLegacyInUnitPolicy() bool {
	return app.effectiveSizingMode() == config.AppSizingModeUnit && app.AppConfig.ProvAppSizingMode != config.AppSizingModeUnit
}

func (app *App) deriveUnitFromStoredResources() int {
	cores, _ := strconv.Atoi(app.AppConfig.ProvAppCpuCores)
	memMB, _ := config.ParseUnitMeasurementToInt("M", app.AppConfig.ProvAppMem, false)
	diskGB, _ := config.ParseUnitMeasurementToInt("G", app.AppConfig.ProvAppDisk, false)
	unitFromCores, unitFromMem, unitFromDisk := 1, 1, 1
	if cores > config.AppUnitCpuCores {
		unitFromCores = (cores + config.AppUnitCpuCores - 1) / config.AppUnitCpuCores
	}
	if memMB > config.AppUnitMemMB {
		unitFromMem = (memMB + config.AppUnitMemMB - 1) / config.AppUnitMemMB
	}
	if diskGB > config.AppUnitDiskGB {
		unitFromDisk = (diskGB + config.AppUnitDiskGB - 1) / config.AppUnitDiskGB
	}
	appUnit := unitFromCores
	if unitFromMem > appUnit {
		appUnit = unitFromMem
	}
	if unitFromDisk > appUnit {
		appUnit = unitFromDisk
	}
	return appUnit
}

func (app *App) SetSetting(key, value string) error {
	switch key {
	case "prov-app-docker-img":
		app.AppConfig.ProvAppDockerImg = value
	case "prov-app-docker-cmd":
		app.AppConfig.ProvAppDockerCmd = value
	case "prov-app-agents":
		if app.effectiveSizingMode() == config.AppSizingModeUnit {
			// Unit mode only: preserve App Unit per agent, recalculate total credits.
			// Save previous agents so we can roll back if credit recalculation fails.
			oldCount := len(app.GetAppAgents())
			oldUnit := 1
			if app.preservedLegacyInUnitPolicy() {
				oldUnit = app.deriveUnitFromStoredResources()
			} else if oldCount > 0 && app.AppConfig.ProvAppCreditPlanned > 0 {
				oldUnit = app.AppConfig.ProvAppCreditPlanned / oldCount
			}
			prevAgents := app.AppConfig.ProvAppAgents
			app.AppConfig.ProvAppAgents = value
			newCount := len(app.GetAppAgents())
			if newCount == 0 {
				newCount = 1
			}
			if err := app.SetAppProvisionByCredit(oldUnit * newCount); err != nil {
				app.AppConfig.ProvAppAgents = prevAgents
				return fmt.Errorf("agent change rejected: %w", err)
			}
		} else {
			// Legacy ("") and Manual: just update agents, no credit recalculation
			app.AppConfig.ProvAppAgents = value
		}
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
		effectiveMode := app.effectiveSizingMode()
		if effectiveMode == config.AppSizingModeManual {
			return errors.New("prov-app-credit-planned cannot be set in manual mode; use CPU/memory/disk controls instead")
		}
		creditPlanSize, err := strconv.Atoi(value)
		if err != nil {
			return errors.New("invalid credit planned value: " + value)
		}
		if creditPlanSize < 1 {
			return errors.New("credit planned must be greater than or equal to 1")
		}
		if effectiveMode == "" {
			if err := app.SetAppProvisionByLegacyCredit(creditPlanSize); err != nil {
				return err
			}
		} else {
			if err := app.SetAppProvisionByCredit(creditPlanSize); err != nil {
				return err
			}
		}
	case "prov-app-sizing-mode":
		if value == "" {
			prevAppMode := app.AppConfig.ProvAppSizingMode
			oldMode := app.effectiveSizingMode()
			app.AppConfig.ProvAppSizingMode = ""
			newMode := app.effectiveSizingMode()
			if newMode == oldMode {
				return nil
			}
			if newMode == config.AppSizingModeUnit {
				numAgents := len(app.GetAppAgents())
				if numAgents == 0 {
					app.AppConfig.ProvAppSizingMode = prevAppMode
					return errors.New("cannot inherit unit mode: no agents configured")
				}
				appUnit := app.deriveUnitFromStoredResources()
				creditPlanSize := appUnit * numAgents
				app.AppConfig.ProvAppCreditPlanned = creditPlanSize
				app.AppConfig.ProvAppCpuCores = strconv.Itoa(appUnit * config.AppUnitCpuCores)
				app.AppConfig.ProvAppMem = strconv.Itoa(appUnit * config.AppUnitMemMB)
				app.AppConfig.ProvAppDisk = strconv.Itoa(appUnit * config.AppUnitDiskGB)
				app.SetReprovCookie()
				app.ClusterGroup.recomputeAppCredits()
			}
			return nil
		}
		if value != config.AppSizingModeUnit && value != config.AppSizingModeManual {
			return errors.New("prov-app-sizing-mode must be 'unit' or 'manual'")
		}
		prevAppMode := app.AppConfig.ProvAppSizingMode
		oldMode := app.effectiveSizingMode()
		app.AppConfig.ProvAppSizingMode = value
		// When switching any non-unit mode (legacy "" or manual) → unit:
		// derive best-fit units from current resources and force-apply resource formula.
		// We do not call SetAppProvisionByCredit here because its early-exit on matching
		// credit count would leave resources at old values if the derived credit count
		// happens to equal the stored planned credits.
		if value == config.AppSizingModeUnit && oldMode != config.AppSizingModeUnit {
			numAgents := len(app.GetAppAgents())
			if numAgents == 0 {
				app.AppConfig.ProvAppSizingMode = prevAppMode
				return errors.New("cannot switch to unit mode: no agents configured")
			}
			var cores int
			if app.AppConfig.ProvAppCpuCores != "" {
				var parseErr error
				cores, parseErr = strconv.Atoi(app.AppConfig.ProvAppCpuCores)
				if parseErr != nil {
					app.AppConfig.ProvAppSizingMode = prevAppMode
					return fmt.Errorf("cannot switch to unit mode: unparseable cpu-cores %q: %w", app.AppConfig.ProvAppCpuCores, parseErr)
				}
			}
			var memMB int
			if app.AppConfig.ProvAppMem != "" {
				var parseErr error
				memMB, parseErr = config.ParseUnitMeasurementToInt("M", app.AppConfig.ProvAppMem, false)
				if parseErr != nil {
					app.AppConfig.ProvAppSizingMode = prevAppMode
					return fmt.Errorf("cannot switch to unit mode: unparseable memory %q: %w", app.AppConfig.ProvAppMem, parseErr)
				}
			}
			var diskGB int
			if app.AppConfig.ProvAppDisk != "" {
				var parseErr error
				diskGB, parseErr = config.ParseUnitMeasurementToInt("G", app.AppConfig.ProvAppDisk, false)
				if parseErr != nil {
					app.AppConfig.ProvAppSizingMode = prevAppMode
					return fmt.Errorf("cannot switch to unit mode: unparseable disk %q: %w", app.AppConfig.ProvAppDisk, parseErr)
				}
			}
			unitFromCores, unitFromMem, unitFromDisk := 1, 1, 1
			if cores > config.AppUnitCpuCores {
				unitFromCores = (cores + config.AppUnitCpuCores - 1) / config.AppUnitCpuCores
			}
			if memMB > config.AppUnitMemMB {
				unitFromMem = (memMB + config.AppUnitMemMB - 1) / config.AppUnitMemMB
			}
			if diskGB > config.AppUnitDiskGB {
				unitFromDisk = (diskGB + config.AppUnitDiskGB - 1) / config.AppUnitDiskGB
			}
			appUnit := unitFromCores
			if unitFromMem > appUnit {
				appUnit = unitFromMem
			}
			if unitFromDisk > appUnit {
				appUnit = unitFromDisk
			}
			creditPlanSize := appUnit * numAgents
			app.AppConfig.ProvAppCreditPlanned = creditPlanSize
			app.AppConfig.ProvAppCpuCores = strconv.Itoa(appUnit * config.AppUnitCpuCores)
			app.AppConfig.ProvAppMem = strconv.Itoa(appUnit * config.AppUnitMemMB)
			app.AppConfig.ProvAppDisk = strconv.Itoa(appUnit * config.AppUnitDiskGB)
			app.SetReprovCookie()
		}
	case "prov-app-ha-topology":
		app.AppConfig.ProvAppHATopology = value
	case "prov-app-cpu-cores":
		app.AppConfig.ProvAppCpuCores = value
		if app.effectiveSizingMode() == config.AppSizingModeManual {
			app.SetReprovCookie()
		}
	case "prov-app-memory":
		app.AppConfig.ProvAppMem = value
		if app.effectiveSizingMode() == config.AppSizingModeManual {
			app.SetReprovCookie()
		}
	case "prov-app-disk-size":
		app.AppConfig.ProvAppDisk = value
		if app.effectiveSizingMode() == config.AppSizingModeManual {
			app.SetReprovCookie()
		}
	case "prov-app-disk-iops":
		app.AppConfig.ProvAppDiskIops = value
		if app.effectiveSizingMode() == config.AppSizingModeManual {
			app.SetReprovCookie()
		}
	default:
		return errors.New("unknown setting: " + key)
	}

	app.ClusterGroup.recomputeAppCredits()
	return nil
}

func (app *App) SwitchSetting(key string) error {
	switch key {
	default:
		return errors.New("unknown setting: " + key)
	}

}

func (app *App) SetMaintenance(maintenance bool) {
	if maintenance {
		app.State = stateMaintenance
	} else {
		app.State = stateAppRunning
	}
}

func (app *App) SetDefaultRoute(cloud18Domain, cloud18SubDomain, cloud18SubDomainZone, clusterName string) error {
	if len(app.AppConfig.Deployment.Routes) > 0 {
		return nil
	}
	app.AppConfig.Deployment.Routes = []config.Route{
		{
			CName:    app.Name + "." + clusterName + "." + cloud18SubDomain + "-" + cloud18SubDomainZone + "." + cloud18Domain + ".cloud18.io",
			Port:     app.AppConfig.AppPort,
			Protocol: "https",
		},
	}
	app.AppConfig.Deployment.NormalizeRoutes()
	if err := app.AppConfig.Deployment.ValidateRoutes(); err != nil {
		app.AppConfig.Deployment.Routes = nil
		return fmt.Errorf("default route for app %s is invalid: %w", app.Name, err)
	}
	return nil
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

func (app *App) SetAppProvisionByCredit(creditPlanSize int) error {

	if creditPlanSize == app.AppConfig.ProvAppCreditPlanned {
		return nil
	}

	numAgents := len(app.GetAppAgents())

	if numAgents == 0 {
		return errors.New("no agents available for flex provisioning")
	}
	if creditPlanSize%numAgents != 0 {
		return fmt.Errorf("credit planned (%d) must be a multiple of the number of agents (%d)", creditPlanSize, numAgents)
	}

	app.AppConfig.ProvAppCreditPlanned = creditPlanSize

	// Unit mode only: apply the App Unit resource formula and trigger reprovision.
	// Legacy and manual modes are dispatched to their own helpers before this function
	// is called, so reaching here in a non-unit mode is a no-op.
	if app.effectiveSizingMode() == config.AppSizingModeUnit {
		unitsPerAgent := creditPlanSize / numAgents
		app.AppConfig.ProvAppCpuCores = strconv.Itoa(unitsPerAgent * config.AppUnitCpuCores)
		app.AppConfig.ProvAppMem = strconv.Itoa(unitsPerAgent * config.AppUnitMemMB)
		app.AppConfig.ProvAppDisk = strconv.Itoa(unitsPerAgent * config.AppUnitDiskGB)
		app.SetReprovCookie()
	}

	return nil
}

// SetAppProvisionByLegacyCredit is the exact origin/develop SetAppProvisionByCredit
// logic preserved for legacy-mode apps (ProvAppSizingMode == ""). It uses only the
// app-level cluster defaults (ProvAppCpuCores / ProvAppMem / ProvAppDisk) with no
// fallback to the DB-level Prov* fields, matching pre-split behavior exactly.
func (app *App) SetAppProvisionByLegacyCredit(creditPlanSize int) error {
	if creditPlanSize == app.AppConfig.ProvAppCreditPlanned {
		return nil
	}

	numAgents := len(app.GetAppAgents())
	if numAgents == 0 {
		return errors.New("no agents available for flex provisioning")
	}
	if creditPlanSize%numAgents != 0 {
		return errors.New("credit planned must be a multiple of the number of agents for flex provisioning")
	}

	provCredit := creditPlanSize / numAgents

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

	app.SetReprovCookie()

	return nil
}

func (app *App) ApplyPlannedCredits() {
	if app.AppConfig.ProvAppCreditPlanned != app.AppConfig.ProvAppCreditUsed {
		app.AppConfig.ProvAppCreditUsed = app.AppConfig.ProvAppCreditPlanned
	}
}

func (app *App) SetRouteStatuses(routeStatuses []config.RouteStatus) {
	app.Lock()
	defer app.Unlock()
	app.RouteStatus = routeStatuses
}
