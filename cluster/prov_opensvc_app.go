// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/misc"
	"github.com/signal18/replication-manager/utils/state"
	ini "gopkg.in/ini.v1"
)

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

func (cluster *Cluster) OpenSVCUnprovisionAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	var opErr error
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		node, err := cluster.OpenSVCFoundAppAgent(app)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't find app agent %s, %s", app.GetId(), err)
			opErr = errors.Join(opErr, err)
		} else {
			for _, service := range node.Svc {
				if app.GetServiceName() == service.Svc_name {
					idaction, err := svc.UnprovisionService(node.Node_id, service.Svc_id)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't queue unprovision app %s, %s", app.GetId(), err)
						opErr = errors.Join(opErr, err)
						continue
					}

					err = cluster.OpenSVCWaitDequeue(svc, idaction)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't unprovision app %s, %s", app.GetId(), err)
						opErr = errors.Join(opErr, err)
					}
				}
			}
		}
	} else if svc.IsV3() {
		err := svc.PurgeServiceV3(cluster.Name, app.GetServiceName())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app service:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
		for _, volumename := range app.GetVolumes(true) {
			err = svc.PurgeServiceV3(cluster.Name, cluster.Name+"/vol/"+volumename)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app volume:  %s ", err)
				opErr = errors.Join(opErr, err)
			}
		}
	} else {
		err := svc.PurgeServiceV2(cluster.GetName(), app.GetServiceName(), app.GetAgent())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app service:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
		for _, volumename := range app.GetVolumes(true) {
			err = svc.PurgeServiceV2(cluster.Name, cluster.Name+"/vol/"+volumename, app.GetAgent())
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision app volume:  %s ", err)
				opErr = errors.Join(opErr, err)
			}
		}
	}

	return opErr
}

func (cluster *Cluster) OpenSVCStopAppService(app *App, node string) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + app.GetName())
		if err != nil {
			return err
		}

		if node != "" {
			agents, err := cluster.FoundAllAppAgents(app)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not find app agents:  %s ", err)
				return err
			}
			for _, agent := range agents {
				if strings.ToUpper(node) == "ALL" || strings.EqualFold(node, agent.Node_name) {
					_, err := svc.StopService(agent.Node_id, service.Svc_id)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app in %s:  %s ", agent.Node_name, err)
						return err
					}
				}
			}
		} else {
			agent, err := cluster.OpenSVCFoundAppAgent(app)
			if err != nil {
				return err
			}
			svc.StopService(agent.Node_id, service.Svc_id)
		}
	} else if svc.IsV3() {
		err := svc.StopServiceV3(cluster.Name, app.GetServiceName())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
			return err
		}
	} else {
		if strings.ToUpper(node) == "ALL" {
			err := svc.StopServiceV2(cluster.Name, app.GetServiceName(), "*")
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
				return err
			}
		} else {
			agentName := app.GetAgent()
			if node != "" {
				agentName = node
			}
			err := svc.StopServiceV2(cluster.Name, app.GetServiceName(), agentName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop app:  %s ", err)
				return err
			}
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartAppService(app *App, node string) error {
	svc := cluster.OpenSVCConnect()
	if svc.IsV3() {
		err := svc.StartServiceV3(cluster.Name, app.GetServiceName())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start app:  %s ", err)
			return err
		}
		return nil
	}
	agent := app.GetAgent()
	if strings.ToUpper(node) == "ALL" {
		nodes := strings.Split(app.GetAppConfig().ProvAppAgents, ",")
		for _, n := range nodes {
			err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), n)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start app:  %s ", err)
				return err
			}
		}
	} else {
		if node != "" {
			agent = node
		}
		err := svc.StartServiceV2(cluster.Name, app.GetServiceName(), agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start app:  %s ", err)
			return err
		}
		return nil
	}
	return nil
}

func (cluster *Cluster) OpenSVCRestartAppService(app *App, node string, rid string) error {
	if err := ValidateAppRestartRid(rid); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "App restart validation failed: %s", err)
		return err
	}

	svc := cluster.OpenSVCConnect()
	if svc.IsV3() {
		if rid != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "RID restart is not supported in OpenSVC v3, falling back to full service restart")
		}
		err := svc.RestartServiceV3(cluster.Name, app.GetServiceName())
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart app:  %s ", err)
			return err
		}
		return nil
	}
	agent := app.GetAgent()
	if strings.ToUpper(node) == "ALL" || node == "*" {
		err := svc.RestartServiceV2(cluster.Name, app.GetServiceName(), "*", rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart app:  %s ", err)
			return err
		}
	} else {
		if node != "" {
			agent = node
		}
		err := svc.RestartServiceV2(cluster.Name, app.GetServiceName(), agent, rid)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart app:  %s ", err)
			return err
		}
		return nil
	}
	return nil
}

func (cluster *Cluster) OpenSVCAbortAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI || !svc.IsV3() {
		err := ErrOpenSVCAbortNotSupported
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not abort app: %s", err)
		return err
	}

	err := svc.AbortServiceV3(cluster.Name, app.GetServiceName())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not abort app: %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) OpenSVCClearAppInstanceState(app *App, node string) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI || !svc.IsV3() {
		err := ErrOpenSVCClearNotSupported
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not clear app instance state: %s", err)
		return err
	}

	agent := app.GetAgent()
	if node != "" {
		agent = node
	}

	err := svc.ClearInstanceV3(agent, app.GetServiceName())
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not clear app instance state: %s", err)
		return err
	}

	return nil
}

// validateAppVolumeSizes checks that every non-blank volume Size in the app's
// deployment is a valid size string.  It validates without mutating or logging
// so it is safe to call from any provision entrypoint.  Returns the first
// invalid entry as an error that names the app, volume, and raw size.
func validateAppVolumeSizes(app *App) error {
	if app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return nil
	}
	appcnf := app.AppConfig
	for _, vol := range appcnf.Deployment.Storages.Volumes {
		if vol.Size == "" {
			continue
		}
		if _, err := config.NormalizeVolumeSize(vol.Size); err != nil {
			return fmt.Errorf("app %q volume %q has invalid persisted size %q: fix the TOML and reload before provisioning", appcnf.AppHost, vol.Name, vol.Size)
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCProvisionAppV3(app *App, svc opensvc.Collector, agent opensvc.Host) error {
	if err := validateAppVolumeSizes(app); err != nil {
		return err
	}

	err := cluster.OpenSVCCreateAppVariableMaps(agent.Node_name, app)
	if err != nil {
		return err
	}

	res, err := cluster.OpenSVCGetAppTemplateV3(app)
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", res)

	templatefile := filepath.Join(app.Datadir, "opensvc_template.ini")
	if err := os.WriteFile(templatefile, res, 0600); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "Failed to write template file %s: %s", templatefile, err)
	}

	app.TemplateMD5Prov = misc.GetMD5HashFromBytes(res)
	app.TemplateMD5 = app.TemplateMD5Prov

	body, err := svc.CreateTemplateV3(cluster.Name, app.ServiceName, app.Agent, res)
	if err != nil {
		if !isOpenSVCAlreadyExists(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision app:  %s ", err)
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "App template already exists, reusing existing template: %s", app.ServiceName)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Template created with response: %s", body)
	}

	delay := normalizeOpenSVCV3ProvisionDelay(cluster.Conf.ProvOpensvcV3ProvisionDelay)
	time.Sleep(time.Duration(delay) * time.Second)

	return svc.ProvisionServiceV3(cluster.Name, app.ServiceName)
}

