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
	"time"

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

// SetPrevState is locked (app.Lock()) because it, like SetState, is read
// cross-goroutine by GetAppAPIView() (cluster/app_get.go) while
// maybeRefreshAppsAsync's worker concurrently calls it during Refresh().
func (app *App) SetPrevState(state string) {
	app.Lock()
	defer app.Unlock()
	app.PrevState = state
}

// SetRefreshInProgress marks whether this app's Refresh() is currently
// running, under app.Lock() -- meant to be read from a different goroutine
// than the one that writes it (any status/API reader vs. the
// maybeRefreshAppsAsync worker calling Refresh()), so it needs to actually
// be safe to do so.
func (app *App) SetRefreshInProgress(v bool) {
	app.Lock()
	defer app.Unlock()
	app.RefreshInProgress = v
}

// SetRefreshResult records one Refresh() call's timing/outcome under
// app.Lock() -- see SetRefreshInProgress. err is whatever internal error
// Refresh() captured along the way (currently just
// GetAppsSubstitutionJSon's); Refresh() itself still always returns nil to
// its caller, unchanged, so this is purely additive observability.
func (app *App) SetRefreshResult(start, end time.Time, err error) {
	app.Lock()
	defer app.Unlock()
	app.LastRefreshStart = start
	app.LastRefreshEnd = end
	app.LastRefreshDurationMs = end.Sub(start).Milliseconds()
	if err != nil {
		app.LastRefreshError = err.Error()
	} else {
		app.LastRefreshError = ""
	}
}

func (app *App) SetSuspect() {
	app.State = stateSuspect
}

// SetFailCount is locked (app.Lock()) -- see SetPrevState.
func (app *App) SetFailCount(c int) {
	app.Lock()
	defer app.Unlock()
	app.FailCount = c
}

// SetWarnCount is locked (app.Lock()) -- see SetPrevState.
func (app *App) SetWarnCount(c int) {
	app.Lock()
	defer app.Unlock()
	app.WarnCount = c
}

func (app *App) SetCredential(credential string) {
	app.User, app.Pass = misc.SplitPair(credential)
}

// SetState is locked (app.Lock()) -- see SetPrevState.
func (app *App) SetState(v string) {
	app.Lock()
	defer app.Unlock()
	app.State = v
}