func (cluster *Cluster) OpenSVCProvisionAppService(app *App) error {
	svc := cluster.OpenSVCConnect()
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Provisioning app %s on OpenSVC", app.GetId())
	agent, err := cluster.OpenSVCFoundAppAgent(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not find app agent:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Found app agent %s. Creating maps", agent.Node_name)

	if !cluster.Conf.ProvOpensvcUseCollectorAPI && svc.IsV3() {
		err = cluster.OpenSVCProvisionAppV3(app, svc, agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision app:  %s ", err)
			cluster.errorChan <- err
			return err
		}
		if err = cluster.OpenSVCProvisionRoute(app); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
				"App %s service provisioned but route publication failed: %s — retry via update-routes when DNS and gateway are ready", app.GetId(), err)
		}
		cluster.errorChan <- nil
		return nil
	}

	if err := validateAppVolumeSizes(app); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision app: %s", err)
		cluster.errorChan <- err
		return err
	}

	err = cluster.OpenSVCCreateAppVariableMaps(agent.Node_name, app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create maps:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Getting app template %s for OpenSVC", app.GetId())

	res, err := cluster.OpenSVCGetAppTemplateV2(app)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not get app template:  %s ", err)
		cluster.errorChan <- err
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, "Creating app template %s on OpenSVC", app.GetId())

	templatefile := filepath.Join(app.Datadir, "opensvc_template.json")
	_ = os.WriteFile(templatefile, res, 0644)

	app.TemplateMD5Prov = misc.GetMD5HashFromBytes(res)
	app.TemplateMD5 = app.TemplateMD5Prov

	err = svc.CreateTemplateV2(cluster.Name, app.ServiceName, app.Agent, res)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not create app template:  %s ", err)
		cluster.errorChan <- err
		return err
	}
	if err = cluster.OpenSVCProvisionRoute(app); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
			"App %s service provisioned but route publication failed: %s — retry via update-routes when DNS and gateway are ready", app.GetId(), err)
	}
	cluster.errorChan <- nil
	return nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateSectionMap(app *App) (map[string]map[string]string, error) {
	if app.AppConfig.ProvAppDockerImg == "" {
		return nil, errors.New("App image is not defined in app config")
	}

	deployment := app.AppConfig.Deployment

	containernum := 1
	svcsection := make(map[string]map[string]string)
	svcsection["DEFAULT"] = cluster.OpenSVCGetAppDefaultSection(app)
	svcsection["ip#01"] = cluster.OpenSVCGetNetSection()
	svcsection, err := cluster.OpenSVCGetAppVolumeSections(svcsection, app)
	if err != nil {
		return nil, err
	}
	svcsection[fmt.Sprintf("container#%02d", containernum)] = cluster.OpenSVCGetNamespaceContainerSection()

	for _, gc := range deployment.Storages.GitClones {
		if gc.Volume == nil {
			if vol, err := deployment.GetVolumeByName(gc.VolumeName); err == nil {
				gc.Volume = vol
			} else {
				return nil, fmt.Errorf("Git clone volume %s not found in deployment", gc.VolumeName)
			}
		}
		containernum++
		sectionName := fmt.Sprintf("container#%02dinit%s", containernum, gc.Name)
		svcsection[sectionName] = cluster.OpenSVCGetAppGitInitContainerSection(app, gc)
	}

	for _, s3m := range deployment.Storages.S3Mounts {
		if s3m.Volume == nil {
			if vol, err := deployment.GetVolumeByName(s3m.VolumeName); err == nil {
				s3m.Volume = vol
			} else {
				return nil, fmt.Errorf("S3 mount volume %s not found in deployment", s3m.VolumeName)
			}
		}

		if _, err := cluster.resolveS3MountProvisioningEndpoint(app, s3m); err != nil {
			return nil, err
		}

		containernum++
		sectionName := fmt.Sprintf("container#%02dinit%s", containernum, s3m.Name)
		svcsection[sectionName] = cluster.OpenSVCGetAppS3MountContainerSection(app, s3m)
	}

	svcsection["container#app"] = cluster.OpenSVCGetAppContainerSection(app)
	svcsection["env"] = cluster.OpenSVCGetAppEnvSection(app)

	return svcsection, nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateV2(app *App) ([]byte, error) {
	svcsection, err := cluster.OpenSVCGetAppTemplateSectionMap(app)
	if err != nil {
		return []byte(""), err
	}

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return []byte(""), err
	}

	return svcsectionJson, nil
}

func (cluster *Cluster) OpenSVCGetAppTemplateV3(app *App) ([]byte, error) {
	svcsection, err := cluster.OpenSVCGetAppTemplateSectionMap(app)
	if err != nil {
		return nil, err
	}

	cfg := ini.Empty()

	sectionNames := make([]string, 0, len(svcsection))
	for k := range svcsection {
		sectionNames = append(sectionNames, k)
	}
	sort.Strings(sectionNames)

	for _, sectionName := range sectionNames {
		kv := svcsection[sectionName]
		sec, err := cfg.NewSection(sectionName)
		if err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(kv))
		for k := range kv {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			_, err := sec.NewKey(k, kv[k])
			if err != nil {
				return nil, err
			}
		}
	}

	var buf bytes.Buffer
	_, err = cfg.WriteTo(&buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func (cluster *Cluster) OpenSVCGetAppVolumeSections(basemap map[string]map[string]string, app *App) (map[string]map[string]string, error) {

	appcnf := app.AppConfig
	if appcnf == nil {
		return basemap, nil
	}

	deployment := appcnf.Deployment
	if len(deployment.Storages.Volumes) == 0 {
		return basemap, nil
	}

	deployment.Paths.Sort()
	pathmap := deployment.Paths.GetVolumeDirs()

	poolList, err := cluster.OpenSVCGetPoolInfoListFresh()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch OpenSVC pool list before app volume provisioning: %w", err)
	}
	if len(poolList) == 0 {
		return nil, errors.New("OpenSVC pool list is empty; cannot provision app volumes")
	}

	poolSet := make(map[string]opensvc.PoolInfo, len(poolList))
	for _, pool := range poolList {
		if pool.Name != "" {
			poolSet[pool.Name] = pool
		}
	}

	seq := 1
	for _, vol := range deployment.Storages.Volumes {
		poolInfo, ok := poolSet[vol.PoolName]
		if !ok {
			return nil, fmt.Errorf("OpenSVC pool %q not found in runtime pool list", vol.PoolName)
		}

		basemap["volume#"+strconv.Itoa(seq)] = openSVCAppVolumeSection(vol, pathmap, poolInfo.Shared)
		seq++
	}

	return basemap, nil
}

// openSVCAppVolumeSection builds the OpenSVC "volume#N" section for a single
// saved app volume row. It merges the row's own VolumeDir tokens (split via
// vol.GetVolumeDirs(), one OpenSVC "directories" entry per token) with any
// extra directories derived from deployment.Paths.GetVolumeDirs(), so a
// merged multi-pool row (e.g. VolumeDir = "etc log var data") is emitted as
// separate top-level directories instead of one literal name containing
// spaces.
func openSVCAppVolumeSection(vol *config.Volume, pathmap map[string][]string, shared bool) map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = vol.Name
	svcvol["pool"] = vol.PoolName
	if vol.Size != "" {
		svcvol["size"] = vol.Size + "g"
	} else {
		svcvol["size"] = "{env.size}"
	}
	if shared {
		svcvol["shared"] = "true"
	}

	// Use set to avoid duplicate directories
	directorySet := make(map[string]struct{})

	for _, baseDir := range vol.GetVolumeDirs() {
		directorySet[baseDir] = struct{}{}
	}
	if mappedPaths, ok := pathmap[vol.Name]; ok {
		for _, path := range mappedPaths {
			// if path is not end with "/", get parent directory
			if !strings.HasSuffix(path, "/") {
				path = filepath.Dir(path) + "/"
			}
			directorySet[filepath.Join(path)] = struct{}{}
		}
	}

	// Join unique directory list
	dirs := make([]string, 0, len(directorySet))
	for dir := range directorySet {
		dirs = append(dirs, dir)
	}

	sort.Strings(dirs) // Optional: consistent order
	svcvol["directories"] = strings.Join(dirs, " ")
	return svcvol
}

func (cluster *Cluster) OpenSVCFoundAppAgent(app *App) (opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	svc.ProvAppAgents = cluster.GetAppAgents(app.AppConfig)

	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvAppAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		return agent, errors.New("Indice not found in apps agent list")
	}
	for i, srv := range cluster.Apps {
		if srv.GetId() == app.GetId() {
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in apps agent list")
}

func (cluster *Cluster) FoundAllAppAgents(app *App) ([]opensvc.Host, error) {
	svc := cluster.OpenSVCConnect()
	svc.ProvAppAgents = cluster.GetAppAgents(app.AppConfig)

	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	var clusteragents []opensvc.Host
	for _, node := range agents {
		if strings.Contains(svc.ProvAppAgents, node.Node_name) {
			clusteragents = append(clusteragents, node)
		}
	}
	if len(clusteragents) == 0 {
		return nil, errors.New("Indice not found in apps agent list")
	}

	return clusteragents, nil
}

func (cluster *Cluster) OpenSVCGetAppEnvSection(app *App) map[string]string {
	appcnf := app.AppConfig
	svcenv := make(map[string]string)

	// Cluster values section
	svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
	svcenv["mrm_api_addr"] = cluster.Conf.MonitorAddress + ":" + cluster.Conf.HttpPort
	svcenv["mrm_cluster_name"] = cluster.GetClusterName()

	svcenv["nodes"] = strings.ReplaceAll(cluster.GetAppAgents(appcnf), ",", " ")
	svcenv["size"] = cluster.GetAppDisk(appcnf) + "g"
	svcenv["app_img"] = appcnf.ProvAppDockerImg

	return svcenv
}

func (cluster *Cluster) OpenSVCGetAppDefaultSection(app *App) map[string]string {
	appcnf := app.AppConfig
	svcdefault := make(map[string]string)

	svcdefault["nodes"] = strings.ReplaceAll(cluster.GetAppAgents(appcnf), ",", " ")
	nodes := strings.Split(svcdefault["nodes"], " ")

	if appcnf.ProvAppHATopology == "flex" {
		svcdefault["topology"] = "flex"
		svcdefault["flex_primary"] = nodes[0]
		svcdefault["flex_target"] = strconv.Itoa(len(nodes))
	} else {
		svcdefault["rollback"] = "false"
	}

	svcdefault["orchestrate"] = "ha"
	svcdefault["app"] = cluster.Conf.ProvCodeApp
	return svcdefault
}

func (cluster *Cluster) OpenSVCGetAppContainerSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "{env.app_img}"
		svccontainer["type"] = cluster.Conf.ProvType
		if app.AppConfig.ProvAppHATopology == "failover" {
			svccontainer["shared"] = "true"
		}

		if app.AppConfig.ProvAppDockerCmd != "" {
			svccontainer["run_command"], _ = app.ClusterGroup.ParseAppTemplate(app.AppConfig.ProvAppDockerCmd, app.AppClusterSubstitute)
		}

		if cluster.Conf.ProvDBDockerRunArgsLimit {
			appMemMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.GetAppMemory(app.AppConfig), true)
			appMemStr := strconv.Itoa(appMemMB) + "m"
			svccontainer["run_args"] = svccontainer["run_args"] + " --memory=" + appMemStr + " --memory-swap=" + appMemStr + " --cpus=" + cluster.GetAppCores(app.AppConfig) + ".0"
		}

		svccontainer["volume_mounts"] = cluster.GetOpenSVCDeploymentPathMapping(app)
		svccontainer["configs_environment"] = app.GetOpenSVCDeploymentAppEnv("env")
		svccontainer["secrets_environment"] = app.GetOpenSVCDeploymentAppEnv("secret")
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppGitInitDefaultSection(app *App) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["detach"] = "false"
		svccontainer["type"] = "docker"
		svccontainer["image"] = "alpine/git"
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["start_timeout"] = "300s"
		svccontainer["optional"] = "true"
		svccontainer["entrypoint"] = "/bin/sh"
	}
	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppGitInitContainerSection(app *App, gc *config.GitClone) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer = cluster.OpenSVCGetAppGitInitDefaultSection(app)
		svccontainer["volume_mounts"] = fmt.Sprintf("/etc/localtime:/etc/localtime:ro %s:/bootstrap", gc.GetSourceVolumeName())
		svccontainer["secrets_environment"] = app.GetOpenSVCDeploymentAppEnv(config.VariableTypeSecret)
		svccontainer["configs_environment"] = app.GetOpenSVCDeploymentAppEnv(config.VariableTypeEnv)
		dirname := filepath.Join("/bootstrap", gc.GetSourcePath())

		prefix := gc.GetVariablePrefix()
		branchKey := "$" + prefix + config.GitVarSuffixBranch
		gituser := prefix + config.GitVarSuffixUser
		gitpass := prefix + config.GitVarSuffixPass
		gitURL := prefix + config.GitVarSuffixRepo

		urlString := "$" + gitURL
		if cluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass) != "" {
			if strings.Contains(gc.GitRepo, "github.com") {
				urlString = fmt.Sprintf("$%s@$%s", gitpass, gitURL)
			} else {
				urlString = fmt.Sprintf("$%s:$%s@$%s", gituser, gitpass, gitURL)
			}
		}

		svccontainer["command"] = "-c 'rm -rf " + dirname + ";mkdir " + dirname + ";git clone -b " + branchKey + " https://" + urlString + " " + dirname + "'"
	}

	return svccontainer
}