// CommitStateTransition is locked (app.Lock()) -- see SetPrevState. It reads
// PrevState and State as one atomic pair and, only when they differ, advances
// PrevState to State, returning (old, new, true). When they already match it
// returns (State, State, false) without mutating anything. Refresh() uses
// this instead of separately comparing app.PrevState != app.State and then
// calling SetState()/SetPrevState() -- two locked calls a concurrent
// Refresh() on the same App (e.g. via BackendsStateChange(), which bypasses
// the maybeRefreshAppsAsync single-flight guarantee) could interleave with,
// causing a spurious or missed transition log/alert.
func (app *App) CommitStateTransition() (oldState, newState string, changed bool) {
	app.Lock()
	defer app.Unlock()
	oldState, newState = app.PrevState, app.State
	if oldState != newState {
		app.PrevState = newState
		return oldState, newState, true
	}
	return oldState, newState, false
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
	unitCores, unitMemMB, unitDiskGB := app.unitRatioInts()
	unitFromCores, unitFromMem, unitFromDisk := 1, 1, 1
	if unitCores > 0 && cores > unitCores {
		unitFromCores = (cores + unitCores - 1) / unitCores
	}
	if unitMemMB > 0 && memMB > unitMemMB {
		unitFromMem = (memMB + unitMemMB - 1) / unitMemMB
	}
	if unitDiskGB > 0 && diskGB > unitDiskGB {
		unitFromDisk = (diskGB + unitDiskGB - 1) / unitDiskGB
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
	case "prov-app-start-timeout":
		if value != "" {
			if _, err := time.ParseDuration(value); err != nil {
				return fmt.Errorf("prov-app-start-timeout %q: a duration such as 10m or 1h is expected", value)
			}
		}
		app.AppConfig.ProvAppStartTimeout = value
	case "prov-app-configurator":
		if value != "" {
			if _, err := loadAppConfiguratorModule(value); err != nil {
				return err
			}
		}
		app.AppConfig.ProvAppConfigurator = value
	case "prov-app-agents":
		// The shape (prov-app-cpu-cores/memory/disk) is PER INSTANCE and never depends on
		// the agent count; the instances follow the topology (flex = agents, failover = 1).
		app.AppConfig.ProvAppAgents = value
	case "prov-app-template":
		app.AppConfig.ProvAppTemplate = value
	case "app-port":
		app.AppConfig.AppPort = value
	case "app-db-user":
		app.AppConfig.AppDbUser = value
	case "app-db-pass":
		// stored encrypted; on an owned database the user password is rotated at once (#1870)
		app.AppConfig.AppDbPass = app.ClusterGroup.Conf.GetEncryptedString(app.ClusterGroup.Conf.GetDecryptedPassword("app-db-pass", value))
		if err := app.ClusterGroup.RotateAppDatabasePassword(app); err != nil {
			return err
		}
	case "app-random-password":
		// stored encrypted; the app's containers read it at their next provisioning
		app.AppConfig.AppRandomPassword = app.ClusterGroup.Conf.GetEncryptedString(app.ClusterGroup.Conf.GetDecryptedPassword("app-random-password", value))
	case "app-db-auto-create":
		app.AppConfig.AppDbAutoCreate = value == "true" || value == "1" || value == "on"
		if err := app.ClusterGroup.ApplyAppDbDefaults(app.AppConfig); err != nil {
			return err
		}
	case "app-db-owned":
		// the ownership mark: an operator asserts the schema and user belong to this app
		app.AppConfig.AppDbOwned = value == "true" || value == "1" || value == "on"
	case "app-db-schema":
		app.AppConfig.AppDbSchema = value
	case "prov-app-units":
		if app.ClusterGroup != nil && app.ClusterGroup.engineServerOfApp(app) != nil {
			// a monitored server is sized by the database plan: resize it there
			return errors.New("this app is a database server of the cluster: resize it with the database settings (prov-db-memory, prov-db-cpu-cores, prov-db-disk-size), not with app units")
		}
		// The unit sizing HELPER, not a store (Stéphane 2026-09-29: the unit count is derived,
		// tracked in graphite, never a field): N whole units per instance -> the three declared
		// prov-app-* values at the manager's ratio of the app's profile (Compute, or Database
		// when app-stateful). Manual mode keeps the hand-typed shape.
		if app.effectiveSizingMode() == config.AppSizingModeManual {
			return errors.New("prov-app-units cannot be set in manual mode; use CPU/memory/disk controls instead")
		}
		units, err := strconv.Atoi(value)
		if err != nil || units < 1 {
			return errors.New("invalid units value: " + value + " (whole number >= 1)")
		}
		app.applyUnitShape(units)
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
				app.applyUnitShape(app.deriveUnitFromStoredResources())
			}
			return nil
		}
		if value != config.AppSizingModeUnit && value != config.AppSizingModeManual {
			return errors.New("prov-app-sizing-mode must be 'unit' or 'manual'")
		}
		prevAppMode := app.AppConfig.ProvAppSizingMode
		oldMode := app.effectiveSizingMode()
		app.AppConfig.ProvAppSizingMode = value
		// When switching any non-unit mode (legacy "" or manual) -> unit: derive the
		// best-fit whole units from the current shape and snap the shape onto the unit
		// grid (applyUnitShape), so a unit-managed app always sits on whole units.
		if value == config.AppSizingModeUnit && oldMode != config.AppSizingModeUnit {
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
			unitCores, unitMemMB, unitDiskGB := app.ClusterGroup.computeRatioInts()
			unitFromCores, unitFromMem, unitFromDisk := 1, 1, 1
			if unitCores > 0 && cores > unitCores {
				unitFromCores = (cores + unitCores - 1) / unitCores
			}
			if unitMemMB > 0 && memMB > unitMemMB {
				unitFromMem = (memMB + unitMemMB - 1) / unitMemMB
			}
			if unitDiskGB > 0 && diskGB > unitDiskGB {
				unitFromDisk = (diskGB + unitDiskGB - 1) / unitDiskGB
			}
			appUnit := unitFromCores
			if unitFromMem > appUnit {
				appUnit = unitFromMem
			}
			if unitFromDisk > appUnit {
				appUnit = unitFromDisk
			}
			app.applyUnitShape(appUnit)
		}
	case "prov-app-ha-topology":
		app.AppConfig.ProvAppHATopology = value
	case "app-monitor-mode":
		// How an app without a route is probed: port (TCP connect to app-port) or ping
		// (ICMP echo to the host, for a process that listens on nothing, #1919).
		mode := strings.ToLower(strings.TrimSpace(value))
		if mode != "" && mode != "port" && mode != "ping" {
			return fmt.Errorf("app-monitor-mode: %q is not port or ping", value)
		}
		app.AppConfig.AppMonitorMode = mode
	case "app-stateful":
		// Stateful app (minio and the like): accounted as DBU, not APU. The plan and the
		// billing re-project on the spot; the sensor's next push lands on the DBU track.
		app.AppConfig.AppStateful = value == "true" || value == "1" || value == "on"
		app.ClusterGroup.RefreshComputePlanAPU()
	case "app-s3-provider":
		// The storage profile: the app hosts an archive for others (minio). Its volume is
		// billed as producer BAU, not BKU, and it stays a compute unit for its cores/memory.
		app.AppConfig.AppS3Provider = value == "true" || value == "1" || value == "on"
		app.ClusterGroup.refreshAppS3Providers()
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
	return nil
}