func (cluster *Cluster) OpenSVCGetAppS3MountContainerSection(app *App, s3m *config.S3Mount) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["type"] = "docker"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "signal18/nfsmixr:latest"
		svccontainer["netns"] = "container#01"
		svccontainer["privileged"] = "true"
		svccontainer["secrets_environment"] = app.GetOpenSVCDeploymentAppEnv(config.VariableTypeSecret)
		svccontainer["configs_environment"] = app.GetOpenSVCDeploymentAppEnv(config.VariableTypeEnv)
		diskname := s3m.GetSourceVolumeName()
		svccontainer["volume_mounts"] = fmt.Sprintf("%s:/mnt:rw,rshared", filepath.Join(diskname, s3m.VolumeDir))
		svccontainer["run_command"] = "-o allow_other -o nonempty --use-content-type --uid 33 --gid 33 -f"
		svccontainer["blocking_post_provision"] = "sleep 3"
		svccontainer["blocking_post_start"] = "sleep 3"
	}
	return svccontainer
}

func (cluster *Cluster) getAppDeploymentVariableValue(app *App, key string) (string, bool) {
	if app == nil || app.AppConfig == nil || app.AppConfig.Deployment == nil {
		return "", false
	}

	for _, variable := range app.AppConfig.Deployment.Variables {
		if variable.Name != key {
			continue
		}

		if variable.Type == config.VariableTypeSecret {
			return cluster.Conf.GetDecryptedPassword(variable.Name, variable.Value), true
		}

		return variable.Value, true
	}

	return "", false
}

func (cluster *Cluster) resolveS3MountProvisioningEndpoint(app *App, s3m *config.S3Mount) (string, error) {
	if s3m == nil {
		return "", errors.New("S3 mount is nil")
	}
	// Provider-linked mounts are hydrated server-side (API add/modify paths) so
	// provisioning can safely consume the mount's effective endpoint/credentials.
	// Provisioning never calls provider GET/list APIs and does not depend on
	// credentials being returned to UI.

	if s3m.Endpoint != "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlDbg,
			"S3 mount %s uses custom endpoint %s – skipping sibling-app resolution", s3m.Name, s3m.Endpoint)
		return s3m.Endpoint, nil
	}

	if s3m.Node != nil {
		return "http://" + s3m.Node.GetS3Endpoint(), nil
	}

	prefix := s3m.GetVariablePrefix()
	legacyEndpoint, _ := cluster.getAppDeploymentVariableValue(app, prefix+config.S3VarSuffixEndpoint)
	if legacyEndpoint != "" {
		node, _ := cluster.GetAppByURL(legacyEndpoint)
		if node != nil {
			s3m.Node = node
			return "http://" + s3m.Node.GetS3Endpoint(), nil
		}
	}

	fallbackAccessKey := s3m.AccessKey
	fallbackSecretKey := s3m.SecretKey
	if fallbackAccessKey == "" {
		fallbackAccessKey, _ = cluster.getAppDeploymentVariableValue(app, prefix+config.S3VarSuffixAccessKey)
	}
	if fallbackSecretKey == "" {
		fallbackSecretKey, _ = cluster.getAppDeploymentVariableValue(app, prefix+config.S3VarSuffixSecretKey)
	}

	if legacyEndpoint != "" && fallbackAccessKey != "" && fallbackSecretKey != "" {
		s3m.Endpoint = legacyEndpoint
		s3m.AccessKey = fallbackAccessKey
		s3m.SecretKey = fallbackSecretKey
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"S3 mount %s sibling-app resolution failed; reusing legacy fallback credentials", s3m.Name)
		return legacyEndpoint, nil
	}

	return "", fmt.Errorf(
		"S3 mount %s: sibling-app unresolved and no fallback credentials available (endpoint=%q, access_key=%t, secret_key=%t)",
		s3m.Name,
		legacyEndpoint,
		fallbackAccessKey != "",
		fallbackSecretKey != "",
	)
}

func (cluster *Cluster) GetOpenSVCDeploymentPathMapping(app *App) string {
	var results []string
	appcnf := app.GetAppConfig()
	if appcnf == nil {
		return ""
	}

	deployment := appcnf.Deployment
	deployment.SortPaths()
	if len(deployment.Paths) == 0 {
		return ""
	}

	for _, path := range deployment.Paths {
		if path.SourceType != "" && path.SourceName != "" && path.VolumeName == "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Skipping unresolved deployment path mapping dockerpath=%q name=%q parentname=%q srctype=%q srcname=%q srcpath=%q volumename=%q",
				path.DockerPath, path.Name, path.ParentName, path.SourceType, path.SourceName, path.SourcePath, path.VolumeName)
			continue
		}

		vol, err := deployment.GetVolumeByName(path.VolumeName)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
				"Skipping deployment path mapping dockerpath=%q name=%q volumename=%q srctype=%q srcname=%q: %v",
				path.DockerPath, path.Name, path.VolumeName, path.SourceType, path.SourceName, err)
			continue
		}

		results = append(results, path.GetDockerMapping(vol.Name))
	}

	return strings.Join(results, " ")
}

func (cluster *Cluster) OpenSVCCreateAppVariableMaps(agent string, app *App) error {
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		return errors.New("No support of Maps in Collector API")
	}

	svc := cluster.OpenSVCConnect()

	err := svc.CreateSecret(cluster.Name, app.Name, agent)
	if err != nil {
		if isOpenSVCAlreadyExists(err) && cluster.Conf.ProvObjectAllowOverwrite {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Overwriting existing secret for app %s without truncation", app.Name)
		} else {
			return err
		}
	}

	err = svc.CreateConfig(cluster.Name, app.Name, agent)
	if err != nil {
		if isOpenSVCAlreadyExists(err) && cluster.Conf.ProvObjectAllowOverwrite {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Overwriting existing config for app %s without truncation", app.Name)
		} else {
			return err
		}
	}

	for _, v := range app.AppConfig.Deployment.Variables {
		if v.Type == "secret" {
			err = svc.CreateSecretKeyValue(cluster.Name, app.Name, v.Name, cluster.Conf.GetDecryptedPassword(v.Name, v.Value))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", v.Name, err)
			}

			for _, cd := range v.Conditional {
				cdname := fmt.Sprintf("%s@%s", v.Name, cd.Agent)
				err = svc.CreateSecretKeyValue(cluster.Name, app.Name, cdname, cluster.Conf.GetDecryptedPassword(cdname, cd.Value))
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add conditional key to secret: %s %s ", cdname, err)
				}
			}
		} else {
			err = svc.CreateConfigKeyValue(cluster.Name, app.Name, v.Name, cluster.Conf.GetDecryptedPassword(v.Name, v.Value))
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", v.Name, err)
			}

			for _, cd := range v.Conditional {
				cdname := fmt.Sprintf("%s@%s", v.Name, cd.Agent)
				err = svc.CreateConfigKeyValue(cluster.Name, app.Name, cdname, cluster.Conf.GetDecryptedPassword(cdname, cd.Value))
				if err != nil {
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add conditional key to config: %s %s ", cdname, err)
				}
			}
		}
	}

	for _, gc := range app.AppConfig.Deployment.Storages.GitClones {
		prefix := gc.GetVariablePrefix()
		envs := gc.GetEnvVariables()
		for k, val := range envs {
			vName := prefix + k

			if k == config.GitVarSuffixRepo {
				if strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://") {
					val = strings.TrimPrefix(val, "http://")
					val = strings.TrimPrefix(val, "https://")
				}
			}
			if k == config.GitVarSuffixBranch {
				if val == "" {
					val = "master"
				}
			}

			err = svc.CreateConfigKeyValue(cluster.Name, app.Name, vName, val)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", vName, err)
			}
		}

		err = svc.CreateSecretKeyValue(cluster.Name, app.Name, prefix+config.GitVarSuffixPass, cluster.Conf.GetDecryptedPassword(gc.Name, gc.GitPass))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", prefix+config.GitVarSuffixPass, err)
		}
	}

	for _, s3m := range app.AppConfig.Deployment.Storages.S3Mounts {
		effectiveEndpoint, err := cluster.resolveS3MountProvisioningEndpoint(app, s3m)
		if err != nil {
			return err
		}

		prefix := s3m.GetVariablePrefix()
		envs := s3m.GetEnvVariables()
		for k, val := range envs {
			vName := prefix + k
			if k == config.S3VarSuffixEndpoint {
				val = effectiveEndpoint
			} else if k == config.S3VarSuffixMountDir {
				if val == "" {
					val = "/mnt"
				}
			} else if k == config.S3VarSuffixRegion {
				if val == "" {
					val = "fr-east-1"
				}
			} else if k == config.S3VarSuffixBucket {
				if val == "" {
					val = app.Name
				}
			}

			err = svc.CreateConfigKeyValue(cluster.Name, app.Name, vName, val)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to config: %s %s ", vName, err)
			}
		}

		err = svc.CreateSecretKeyValue(cluster.Name, app.Name, prefix+config.S3VarSuffixSecretKey, cluster.Conf.GetDecryptedPassword(s3m.Name, s3m.SecretKey))
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not add key to secret: %s %s ", prefix+config.S3VarSuffixSecretKey, err)
		}
	}

	return nil
}

// appRouteFragmentPrefix returns the shared prefix for all HAProxy fragment
// keys owned by this app, matching the origin/develop naming convention.
func appRouteFragmentPrefix(opensvcDNS string) string {
	return "haproxy.cfg.d/" + opensvcDNS + "_"
}

// withdrawGatewayRoutes deletes any HAProxy config fragments previously
// published for app from the shared gateway service.  Used to clean up stale
// fragments when an app is marked as gateway-conflicted so the live gateway
// state matches the blocked publication state.  Best-effort: errors are
// returned but logged by the caller as warnings rather than halting startup.
func (cluster *Cluster) withdrawGatewayRoutes(app *App) error {
	cloud18GatewayServiceConfig := strings.Split(cluster.Conf.Cloud18GatewayService, "/")
	if len(cloud18GatewayServiceConfig) < 3 {
		return nil
	}
	gwNamespace := cloud18GatewayServiceConfig[0]
	gwService := cloud18GatewayServiceConfig[2]

	svc := cluster.OpenSVCConnect()
	opensvcDNS := app.Name + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster
	ownerPrefix := appRouteFragmentPrefix(opensvcDNS)
	staleBranchPrefix := "haproxy.cfg.d/repman_" + cluster.Name + "_" + app.Name + "_"

	existingKeys, err := svc.ListConfigKeys(gwNamespace, gwService)
	if err != nil {
		return fmt.Errorf("cannot list gateway config keys for conflict withdrawal (app %s): %w", app.Name, err)
	}

	var errs []error
	for _, key := range existingKeys {
		if !strings.HasPrefix(key, ownerPrefix) && !strings.HasPrefix(key, staleBranchPrefix) {
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Withdrawing stale gateway fragment key=%s for conflicted app %s", key, app.Name)
		if delErr := svc.DeleteConfigKeyValue(gwNamespace, gwService, key); delErr != nil {
			errs = append(errs, fmt.Errorf("failed to delete fragment key=%s (app %s): %w", key, app.Name, delErr))
		}
	}
	return errors.Join(errs...)
}

// sanitizeRouteNameToken replaces characters that are unsafe in HAProxy object
// names and OpenSVC config keys with '_', preserving HAProxy-safe chars
// ([A-Za-z0-9._:-]) so distinct CNAMEs remain distinct.
func sanitizeRouteNameToken(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == ':' {
			return r
		}
		return '_'
	}, s)
}