func (app *App) SwitchSetting(key string) error {
	switch key {
	case "app-db-auto-create":
		if app.AppConfig.AppDbAutoCreate {
			return app.SetSetting(key, "false")
		}
		return app.SetSetting(key, "true")
	case "app-db-owned":
		if app.AppConfig.AppDbOwned {
			return app.SetSetting(key, "false")
		}
		return app.SetSetting(key, "true")
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

// applyUnitShape writes the declared shape of ONE instance from a whole unit count at the
// manager's ratio of the app's profile (Database when app-stateful, else Compute), and
// arms a reprovision. The count itself is not stored: it is re-derived from the shape
// (deriveUnitFromStoredResources) and tracked as the app's plan series in graphite.
func (app *App) applyUnitShape(units int) {
	if units < 1 {
		units = 1
	}
	unitCores, unitMemMB, unitDiskGB := app.unitRatioInts()
	app.AppConfig.ProvAppCpuCores = strconv.Itoa(units * unitCores)
	app.AppConfig.ProvAppMem = strconv.Itoa(units * unitMemMB)
	app.AppConfig.ProvAppDisk = strconv.Itoa(units * unitDiskGB)
	app.SetReprovCookie()
}

// unitRatioInts is the whole cores / MB / GB per unit for THIS app: the Database ratio
// for a stateful app (DBU), the Compute ratio otherwise (APU).
func (app *App) unitRatioInts() (cores, memMB, diskGB int) {
	if app.AppConfig != nil && app.AppConfig.AppStateful {
		c, m := app.ClusterGroup.dbuRatioInts()
		d := 0
		if app.ClusterGroup.resources != nil {
			d = int(app.ClusterGroup.resources.Ratios(ProfileDatabase).DiskGBPerUnit + 0.5)
		}
		if d <= 0 {
			d = int(mustRatio(DefaultRatioDBU).DiskGBPerUnit + 0.5)
		}
		return c, m, d
	}
	return app.ClusterGroup.computeRatioInts()
}

func (app *App) SetRouteStatuses(routeStatuses []config.RouteStatus) {
	app.Lock()
	defer app.Unlock()
	app.RouteStatus = routeStatuses
}