func buildRouteObjectName(route config.Route, opensvcDNS string) string {
	if route.Mode == "port" {
		return opensvcDNS + "_" + sanitizeRouteNameToken(route.CName) + "_" + route.SourcePort + "_" + route.DestinationPort
	}
	return opensvcDNS + "_" + route.DestinationPort
}

// normalizeRouteCNAME lowercases a CNAME and trims surrounding whitespace and
// any trailing dot, matching the canonical DNS wire format used in HAProxy rules.
func normalizeRouteCNAME(cname string) string {
	return strings.ToLower(strings.TrimRight(strings.TrimSpace(cname), "."))
}

// buildGroupedHostRouteFragment generates a single HAProxy config fragment for
// all host routes sharing the same destination port.  Multiple CNAMEs produce
// multiple use_backend rules in the shared 'frontend https' block.  All CNAMEs
// are normalized before being emitted.
func buildGroupedHostRouteFragment(routes []config.Route, opensvcDNS string, numBE int) (fragmentKey, haproxyfragment string, err error) {
	if len(routes) == 0 {
		return "", "", errors.New("buildGroupedHostRouteFragment: no routes provided")
	}
	destPort := routes[0].DestinationPort
	backend := opensvcDNS + "_" + destPort
	fragmentKey = "haproxy.cfg.d/" + backend

	var frontendLines strings.Builder
	for _, r := range routes {
		cname := normalizeRouteCNAME(r.CName)
		fmt.Fprintf(&frontendLines, "    use_backend %s if { hdr(host) -i %s }\n", backend, cname)
	}

	haproxyfragment = fmt.Sprintf(`
frontend https
%s
backend %s
    cookie SERVER insert indirect nocache dynamic
    balance roundrobin
    dynamic-cookie-key mysecretphrase
    option http-server-close
    timeout connect 10s
    timeout server 5m
    timeout tunnel 1h
    server-template srv %d %s:%s resolvers cluster check init-addr none
`, frontendLines.String(), backend, numBE, opensvcDNS, destPort)
	return fragmentKey, haproxyfragment, nil
}

// buildRouteFragment generates the HAProxy config fragment and its gateway
// fragment key for the given route.  It does not perform DNS provisioning.
//
// For port routes the listener binds on cname:sourcePort so the gateway
// resolves the CNAME itself; repman does not need to know the VIP.
func buildRouteFragment(route config.Route, opensvcDNS string, numBE int) (fragmentKey, haproxyfragment string, err error) {
	routeName := buildRouteObjectName(route, opensvcDNS)
	fragmentKey = "haproxy.cfg.d/" + routeName

	switch route.Mode {
	case "port":
		frontend := routeName + "_frontend"
		backend := routeName + "_backend"
		if route.Protocol == "tcp" {
			haproxyfragment = fmt.Sprintf(`
frontend %s
    bind %s:%s
    mode tcp
    default_backend %s

backend %s
    mode tcp
    balance leastconn
    default-server inter 5s fastinter 2s downinter 10s fall 3 rise 2
    server-template srv %d %s:%s resolvers cluster check init-addr none
`, frontend, route.CName, route.SourcePort, backend, backend, numBE, opensvcDNS, route.DestinationPort)
		} else {
			haproxyfragment = fmt.Sprintf(`
frontend %s
    bind %s:%s
    mode http
    option forwardfor
    default_backend %s

backend %s
    mode http
    balance leastconn
    option http-server-close
    timeout connect 10s
    timeout server 5m
    timeout tunnel 1h
    default-server inter 5s fastinter 2s downinter 10s fall 3 rise 2
    server-template srv %d %s:%s resolvers cluster check init-addr none
`, frontend, route.CName, route.SourcePort, backend, backend, numBE, opensvcDNS, route.DestinationPort)
		}
	default: // host
		backend := routeName
		haproxyfragment = fmt.Sprintf(`
frontend https
    use_backend %s if { hdr(host) -i %s }

backend %s
    cookie SERVER insert indirect nocache dynamic
    balance roundrobin
    dynamic-cookie-key mysecretphrase
    option http-server-close
    timeout connect 10s
    timeout server 5m
    timeout tunnel 1h
    server-template srv %d %s:%s resolvers cluster check init-addr none
`, backend, route.CName, backend, numBE, opensvcDNS, route.DestinationPort)
	}
	return fragmentKey, haproxyfragment, nil
}

func (cluster *Cluster) OpenSVCProvisionRoute(app *App) error {
	// Block publication for apps with a detected gateway conflict.  Conflicts are
	// set at startup and cleared when the route config is fixed and the conflict
	// check re-runs.  The rest of the cluster (monitoring, replication) is unaffected.
	if app.AppConfig != nil {
		if conflicted, reason := cluster.IsAppGatewayConflicted(app.AppConfig.AppHost, app.AppConfig.AppPort); conflicted {
			return fmt.Errorf("gateway routes for app %q are blocked: %s — fix route config and reload to clear", app.Name, reason)
		}
	}

	agents := app.GetAppAgents()
	svc := cluster.OpenSVCConnect()
	numBE := len(agents)
	if app.AppConfig != nil && app.AppConfig.ProvAppHATopology == "failover" {
		numBE = 1
	}
	if numBE < 1 {
		numBE = 1
	}

	cloud18GatewayServiceConfig := strings.Split(cluster.Conf.Cloud18GatewayService, "/")
	if len(cloud18GatewayServiceConfig) < 3 {
		return fmt.Errorf("invalid Cloud18GatewayService format: %q", cluster.Conf.Cloud18GatewayService)
	}
	gwNamespace := cloud18GatewayServiceConfig[0]
	gwService := cloud18GatewayServiceConfig[2]
	opensvcDNS := app.Name + "." + cluster.Name + ".svc." + cluster.Conf.ProvOrchestratorCluster

	// Step 1: normalize then validate — fail closed before any OpenSVC write.
	app.AppConfig.Deployment.NormalizeRoutes()
	if err := app.AppConfig.Deployment.ValidateRoutes(); err != nil {
		return fmt.Errorf("route validation failed for app %s: %w", app.Name, err)
	}

	// Step 2: gateway-scoped conflict check.
	// Collect normalized route slices from every app in every cluster that
	// shares the same Cloud18GatewayService, excluding this app.
	// Cloud18GatewayService (namespace/type/service) uniquely identifies the
	// shared HAProxy instance regardless of DNS domain aliases.
	thisGateway := strings.ToLower(strings.TrimSpace(cluster.Conf.Cloud18GatewayService))
	if thisGateway != "" {
		var externalRoutes [][]config.Route
		collectOthers := func(cl *Cluster) {
			if strings.ToLower(strings.TrimSpace(cl.Conf.Cloud18GatewayService)) != thisGateway {
				return
			}
			for _, other := range cl.GetAppsCopy() {
				if other == nil || other.AppConfig == nil {
					continue
				}
				if cl.Name == cluster.Name && other.Name == app.Name {
					continue
				}
				// Conflicted apps are blocked from publication and must not
				// be counted as owning gateway address space.
				if conflicted, _ := cl.IsAppGatewayConflicted(other.AppConfig.AppHost, other.AppConfig.AppPort); conflicted {
					continue
				}
				if normalized := config.NormalizedCopy(other.GetDeploymentRoutesSnapshot()); len(normalized) > 0 {
					externalRoutes = append(externalRoutes, normalized)
				}
			}
		}
		// Same-cluster other apps always contribute — they have equal priority.
		collectOthers(cluster)
		// Peer clusters: apply "first wins" — only collect routes from clusters
		// that appear BEFORE this cluster in clusterOrder.  Clusters appearing
		// later cannot preempt an earlier cluster's gateway-listener ownership.
		// Fall back to checking all peers if clusterOrder is not set.
		if len(cluster.clusterOrder) > 0 {
			for _, name := range cluster.clusterOrder {
				if name == cluster.Name {
					break
				}
				if peer, ok := cluster.clusterList[name]; ok {
					collectOthers(peer)
				}
			}
		} else {
			for _, cl := range cluster.clusterList {
				if cl != nil && cl.Name != cluster.Name {
					collectOthers(cl)
				}
			}
		}
		if err := config.CheckGatewayConflicts(app.AppConfig.Deployment.Routes, externalRoutes...); err != nil {
			return fmt.Errorf("gateway conflict for app %q: %w", app.Name, err)
		}
	}

	// Step 2.5: managed host-route DNS stale-record cleanup.
	//
	// Before writing new fragments we reconcile the DNS state so that renamed or
	// dropped managed host-route CNAMEs are removed from the DNS provider.
	//
	// State is persisted in a small JSON file in app.Datadir so that the set of
	// previously provisioned CNAMEs is known across restarts and non-API provision
	// paths (config file edits, reloads, etc.).
	{
		oldManagedCNAMEs, loadErr := loadProvisionedManagedCNAMEs(app)
		if loadErr != nil {
			return fmt.Errorf("cannot load managed CNAME state for app %s: %w — delete %s to reset", app.Name, loadErr, managedCNAMEsFile(app))
		}

		// Compute desired managed CNAMEs from current host routes.
		newManagedCNAMEs := make(map[string]struct{})
		for _, route := range app.AppConfig.Deployment.Routes {
			if route.Mode == "host" {
				if _, managed := cluster.ManagedHostCNAME(route.CName); managed {
					newManagedCNAMEs[strings.ToLower(strings.TrimRight(route.CName, "."))] = struct{}{}
				}
			}
		}

		persisted, reconcileErr := cluster.reconcileStaleManagedCNAMEs(app, oldManagedCNAMEs, newManagedCNAMEs)
		if reconcileErr != nil {
			return reconcileErr
		}

		if err := saveProvisionedManagedCNAMEs(app, persisted); err != nil {
			return fmt.Errorf("cannot save managed CNAME state for app %s: %w", app.Name, err)
		}
	}

	// Step 3: build the desired fragment set, provisioning DNS for host routes.
	desired := make(map[string]string) // fragmentKey → haproxyfragment

	// Group host routes by destination port so that multiple CNAMEs targeting
	// the same port share one fragment with multiple use_backend rules instead
	// of overwriting each other in the desired map.
	hostRoutesByPort := make(map[string][]config.Route)
	var hostPortOrder []string // preserve declaration order for deterministic output
	for _, route := range app.AppConfig.Deployment.Routes {
		if route.Mode == "host" || route.Mode == "" {
			if _, seen := hostRoutesByPort[route.DestinationPort]; !seen {
				hostPortOrder = append(hostPortOrder, route.DestinationPort)
			}
			hostRoutesByPort[route.DestinationPort] = append(hostRoutesByPort[route.DestinationPort], route)
		}
	}

	for _, destPort := range hostPortOrder {
		routes := hostRoutesByPort[destPort]
		for _, route := range routes {
			if err := cluster.provisionHostRouteDNS(route); err != nil {
				return fmt.Errorf("DNS provisioning failed for route %s (app %s): %w", route.CName, app.Name, err)
			}
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModApp, config.LvlWarn,
			"app %s host routes on port %s: fragment adds use_backend rules to the shared 'frontend https' block; ensure the gateway base config defines that frontend or the HAProxy reload will fail",
			app.Name, destPort)
		key, frag, fragErr := buildGroupedHostRouteFragment(routes, opensvcDNS, numBE)
		if fragErr != nil {
			return fmt.Errorf("cannot build host route fragment for app %s port %s: %w", app.Name, destPort, fragErr)
		}
		desired[key] = frag
	}

	// Port routes are per-route (CName+port combo is already unique by validation).
	for _, route := range app.AppConfig.Deployment.Routes {
		if route.Mode == "port" {
			key, frag, fragErr := buildRouteFragment(route, opensvcDNS, numBE)
			if fragErr != nil {
				return fmt.Errorf("cannot build route fragment for app %s: %w", app.Name, fragErr)
			}
			desired[key] = frag
		}
	}

	// Step 4: list existing gateway fragments and delete stale or legacy ones.
	//
	// Two naming schemes may coexist after an upgrade:
	//   desired: haproxy.cfg.d/<opensvcDNS>_<route...>
	//   stale branch scheme: haproxy.cfg.d/repman_<cluster>_<app>_<routeToken>.cfg
	//
	// Both are discovered and removed if no longer in the desired set.
	ownerPrefix := appRouteFragmentPrefix(opensvcDNS)
	staleBranchPrefix := "haproxy.cfg.d/repman_" + cluster.Name + "_" + app.Name + "_"

	existingKeys, listErr := svc.ListConfigKeys(gwNamespace, gwService)
	if listErr != nil {
		return fmt.Errorf("cannot list gateway config keys for stale-fragment cleanup (app %s): %w", app.Name, listErr)
	}

	for _, key := range existingKeys {
		isOwned := strings.HasPrefix(key, ownerPrefix)
		isStaleBranch := strings.HasPrefix(key, staleBranchPrefix)
		if !isOwned && !isStaleBranch {
			continue
		}
		if isOwned {
			if _, stillWanted := desired[key]; stillWanted {
				continue
			}
		}
		// Branch-specific repman_ keys are always stale once we reconcile with
		// the restored origin/develop naming scheme.
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Deleting stale route fragment key=%s (app %s)", key, app.Name)
		if delErr := svc.DeleteConfigKeyValue(gwNamespace, gwService, key); delErr != nil {
			return fmt.Errorf("failed to delete stale fragment key=%s (app %s): %w", key, app.Name, delErr)
		}
	}

	// Step 5: upsert desired fragments.
	for key, frag := range desired {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Upserting route fragment key=%s (app %s)", key, app.Name)
		if err := svc.CreateConfigKeyValue(gwNamespace, gwService, key, frag); err != nil {
			return fmt.Errorf("failed to upsert fragment %s: %w", key, err)
		}
	}

	// Step 6: run mergecfg on all gateway nodes.
	nodes, err := svc.GetServiceNodeFromState(cluster.Conf.Cloud18GatewayService)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
			"Cannot find node of gateway service: %s", err)
		return err
	}

	var errtask ErrSlice
	for _, node := range nodes {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Merging app route fragments for %s on host %s", app.GetId(), node)
		if err := svc.RunTask(cluster.Name, cluster.Conf.Cloud18GatewayService, node, "task#mergecfg", ""); err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
				"Cannot aggregate gateway fragments: %s", err)
			errtask = append(errtask, err)
		}
	}

	if len(errtask) > 0 {
		return ErrSlice(errtask)
	}

	return nil
}

// provisionHostRouteDNS calls cloud18-domain-add-script with the full
// normalized FQDN.  The script is the authority on which domains it manages;
// repman does not restrict by suffix.  If no script is configured DNS is
// considered operator-managed and provisioning is skipped without error.
func (cluster *Cluster) provisionHostRouteDNS(route config.Route) error {
	cname := normalizeRouteCNAME(route.CName)
	if cname == "" {
		return fmt.Errorf("host route has an empty CNAME — set a valid hostname before provisioning")
	}
	if cluster.Conf.Cloud18DomainAddScript == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"cloud18-domain-add-script not configured, skipping DNS provisioning for CNAME %s", cname)
		return nil
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"Provisioning DNS for CNAME %s via add script", cname)
	return cluster.BashScriptProvDNS(cname)
}

// managedCNAMEsFile returns the path of the per-app JSON file that records
// which managed host-route CNAMEs have been provisioned in the DNS provider.
// This allows reconcile to detect and clean up stale records when routes are
// dropped or renamed between runs.
func managedCNAMEsFile(app *App) string {
	return filepath.Join(app.Datadir, "managed_cnames.json")
}

// loadProvisionedManagedCNAMEs reads the persisted set of managed CNAMEs for
// the given app.  Returns an empty set when the file does not exist yet.
func loadProvisionedManagedCNAMEs(app *App) (map[string]struct{}, error) {
	data, err := os.ReadFile(managedCNAMEsFile(app))
	if os.IsNotExist(err) {
		return make(map[string]struct{}), nil
	}
	if err != nil {
		return nil, err
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, err
	}
	set := make(map[string]struct{}, len(list))
	for _, c := range list {
		set[c] = struct{}{}
	}
	return set, nil
}

// saveProvisionedManagedCNAMEs writes the current managed CNAME set to disk
// atomically: write to a temp file in the same directory then rename so that
// a crash cannot leave an empty or partially-written file.
func saveProvisionedManagedCNAMEs(app *App, set map[string]struct{}) error {
	list := make([]string, 0, len(set))
	for c := range set {
		list = append(list, c)
	}
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	dest := managedCNAMEsFile(app)
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".managed_cnames_*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0644); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, dest)
}

// reconcileStaleManagedCNAMEs computes the persisted managed CNAME set after
// attempting to remove stale entries (those in old but not in new).
//
// When stale entries exist and cloud18-domain-drop-script is not configured,
// cleanup is skipped with a warning and the stale entries are merged back into
// the returned persisted set so they can be cleaned later once a drop script is
// available.  This allows HAProxy fragment generation to continue even when the
// optional DNS cleanup cannot run.
func (cluster *Cluster) reconcileStaleManagedCNAMEs(
	app *App,
	oldManagedCNAMEs map[string]struct{},
	newManagedCNAMEs map[string]struct{},
) (persisted map[string]struct{}, err error) {
	// Collect stale entries: in old but not desired.
	var staleCNAMEs []string
	for cname := range oldManagedCNAMEs {
		if _, stillWanted := newManagedCNAMEs[cname]; !stillWanted {
			staleCNAMEs = append(staleCNAMEs, cname)
		}
	}

	// Start persisted set from the desired (new) set.
	persisted = make(map[string]struct{}, len(newManagedCNAMEs))
	for k := range newManagedCNAMEs {
		persisted[k] = struct{}{}
	}

	if len(staleCNAMEs) == 0 {
		return persisted, nil
	}

	if cluster.Conf.Cloud18DomainDropScript == "" {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"App %s has %d stale managed DNS record(s) %v but cloud18-domain-drop-script is not configured — "+
				"proceeding with route reconcile; stale entries retained in state for later cleanup",
			app.Name, len(staleCNAMEs), staleCNAMEs)
		// Retain stale entries so they are not forgotten and can be cleaned
		// once a drop script is configured.
		for k := range oldManagedCNAMEs {
			persisted[k] = struct{}{}
		}
		return persisted, nil
	}

	for _, fqdn := range staleCNAMEs {
		if _, managed := cluster.ManagedHostCNAME(fqdn); !managed {
			// The FQDN was recorded under a previous managed suffix (cluster
			// rename, domain/subdomain/zone config change, etc.).  Log a warning
			// and retain the entry rather than aborting the whole reconcile — a
			// drop-script call for an unrecognised zone would be worse than
			// leaving the entry for manual cleanup.
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
				"Stale CNAME %s (app %s) does not match the current managed suffix — skipping drop; retaining in state for manual cleanup",
				fqdn, app.Name)
			persisted[fqdn] = struct{}{}
			continue
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
			"Deleting stale managed DNS record %s (app %s)", fqdn, app.Name)
		if err := cluster.BashScriptDeprovDNS(fqdn); err != nil {
			return nil, fmt.Errorf("failed to delete stale managed DNS record %s (app %s): %w", fqdn, app.Name, err)
		}
	}

	return persisted, nil
}

type ErrSlice []error

func (e ErrSlice) Error() string {
	var buf bytes.Buffer
	for i, err := range e {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString(err.Error())
	}
	return buf.String()
}
