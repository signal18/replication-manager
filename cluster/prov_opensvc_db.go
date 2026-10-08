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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/opensvc"
	"github.com/signal18/replication-manager/utils/state"
	ini "gopkg.in/ini.v1"
)

func (cluster *Cluster) GetDatabaseServiceConfig(s *ServerMonitor) []byte {
	agent, err := cluster.OpenSVCFoundDatabaseAgent(s)
	if err != nil {
		cluster.errorChan <- err
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't OpenSVCFoundDatabaseAgent in service config %s", err)
		return []byte("")
	}
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		svc := cluster.OpenSVCConnect()
		res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, s.Id, agent.Node_name)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC config template %s", err)
			return []byte("")
		}
		return []byte(res)
	} else {
		res, err := s.GenerateDBTemplateV2()
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC config template  %s", err)
			return []byte("")
		}
		return res
	}
}

func (cluster *Cluster) OpenSVCProvisionDatabaseV1(s *ServerMonitor, svc opensvc.Collector, agent opensvc.Host) error {
	var taglist []string

	// Unprovision if already in OpenSVC
	var idsrv string
	mysrv, err := svc.GetServiceFromName(cluster.Name + "/svc/" + s.Name)
	if err == nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Found opensvc database service %s service %s", cluster.Name+"/svc/"+s.Name, mysrv.Svc_id)
		idsrv = mysrv.Svc_id
	} else {
		idsrv, err = svc.CreateService(cluster.Name+"/svc/"+s.Name, "MariaDB")
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't create OpenSVC service %s", err)
			return err
		}
	}
	err = svc.DeleteServiceTags(idsrv)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't delete service tags: %s", err)
		return err
	}
	taglist = strings.Split(svc.ProvTags, ",")
	svctags, _ := svc.GetTags()
	for _, tag := range taglist {
		idtag, err := svc.GetTagIdFromTags(svctags, tag)
		if err != nil {
			idtag, _ = svc.CreateTag(tag)
		}
		svc.SetServiceTag(idtag, idsrv)
	}

	// create template && bootstrap
	res, err := s.GenerateDBTemplate(svc, []string{s.Host}, []string{s.Port}, []opensvc.Host{agent}, cluster.Name+"/svc/"+s.Name, agent.Node_name)
	if err != nil {
		return err
	}
	idtemplate, _ := svc.CreateTemplate(cluster.Name+"/svc/"+s.Name, string(res))
	idaction, _ := svc.ProvisionTemplate(idtemplate, agent.Node_id, cluster.Name+"/svc/"+s.Name)
	err = cluster.OpenSVCWaitDequeue(svc, idaction)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}
	task := svc.GetAction(strconv.Itoa(idaction))
	if task != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", task.Stderr)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't fetch task")
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionDatabaseV2(s *ServerMonitor, svc opensvc.Collector, agent opensvc.Host) error {
	err := cluster.OpenSVCCreateMaps(s.Agent)
	if err != nil {
		return err
	}
	res, err := s.GenerateDBTemplateV2()
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", res)
	err = svc.CreateTemplateV2(cluster.Name, s.ServiceName, s.Agent, res)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision database:  %s ", err)
	}

	return nil
}

func (cluster *Cluster) OpenSVCProvisionDatabaseV3(s *ServerMonitor, svc opensvc.Collector, agent opensvc.Host) error {
	err := cluster.OpenSVCCreateMaps(s.Agent)
	if err != nil {
		return err
	}
	res, err := s.GenerateDBTemplateV3()
	if err != nil {
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", res)
	body, err := svc.CreateTemplateV3(cluster.Name, s.ServiceName, s.Agent, res)
	if err != nil {
		if !isOpenSVCAlreadyExists(err) {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision database:  %s ", err)
			return err
		}
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Database template already exists, reusing existing template: %s", s.ServiceName)
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "Template created with response: %s", body)
	}

	delay := normalizeOpenSVCV3ProvisionDelay(cluster.Conf.ProvOpensvcV3ProvisionDelay)
	time.Sleep(time.Duration(delay) * time.Second)

	return svc.ProvisionServiceV3(cluster.Name, s.ServiceName)
}

// OpenSVCUpdateDatabaseTemplate regenerates the service config for a single
// server and pushes it to OpenSVC via UpdateObjectV3.  It does not trigger
// provisioning or wait for the service to start — safe to run on a live node.
func (cluster *Cluster) OpenSVCUpdateDatabaseTemplate(s *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if !svc.IsV3() {
		return fmt.Errorf("update-opensvc-template requires OpenSVC v3 API")
	}
	var res []byte
	var err error
	if app := cluster.engineAppOfServer(s); app != nil {
		// an engine server: its definition is the one its template renders (the same
		// object, the server's service); refreshed here like any server's, so the rolling
		// restart and the restart carry a changed mount or container without reprovisioning
		res, err = cluster.OpenSVCGetAppTemplateV3(app)
		if err != nil {
			return err
		}
		// and the scripts its containers run from config keys: the start script and the
		// configurator render, written at provision, follow the build like the definition
		// (pg1 of pg-active-passive restarted on an old start script, 2026-10-06); the
		// jobs script has its own upgrade (checkPostgresJobsVersion)
		if script := appStartScript(app); script != "" {
			if err := svc.CreateConfigKeyValue(cluster.Name, app.Name, appStartScriptKey, script); err != nil { // create updates an existing key
				return fmt.Errorf("config key %s of %s: %w", appStartScriptKey, app.Name, err)
			}
		}
		if app.AppConfig != nil && app.AppConfig.ProvAppConfigurator != "" {
			script, err := cluster.AppConfiguratorScript(app)
			if err != nil {
				return err
			}
			if err := svc.CreateConfigKeyValue(cluster.Name, app.Name, appConfiguratorScriptKey, script); err != nil {
				return fmt.Errorf("config key %s of %s: %w", appConfiguratorScriptKey, app.Name, err)
			}
		}
	} else {
		if _, err = cluster.OpenSVCFoundDatabaseAgent(s); err != nil {
			return err
		}
		res, err = s.GenerateDBTemplateV3()
		if err != nil {
			return err
		}
	}
	svcparts := strings.SplitN(s.ServiceName, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service name format %q, expected namespace/kind/name", s.ServiceName)
	}
	ns, kind, svcname := svcparts[0], svcparts[1], svcparts[2]
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Refreshing OpenSVC template for %s", s.ServiceName)
	if _, err = svc.UpdateObjectV3(ns, kind, svcname, res); err != nil {
		return err
	}
	// om3 commits the file synchronously but reloads the instance config
	// asynchronously: the rolling restart's start issued right after this PUT ran
	// on the PREVIOUS config and recreated the containers without the pushed
	// change (#1792, belair 2026-09-14). Return only once the node has loaded it.
	return svc.WaitObjectConfigSettledV3(s.Agent, ns, kind, svcname, openSVCConfigSettleTimeout)
}

func (cluster *Cluster) OpenSVCProvisionDatabaseService(s *ServerMonitor) {
	if app := cluster.engineAppOfServer(s); app != nil {
		// An engine server (PostgreSQL member rendered from an app template): its service
		// is the app's, provisioned from that template; the database template would deploy
		// the cluster's default image under the member's name (MariaDB 13 answering on
		// pg1:5432, pg-logical 2026-10-08). The app provision reports on errorChan itself.
		cluster.OpenSVCProvisionAppService(app)
		return
	}
	cluster.warnDBRunAsVolumeMismatch()
	svc := cluster.OpenSVCConnect()
	agent, err := cluster.OpenSVCFoundDatabaseAgent(s)
	if err != nil {
		cluster.errorChan <- err
		return
	}
	cluster.ResolveDatabaseImage(false) // the service definition carries a release, not a pointer (#1862)

	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		err = cluster.OpenSVCProvisionDatabaseV1(s, svc, agent)
	} else if svc.IsV3() {
		err = cluster.OpenSVCProvisionDatabaseV3(s, svc, agent)
	} else {
		err = cluster.OpenSVCProvisionDatabaseV2(s, svc, agent)
	}
	if err != nil {
		cluster.errorChan <- err
		return
	}
	cluster.WaitDatabaseStart(s)

	cluster.errorChan <- nil
	return
}

// OpenSVCUpdateDatabaseServiceConfig patches image_pull_policy in the live service config on
// OpenSVC without touching any other keys, cfg objects, or secret objects.
func (cluster *Cluster) OpenSVCUpdateDatabaseServiceConfig(s *ServerMonitor, forcePull bool) error {
	svc := cluster.OpenSVCConnect()
	_, err := cluster.OpenSVCFoundDatabaseAgent(s)
	if err != nil {
		return err
	}

	if !svc.IsV3() {
		const dbSection = "container#db"
		const jobsSection = "container#jobs"
		const key = "image_pull_policy"
		if forcePull {
			return svc.SetServiceConfigKeysV2(s.ServiceName, s.Agent, []string{
				"env.docker_image=" + cluster.deployImage(), // the release the upgrade pins (#1862)
				dbSection + "." + key + "=always",
				jobsSection + "." + key + "=always",
			})
		}
		return svc.UnsetServiceConfigKeysV2(s.ServiceName, s.Agent, []string{
			dbSection + "." + key,
			jobsSection + "." + key,
		})
	}

	svcparts := strings.SplitN(s.ServiceName, "/", 3)
	if len(svcparts) != 3 {
		return fmt.Errorf("invalid service name format %q, expected namespace/kind/name", s.ServiceName)
	}
	ns, kind, svcname := svcparts[0], svcparts[1], svcparts[2]

	raw, err := svc.GetObjectConfigFileV3(ns, kind, svcname)
	if err != nil {
		return err
	}

	cfg, err := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("failed to parse service config for %s: %w", s.ServiceName, err)
	}

	for _, section := range cfg.Sections() {
		name := section.Name()
		if name == "env" && forcePull {
			// The pull phase of the upgrade pins the release the declared image resolved
			// to (#1862); the file keeps every other key as it is.
			section.Key("docker_image").SetValue(cluster.deployImage())
			continue
		}
		if name != "container#db" && name != "container#jobs" {
			continue
		}
		if forcePull {
			section.Key("image_pull_policy").SetValue("always")
		} else {
			section.DeleteKey("image_pull_policy")
		}
	}

	var buf bytes.Buffer
	if _, err = cfg.WriteTo(&buf); err != nil {
		return fmt.Errorf("failed to serialize patched service config for %s: %w", s.ServiceName, err)
	}

	_, err = svc.UpdateObjectV3(ns, kind, svcname, buf.Bytes())
	return err
}

func (cluster *Cluster) OpenSVCStopDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.OpenSVCFoundDatabaseAgent(server)
		if err != nil {
			return err
		}
		svc.StopService(agent.Node_id, service.Svc_id)
	} else if svc.IsV3() {
		if len(cluster.GetDatabaseAgentNames(server)) > 1 {
			// a service placed on several agents (its prov-db-agents) is stopped by the
			// orchestration, which holds it down on every node, frozen or not (verified on
			// om3, 2026-10-06): an instance stop is undone by om3, which re-places the
			// service on another node (pg1 of pg-logical moved from s18-fr-4 to s18-fr-5,
			// then back, instead of stopping)
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"OpenSVC V3 orchestrated stop for %s (placed on several nodes)", server.URL)
			if err := svc.StopServiceV3(cluster.Name, server.ServiceName); err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop database: %s", err)
				return err
			}
			return nil
		}
		// the instance to stop is where the service RUNS: not necessarily the agent the
		// round robin assigned (pg2 of pg-stream: the stop went to an idle node and
		// nothing stopped, 2026-10-06)
		agent := server.placementNode()
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"OpenSVC V3 instance stop for %s on node %s", server.URL, agent)
		err := svc.StopInstanceV3(agent, server.ServiceName)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop database: %s", err)
			return err
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"OpenSVC V2 stop for %s", server.URL)
		err := svc.StopServiceV2(cluster.Name, server.ServiceName, server.Agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not stop database: %s", err)
			return err
		}
	}
	return nil
}

func (cluster *Cluster) OpenSVCStartDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		service, err := svc.GetServiceFromName(cluster.Name + "/svc/" + server.Name)
		if err != nil {
			return err
		}
		agent, err := cluster.OpenSVCFoundDatabaseAgent(server)
		if err != nil {
			return err
		}
		svc.StartService(agent.Node_id, service.Svc_id)
	} else if svc.IsV3() {
		if cluster.Conf.ProvOpensvcUseOrchestratedStart {
			// HA-safe path: abort (clears warn + cancels pending orchestration)
			// then restart (atomic stop+start, avoids race with repman detecting
			// the service as down between stop and start).
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"OpenSVC V3 orchestrated start: abort + restart for %s", server.URL)
			if abortErr := svc.AbortServiceV3(cluster.Name, server.ServiceName); abortErr != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn,
					"OpenSVC V3 abort before start failed for %s: %s (proceeding)", server.URL, abortErr)
			}
			err := svc.RestartServiceV3(cluster.Name, server.ServiceName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"OpenSVC V3 orchestrated restart failed for %s: %s", server.URL, err)
				return err
			}
		} else {
			// Default: instance-level start (om start --local). Bypasses the
			// orchestrator's global monitor state check so it works even when the
			// service is in warn state. Does not coordinate failover volumes.
			if len(cluster.GetDatabaseAgentNames(server)) > 1 {
				// a service placed on several agents (its prov-db-agents): the orchestrator
				// picks the node, an instance start on one node would fight its placement
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
					"OpenSVC V3 orchestrated start for %s (failover placement)", server.URL)
				// retried while the orchestrator still runs the stop that preceded (409)
				deadline := time.Now().Add(3 * time.Minute)
				for {
					err := svc.StartServiceV3(cluster.Name, server.ServiceName)
					if err == nil {
						return nil
					}
					if !(strings.Contains(err.Error(), "409") || strings.Contains(err.Error(), "in progress")) || time.Now().After(deadline) {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
							"OpenSVC V3 start failed for %s: %s", server.URL, err)
						return err
					}
					time.Sleep(5 * time.Second)
				}
			}
			agent := server.placementNode()
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
				"OpenSVC V3 instance start for %s on node %s", server.URL, agent)
			err := svc.StartInstanceV3(agent, server.ServiceName)
			if err != nil {
				cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr,
					"OpenSVC V3 instance start failed for %s: %s", server.URL, err)
				return err
			}
		}
	} else {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
			"OpenSVC V2 start for %s", server.URL)
		err := svc.StartServiceV2(cluster.Name, server.ServiceName, server.Agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not start database: %s", err)
			return err
		}
	}

	return nil
}

func (cluster *Cluster) OpenSVCRestartDatabaseService(server *ServerMonitor, node string, rid string) error {
	svc := cluster.OpenSVCConnect()
	agent := server.Agent
	if node != "" {
		agent = node
	}

	if err := validateRestartRid(rid); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Database restart validation failed: %s", err)
		return err
	}

	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Restart with collector API not supported, use V2 API")
		return errors.New("Restart with collector API not supported")
	}

	if svc.IsV3() {
		if rid != "" {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "RID restart is not supported in OpenSVC v3, falling back to full service restart")
		}
		// Optimistic clear before restart
		cluster.OpenSVCClearDatabaseInstanceState(server, agent)

		err := svc.RestartServiceV3(cluster.Name, server.ServiceName)
		if err != nil {
			return err
		}
		return nil
	}

	err := svc.RestartServiceV2(cluster.Name, server.ServiceName, agent, rid)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not restart database:  %s ", err)
		return err
	}

	return nil
}

func (cluster *Cluster) OpenSVCAbortDatabaseService(server *ServerMonitor) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI || !svc.IsV3() {
		err := ErrOpenSVCAbortNotSupported
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not abort database: %s", err)
		return err
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Aborting orchestration for %s", server.ServiceName)
	err := svc.AbortServiceV3(cluster.Name, server.ServiceName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not abort database: %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) OpenSVCClearDatabaseInstanceState(server *ServerMonitor, node string) error {
	svc := cluster.OpenSVCConnect()
	if cluster.Conf.ProvOpensvcUseCollectorAPI || !svc.IsV3() {
		err := ErrOpenSVCClearNotSupported
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not clear database instance state: %s", err)
		return err
	}

	agent := server.Agent
	if node != "" {
		agent = node
	}

	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Clearing instance state for %s on node %s", server.ServiceName, agent)
	err := svc.ClearInstanceV3(agent, server.ServiceName)
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not clear database instance state: %s", err)
		return err
	}

	return nil
}

func (cluster *Cluster) OpenSVCUnprovisionDatabaseService(server *ServerMonitor) {
	if app := cluster.engineAppOfServer(server); app != nil {
		// An engine server: the app unprovision purges its service and the volume objects
		// of its template (pg1-drbd, not a database volume named pg1 that answers 404 and
		// leaves the member's DRBD volumes provisioned on every node, pg-logical 2026-10-08).
		cluster.errorChan <- cluster.OpenSVCUnprovisionAppService(app)
		return
	}
	svc := cluster.OpenSVCConnect()
	var opErr error
	if cluster.Conf.ProvOpensvcUseCollectorAPI {
		node, err := cluster.OpenSVCFoundDatabaseAgent(server)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't find database agent %s, %s", cluster.Name+"/svc/"+server.Name, err)
			opErr = errors.Join(opErr, err)
		} else {
			for _, service := range node.Svc {
				if cluster.Name+"/svc/"+server.Name == service.Svc_name {
					idaction, err := svc.UnprovisionService(node.Node_id, service.Svc_id)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't queue unprovision database %s, %s", cluster.Name+"/svc/"+server.Name, err)
						opErr = errors.Join(opErr, err)
						continue
					}

					err = cluster.OpenSVCWaitDequeue(svc, idaction)
					if err != nil {
						cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can't unprovision database %s, %s", cluster.Name+"/svc/"+server.Name, err)
						opErr = errors.Join(opErr, err)
					}
				}
			}
		}
	} else if svc.IsV3() {
		err := svc.PurgeServiceV3(cluster.Name, server.ServiceName)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision database service:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
		err = svc.PurgeServiceV3(cluster.Name, cluster.Name+"/vol/"+server.Name)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision database volume:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
	} else {
		err := svc.PurgeServiceV2(cluster.Name, server.ServiceName, server.Agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision database service:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
		err = svc.PurgeServiceV2(cluster.Name, cluster.Name+"/vol/"+server.Name, server.Agent)
		if err != nil {
			cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not unprovision database volume:  %s ", err)
			opErr = errors.Join(opErr, err)
		}
	}
	cluster.errorChan <- opErr
}

func (cluster *Cluster) OpenSVCFoundDatabaseAgent(server *ServerMonitor) (opensvc.Host, error) {
	var clusteragents []opensvc.Host
	var agent opensvc.Host
	svc := cluster.OpenSVCConnect()
	agents, err := svc.GetNodes()
	if err != nil {
		cluster.SetState("ERR00082", state.State{ErrType: "WARNING", ErrDesc: fmt.Sprintf(clusterError["ERR00082"], err), ErrFrom: "TOPO"})
	}
	if agents == nil {
		return agent, errors.New("Error getting OpenSVC node list")
	}
	names := cluster.GetDatabaseAgentNames(server)
	for _, node := range agents {
		// the server's agent list (engine app, prov-db-agents), every node when none
		if len(names) == 0 {
			clusteragents = append(clusteragents, node)
			continue
		}
		for _, n := range names {
			if n == node.Node_name {
				clusteragents = append(clusteragents, node)
				break
			}
		}
	}
	for i, srv := range cluster.Servers {

		if srv.Id == server.Id {
			if len(clusteragents) == 0 {
				return agent, errors.New("Indice not found in database node list")
			}
			return clusteragents[i%len(clusteragents)], nil
		}
	}
	return agent, errors.New("Indice not found in database node list")
}

// Start priority of the services in the orchestrator (DEFAULT.priority, smaller first,
// default 50): when a node boots and hits node.max_parallel, databases are served before
// the proxies that route to them and before the apps that connect through the proxies;
// the cluster's system services (dns at 5) keep going first. An engine server rendered
// from an app template is a database. The key is honoured only for an API identity holding
// the orchestrator's "prioritizer" grant (silently dropped otherwise).
const (
	openSVCPriorityDatabase = "10"
	openSVCPriorityProxy    = "20"
	openSVCPriorityApp      = "30"
)

func (server *ServerMonitor) OpenSVCGetDBDefaultSection() map[string]string {
	svcdefault := make(map[string]string)
	svcdefault["nodes"] = server.Agent
	if server.ClusterGroup.Conf.ProvDiskPool == "zpool" && server.ClusterGroup.Conf.AutorejoinZFSFlashback && server.IsPrefered() {
		svcdefault["cluster_type"] = "failover"
		svcdefault["rollback"] = "true"
		svcdefault["orchestrate"] = "start"
	} else {
		svcdefault["rollback"] = "false"
		svcdefault["orchestrate"] = "ha"
	}
	svcdefault["app"] = server.ClusterGroup.Conf.ProvCodeApp
	svcdefault["priority"] = openSVCPriorityDatabase
	if server.ClusterGroup.Conf.ProvType == "docker" {
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcdefault["docker_daemon_private"] = "true"
			if server.ClusterGroup.Conf.ProvDiskType != "volume" {
				svcdefault["docker_data_dir"] = "{env.base_dir}/docker"

			} else {
				svcdefault["docker_data_dir"] = "{name}-docker/docker"
			}
			if server.ClusterGroup.Conf.ProvDiskPool == "zpool" {
				svcdefault["docker_daemon_args"] = " --storage-driver=zfs"
			} else {
				svcdefault["docker_daemon_args"] = " --storage-driver=overlay"
			}
		} else {
			svcdefault["docker_daemon_private"] = "false"
		}

	}
	return svcdefault
}

func (server *ServerMonitor) OpenSVCGetDBContainerSection() map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvType == "docker" || server.ClusterGroup.Conf.ProvType == "podman" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "{env.docker_image}"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		svccontainer["start_timeout"] = server.ClusterGroup.dbStartTimeout() // the orchestrator default is 5s (#1924)
		svccontainer["secrets_environment"] = "env/MYSQL_ROOT_PASSWORD"

		if server.ClusterGroup.Conf.ProvDBDockerTmpfsSize != "0" {
			svccontainer["run_args"] = fmt.Sprintf("--tmpfs=/tmp:size=%sm %s", server.ClusterGroup.Conf.ProvDBDockerTmpfsSize, server.ClusterGroup.Conf.ProvDBDockerRunArgs)
		} else {
			svccontainer["run_args"] = server.ClusterGroup.Conf.ProvDBDockerRunArgs
		}
		if runAsUID, runAsGID, set := server.ClusterGroup.dbRunAs(); set {
			// An operator --user in prov-db-docker-run-args keeps winning (docker
			// takes the last --user, so appending ours would silently override it).
			if !dockerRunArgsHaveUser(svccontainer["run_args"]) {
				svccontainer["run_args"] += fmt.Sprintf(" --user %d:%d", runAsUID, runAsGID)
			}
		} else if strings.Contains(strings.ToLower(server.ClusterGroup.Conf.ProvDbImg), "mysql") {
			svccontainer["run_args"] = svccontainer["run_args"] + " --user mysql"
		}
		if server.ClusterGroup.Conf.ProvDBDockerRunArgsLimit {
			// Container memory CAP = DBU tier + 1 overcommit DBU (GetDBContainerMemoryCapMB),
			// deliberately ABOVE prov-db-memory (which sizes my.cnf) so mariadbd has headroom
			// and is not OOM-killed when its real footprint exceeds the buffer pool.
			memStr := strconv.Itoa(server.ClusterGroup.GetDBContainerMemoryCapMB()) + "m"
			svccontainer["run_args"] = svccontainer["run_args"] + " --memory=" + memStr + " --memory-swap=" + memStr + " --cpus=" + server.ClusterGroup.Conf.ProvCores + ".0"
			// this need to find the device with df in container
			//  --device-read-iops=" + server.ClusterGroup.Conf.ProvIops +".0" --device-write-iops=device" + server.ClusterGroup.Conf.ProvIops
		}
		svccontainer["#run_args"] = "--user mysql --cap-add SYS_PTRACE --ulimit nofile=262144:262144"
		svccontainer["#command"] = "gdb -ex r -ex thread apply all bt -frame-arguments all full --args mariadbd"
		svccontainer["##docker_image"] = "quay.io/mariadb-foundation/mariadb-debug:10.11-mdev-33798-knielsen-pkgtest"
		svccontainer["volume_mounts"] = `/etc/localtime:/etc/localtime:ro {name}/data:/var/lib/mysql:rw {name}/mysql-files:/var/lib/mysql-files:rw {name}/etc/mysql:/etc/mysql:rw {name}/init:/docker-entrypoint-initdb.d:rw {name}/run/mysqld:/run/mysqld:rw`
		svccontainer["environment"] = server.OpenSVCGetDBContainerEnvironment()
		if server.ClusterGroup.Conf.ProvOpensvcImageForcePull {
			svccontainer["image_pull_policy"] = "always"
		}

		//Proceed with galera specific
		if server.ClusterGroup.GetTopology() == config.TopoMultiMasterWsrep && server.ClusterGroup.TopologyClusterDown() {
			if server.ClusterGroup.GetMaster() == nil {
				server.ClusterGroup.vmaster = server
				svccontainer["command"] = "mysqld --wsrep_new_cluster"
			}
		}
	}
	return svccontainer
}

// OpenSVCGetDBContainerEnvironment builds the container#db environment line,
// appending the shared allocator tuning (GetDBAllocatorEnv, #1749).
func (server *ServerMonitor) OpenSVCGetDBContainerEnvironment() string {
	env := "MYSQL_INITDB_SKIP_TZINFO=yes"
	if server.ClusterGroup.DBImageAutoUpgradeEnv() {
		env += " MARIADB_AUTO_UPGRADE=1"
	}
	if preload, arenaMax := server.ClusterGroup.GetDBAllocatorEnv(); preload != "" {
		env += " LD_PRELOAD=" + preload + " MALLOC_ARENA_MAX=" + arenaMax
	}
	return env
}

func (server *ServerMonitor) OpenSVCGetJobsContainerSection() map[string]string {
	return server.openSVCGetJobsContainerSection(server.ClusterGroup.xtrabackupBundleImage())
}

// openSVCGetJobsContainerSection renders the jobs container from a helper image already resolved for this template.
func (server *ServerMonitor) openSVCGetJobsContainerSection(xtrabackupImage string) map[string]string {
	svccontainer := make(map[string]string)
	if server.ClusterGroup.Conf.ProvType == "docker" || server.ClusterGroup.Conf.ProvType == "podman" {
		svccontainer["tags"] = ""
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["image"] = "{env.docker_image}"
		svccontainer["type"] = server.ClusterGroup.Conf.ProvType
		svccontainer["start_timeout"] = server.ClusterGroup.dbStartTimeout() // the orchestrator default is 5s (#1924)
		svccontainer["secrets_environment"] = "env/MYSQL_ROOT_PASSWORD"
		svccontainer["run_args"] = server.ClusterGroup.Conf.ProvDBJobsDockerRunArgs
		if server.ClusterGroup.dbIdentityManaged() {
			// The jobs container runs as root whatever the image's own USER is
			// (Percona Server images default to mysql, 1001): it must read and
			// chown a datadir owned by prov-db-volume-uid (db_owner in dbjobs_new.sh).
			// First, so a --user in prov-db-jobs-docker-run-args still wins.
			svccontainer["run_args"] = strings.TrimSpace("--user 0:0 " + svccontainer["run_args"])
		}
		svccontainer["volume_mounts"] = `/etc/localtime:/etc/localtime:ro {name}/jobs:/var/lib/replication-manager-jobs:rw {name}/data:/var/lib/mysql:rw {name}/etc/mysql:/etc/mysql:rw {name}/init:/docker-entrypoint-initdb.d:rw {name}/run/mysqld:/run/mysqld:rw {name}-sec/:/credentials`
		if xtrabackupImage != "" {
			// read-only: only the helper init container writes the bundle
			svccontainer["volume_mounts"] += " {name}/xtrabackup:" + xtrabackupBundleMount + ":ro"
		}
		if server.ClusterGroup.Conf.MonitoringSystemResources {
			// Bind ONLY this service's pg cgroup slice read-only into the jobs
			// container at /svc-cgroup, so the system-units sensor reads the
			// whole-service memory.current/cpu.stat/io.stat. Least privilege: the
			// sidecar sees only its own service's cgroup -- unlike --cgroupns=host,
			// which would expose the whole node's cgroup tree (every co-tenant on a
			// shared host). {namespace}/{svcname} are substituted by OpenSVC like
			// {name} above. On by default; the flag is the off-switch (T14) if a
			// bad bind blocks container start on an unexpected cgroup layout.
			svccontainer["volume_mounts"] += " " + openSVCServiceCgroupMount(server.ClusterGroup.Name, server.Name)
		}
		svccontainer["environment"] = `MYSQL_INITDB_SKIP_TZINFO=yes`
		if bundlePath := server.ClusterGroup.xtrabackupBundlePathForImage(xtrabackupImage); bundlePath != "" {
			// the tools of the injection on the PATH of the container itself (see xtrabackupBundlePath)
			svccontainer["environment"] += " PATH=" + bundlePath
		}
		svccontainer["command"] = "/docker-entrypoint-initdb.d/dbjobs_launcher_with_sigterm"
		svccontainer["entrypoint"] = "/bin/bash"
		if server.ClusterGroup.Conf.ProvOpensvcImageForcePull {
			svccontainer["image_pull_policy"] = "always"
		}
	}
	return svccontainer
}

func (server *ServerMonitor) OpenSVCGetDBEnvSection() map[string]string {
	svcenv := make(map[string]string)
	agent, err := server.ClusterGroup.GetDatabaseAgent(server)
	if err != nil {
		server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Can not provision database:  %s ", err)
		server.ClusterGroup.errorChan <- err
		return svcenv
	}
	svcenv["nodes"] = agent.HostName
	svcenv["size"] = server.ClusterGroup.provDiskSizeForOpenSVC()
	svcenv["docker_image"] = server.deployImage()
	ips := strings.Split(server.ClusterGroup.Conf.ProvGateway, ".")
	masks := strings.Split(server.ClusterGroup.Conf.ProvNetmask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}

	/*svcenv["ip_pod01"] = server.Host
	svcenv["port_pod01"] = server.Port
	svcenv["mrm_api_addr"] = server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort
	svcenv["mrm_cluster_name"] = server.ClusterGroup.GetClusterName()
	// not required for socket prov

			network := strings.Join(ips, ".")
			svcenv["mysql_root_password"] = server.Pass
			svcenv["mysql_root_user"] = server.User
			svcenv["network"] = network
			svcenv["gateway"] = server.ClusterGroup.Conf.ProvGateway
			svcenv["netmask"] = server.ClusterGroup.Conf.ProvNetmask
		svcenv["base_dir"] = "/srv/{namespace}-{svcname}"
		svcenv["max_iops"] = server.ClusterGroup.Conf.ProvIops
		maxMemMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", server.ClusterGroup.Conf.ProvMem, true)
		svcenv["max_mem"] = strconv.Itoa(maxMemMB)
		svcenv["max_cores"] = server.ClusterGroup.Conf.ProvCores
		svcenv["micro_srv"] = server.ClusterGroup.Conf.ProvType
		svcenv["gcomm"] = server.ClusterGroup.GetGComm()
		svcenv["server_id"] = string(server.Id[2:10])
		svcenv["innodb_buffer_pool_size"] = server.ClusterGroup.GetConfigInnoDBBPSize()
		svcenv["innodb_log_file_size"] = server.ClusterGroup.GetConfigInnoDBLogFileSize()
		svcenv["innodb_buffer_pool_instances"] = server.ClusterGroup.GetConfigInnoDBBPInstances()
		svcenv["innodb_log_buffer_size"] = "8"*/
	return svcenv
}

// OpenSVCGetSensorContainerSection builds the APU (Compute) sensor sidecar shared by
// proxy and app services. It is a long-running busybox container (detach=true, unlike
// the one-shot init container) that shares the service netns (container#01, for egress
// to repman) and has ONLY this service's cgroup slice bound read-only at /svc-cgroup
// (least privilege, same rationale as the DB jobs container). It runs init/app_job --
// staged into the config tarball via go:embed share/scripts/app_job.sh and extracted
// into the shared FS by the init container -- so no image baking and no moduleset edit.
// The SENSOR_API_KEY comes via the OpenSVC SECRET channel (secrets_environment), never
// svcenv. Gated by MonitoringSystemResources (the off-switch, T14).
func (cluster *Cluster) OpenSVCGetSensorContainerSection(kind string, name string) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType != "docker" && cluster.Conf.ProvType != "podman" {
		return svccontainer
	}
	svccontainer["type"] = "docker"
	svccontainer["image"] = "busybox"
	svccontainer["start_timeout"] = cluster.sensorStartTimeout(kind)
	svccontainer["netns"] = "container#01"
	svccontainer["detach"] = "true"
	svccontainer["rm"] = "true"
	svccontainer["entrypoint"] = "/bin/sh"
	if cluster.Conf.ProvDiskType != "volume" {
		svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {env.base_dir}:/bootstrap"
	} else {
		svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}:/bootstrap"
	}
	// Bind ONLY this service's cgroup slice read-only -- NOT --cgroupns=host, which would
	// expose every co-tenant on a shared node. {namespace}/{svcname} substituted by OpenSVC.
	svccontainer["volume_mounts"] += " " + openSVCServiceCgroupMount(cluster.Name, name)
	svccontainer["secrets_environment"] = "env/SENSOR_API_KEY"
	svccontainer["configs_environment"] = "env/REPLICATION_MANAGER_URL"
	svccontainer["environment"] = "MRM_CLUSTER={namespace} SENSOR_KIND=" + kind + " SENSOR_NAME=" + name + " SENSOR_INTERVAL=60"
	// The init container (detach=false) extracts init/app_job before later containers
	// start; the wait-loop makes the sidecar robust to ordering/retries regardless.
	svccontainer["command"] = "-c 'while [ ! -f /bootstrap/init/app_job ]; do sleep 2; done; exec sh /bootstrap/init/app_job'"
	return svccontainer
}

// OpenSVCGetAppSensorContainerSection is the sensor sidecar of an APP service. Same
// busybox/netns/cgroup contract as the proxy one, but an app service has no config
// tarball and no init container to stage init/app_job from, so the script arrives as a
// config key of the namespace `env` object (published by openSVCPublishAppJobScript, named
// after the script's content hash so a new repman build never has to overwrite a key) and
// is materialised from the environment at start. The sidecar runs the exact script version
// it was provisioned with; a reprovision picks up a newer one.
func (cluster *Cluster) OpenSVCGetAppSensorContainerSection(app *App, scriptKey string) map[string]string {
	svccontainer := cluster.OpenSVCGetSensorContainerSection(string(KindApp), app.Name)
	if len(svccontainer) == 0 {
		return svccontainer
	}
	svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro " + openSVCServiceCgroupMount(cluster.Name, app.Name)
	svccontainer["configs_environment"] = "env/REPLICATION_MANAGER_URL env/" + scriptKey
	svccontainer["command"] = "-c 'printf \"%s\\n\" \"$" + scriptKey + "\" > /tmp/app_job; exec sh /tmp/app_job'"
	return svccontainer
}

// OpenSVCGetNamespaceContainerSection is the pause container that holds the pod's network
// namespace; startTimeout is the kind's container start timeout: om3 defaults it to 5 s,
// which the pause container itself exceeded on a node restarting everything after the
// s18-fr-4 crash (pg1.curepipe stayed down for hours, 2026-10-08).
func (cluster *Cluster) OpenSVCGetNamespaceContainerSection(startTimeout string) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["type"] = "docker"
		svccontainer["image"] = "ghcr.io/opensvc/pause"
		svccontainer["start_timeout"] = startTimeoutOrDefault(startTimeout)
		svccontainer["hostname"] = "{svcname}.{namespace}.svc.{clustername}"
		svccontainer["rm"] = "true"
		svccontainer["run_args"] = cluster.Conf.ProvNetDockerRunArgs
	}
	return svccontainer
}

// OpenSVCGetInitContainerSection is shared by database and proxy services. It
// carries no database identity: the bootstrap then chowns /bootstrap/data to
// its legacy 999 default, which is what the proxies (ProxySQL, ShardProxy)
// always had. The database variant is OpenSVCGetDBInitContainerSection.
func (cluster *Cluster) OpenSVCGetInitContainerSection(port string) map[string]string {
	svccontainer := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		svccontainer["detach"] = "false"
		svccontainer["type"] = "docker"
		svccontainer["image"] = "alpine"
		svccontainer["netns"] = "container#01"
		svccontainer["rm"] = "true"
		svccontainer["start_timeout"] = "30s"
		svccontainer["optional"] = "true"
		if cluster.Conf.ProvDiskType != "volume" {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {env.base_dir}:/bootstrap"
		} else {
			svccontainer["volume_mounts"] = "/etc/localtime:/etc/localtime:ro {name}:/bootstrap"
		}
		svccontainer["command"] = "-c 'wget --no-check-certificate -q -O- $REPLICATION_MANAGER_URL/static/configurator/opensvc/bootstrap | sh'"
	}
	svccontainer["entrypoint"] = "/bin/sh"
	svccontainer["secrets_environment"] = "env/REPLICATION_MANAGER_PASSWORD"
	svccontainer["configs_environment"] = "env/REPLICATION_MANAGER_USER env/REPLICATION_MANAGER_URL"
	svccontainer["environment"] = "REPLICATION_MANAGER_CLUSTER_NAME={namespace} REPLICATION_MANAGER_HOST_NAME={fqdn} REPLICATION_MANAGER_HOST_PORT=" + port
	//	svccontainer["# Debug"] = ""
	//	svccontainer["# interactive"] = "true"
	//	svccontainer["# tty"] = "true"
	return svccontainer
}

// OpenSVCGetDBInitContainerSection is the database service's init container:
// the shared one plus, when the owner is managed (dbVolumeOwner), the UID/GID the
// bootstrap applies to the data volume. Otherwise the bootstrap keeps its legacy
// 999:999.
func (cluster *Cluster) OpenSVCGetDBInitContainerSection(port string) map[string]string {
	svccontainer := cluster.OpenSVCGetInitContainerSection(port)
	if ownerUID, ownerGID, managed := cluster.dbVolumeOwner(); managed {
		svccontainer["environment"] += fmt.Sprintf(" REPLICATION_MANAGER_DB_VOLUME_UID=%d REPLICATION_MANAGER_DB_VOLUME_GID=%d", ownerUID, ownerGID)
	}
	return svccontainer
}

// dockerRunArgsHaveUser reports whether docker run arguments already set the
// container user (--user, --user=, -u).
func dockerRunArgsHaveUser(args string) bool {
	for _, f := range strings.Fields(args) {
		if f == "--user" || strings.HasPrefix(f, "--user=") || (strings.HasPrefix(f, "-u") && !strings.HasPrefix(f, "--")) {
			return true
		}
	}
	return false
}

func (cluster *Cluster) OpenSVCGetTmpFsSection() map[string]string {
	svccontainer := make(map[string]string)
	svccontainer["type"] = "tmpfs"
	svccontainer["mnt"] = "{env.base_dir}/tmp"
	svccontainer["dev"] = "none"
	return svccontainer
}

func (server *ServerMonitor) OpenSVCGetTaskZFSSnapshotSection() map[string]string {
	//[task2]
	svctask := make(map[string]string)
	if !server.IsPrefered() || !server.ClusterGroup.Conf.ProvDiskSnapshot {
		return svctask
	}

	svctask["schedule"] = "@1"
	svctask["command"] = "{env.base_dir}/pod01/init/snapback"
	svctask["user"] = "root"
	return svctask
}

func (cluster *Cluster) OpenSVCGetNetSection() map[string]string {
	svcnet := make(map[string]string)
	if cluster.Conf.ProvNetCNI {
		svcnet["type"] = "cni"
		svcnet["netns"] = "container#01"
		svcnet["network"] = cluster.Conf.ProvNetCNICluster
		if cluster.GetTopology() == config.TopoMultiMasterWsrep {
			svcnet["wait_dns"] = "15s"
		}
		return svcnet
	} else if cluster.Conf.ProvType == "docker" {
		svcnet["type"] = "docker"
		svcnet["netns"] = "container#01"
	} else if cluster.Conf.ProvType == "podman" {
		svcnet["type"] = "podman"
		svcnet["netns"] = "container#01"
	}
	svcnet["ipdev"] = cluster.Conf.ProvNetIface
	svcnet["ipname"] = "{env.ip_pod01}"
	svcnet["netmask"] = "{env.netmask}"
	svcnet["network"] = "{env.network}"
	svcnet["gateway"] = "{env.gateway}"
	return svcnet
}

func (cluster *Cluster) OpenSVCGetTaskJobsSection() map[string]string {
	svctask := make(map[string]string)
	svctask["schedule"] = "@1"
	svctask["command"] = "svcmgr -s {svcpath} docker exec -i {namespace}..{svcname}.container.db /bin/bash /docker-entrypoint-initdb.d/dbjobs"
	svctask["user"] = "root"
	svctask["run_requires"] = "container#db(up,stdby up)"
	return svctask
}

func (cluster *Cluster) OpenSVCGetFSDockerPrivateSection() map[string]string {
	svcfs := make(map[string]string)
	podpool := "00"
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskPool == "lvm" || cluster.Conf.ProvDiskPool == "zpool" {
			podpool = "0000"
		}
		svcfs["type"] = cluster.Conf.ProvType
		if cluster.Conf.ProvDiskType == "loopback" {
			svcfs["dev"] = "{disk#" + podpool + ".name}/docker"
		} else if cluster.Conf.ProvDiskType == "pool" {
			svcfs["dev"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_docker"
		} else if cluster.Conf.ProvDiskPool == "none" {
			svcfs["dev"] = "{disk" + podpool + ".file}"
		}
		if cluster.Conf.ProvDiskPool == "zpool" {
			svcfs["mkfs_opt"] = "-o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy"
		}
		svcfs["mnt"] = "{env.base_dir}/docker"
		svcfs["size"] = cluster.Conf.ProvDiskDockerSize + "g"
	}
	return svcfs
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackDockerPrivateSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskType == "loopback" {
			svcdsk["type"] = "loop"
			svcdsk["file"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_docker.dsk"
			svcdsk["size"] = cluster.Conf.ProvDiskDockerSize + "g"
		}
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskZpoolDockerPrivateSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvType == "docker" || cluster.Conf.ProvType == "podman" {
		if cluster.Conf.ProvDiskType == "loopback" && cluster.Conf.ProvDiskPool == "zpool" {
			svcdsk["type"] = "zpool"
			svcdsk["name"] = "zp{namespace}-{svcname}_00"
			svcdsk["vdev"] = "{disk#00.file}"
			svcdsk["standby"] = "true"
		}
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackPodSection() map[string]string {
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvDiskType == "loopback" {
		//disk#01
		svcdsk["type"] = "loop"
		svcdsk["file"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}_pod01.dsk"
		svcdsk["size"] = "{env.size}g"
		svcdsk["standby"] = "true"
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetDiskLoopbackSnapshotPodSection() map[string]string {
	//"[disk#1001]
	svcdsk := make(map[string]string)
	if cluster.Conf.ProvDiskType == "loopback" {
		if cluster.Conf.ProvDiskPool == "lvm" {
			svcdsk["type"] = "lvm"
			svcdsk["name"] = "name = {namespace}-{svcname}_01"
			svcdsk["pvs"] = "{disk#01.file}"
		}
		if cluster.Conf.ProvDiskPool == "zpool" {
			svcdsk["type"] = "zpool"
			svcdsk["name"] = "zp{namespace}-{svcname}_pod01"
			svcdsk["vdev"] = "{disk#01.file}"
		}
		svcdsk["standby"] = "true"
	}
	return svcdsk
}

func (cluster *Cluster) OpenSVCGetFSTmpSection() map[string]string {
	svcfs := make(map[string]string)
	svcfs["type"] = "tmpfs"
	svcfs["mnt"] = "{env.base_dir}/tmp"
	svcfs["dev"] = "none"
	return svcfs
}

func (cluster *Cluster) OpenSVCGetFSPodSection() map[string]string {
	svcfs := make(map[string]string)
	if cluster.Conf.ProvDiskFS == "directory" {
		//fs#01
		svcfs["type"] = "directory"
		svcfs["path"] = " {env.base_dir}"
		if cluster.Conf.ProvType == "docker" {
			svcfs["pre_provision"] = "docker network create {env.subnet_name} --subnet {env.subnet_cidr}"
		}
	} else {

		podpool := "01"
		if cluster.Conf.ProvDiskPool == "lvm" || cluster.Conf.ProvDiskPool == "zpool" {
			podpool = "0001"
		}
		svcfs["type"] = cluster.Conf.ProvDiskFS
		if cluster.Conf.ProvDiskPool == "lvm" {
			svcfs["dev"] = " /dev/{namespace}-{svcname}_01"
			svcfs["vg"] = "{namespace}-{svcname}_01"
			svcfs["size"] = "100%FREE"
		} else if cluster.Conf.ProvDiskPool == "zpool" {
			if cluster.Conf.ProvDiskType == "loopback" || cluster.Conf.ProvDiskType == "physical" {
				svcfs["dev"] = "{disk#" + podpool + ".name}"
			} else if cluster.Conf.ProvDiskType == "pool" {
				svcfs["dev"] = cluster.Conf.ProvDiskDevice + "/{namespace}-{svcname}"
			}
			svcfs["size"] = "{env.size}"
			svcfs["mkfs_opt"] = "-o recordsize=16K -o primarycache=metadata -o atime=off -o compression=" + cluster.Conf.ProvDiskFSCompress + " -o mountpoint=legacy"
		} else { //no pool
			if cluster.Conf.ProvDiskType == "loopback" {
				svcfs["dev"] = "{disk#" + podpool + ".name}"
			} else {
				svcfs["dev"] = "{disk#" + podpool + ".file}"
			}
			svcfs["size"] = "{env.size}"
		}
		svcfs["mnt"] = "{env.base_dir}"
		svcfs["standby"] = "true"
	}
	return svcfs
}

func (server *ServerMonitor) OpenSVCGetZFSSnapshotSection() map[string]string {
	svcsnap := make(map[string]string)
	if !server.IsPrefered() || !server.ClusterGroup.Conf.ProvDiskSnapshot {
		return svcsnap
	}
	if server.ClusterGroup.Conf.ProvDiskPool == "zpool" {
		svcsnap["type"] = "zfssnap"
		svcsnap["dataset"] = "{disk#0001.name}/pod01"
		svcsnap["recursive"] = "true"
		svcsnap["name"] = "daily"
		svcsnap["schedule"] = "00:01-02:00@120"
		svcsnap["keep"] = strconv.Itoa(server.ClusterGroup.Conf.ProvDiskSnapshotKeep)
		svcsnap["sync_max_delay"] = "1440"
	}
	return svcsnap
}

// OpenSVCGetVolumeDataSection is the data volume of a database service (volume#01 of
// GenerateDBTemplateMap only). Its owner is prov-db-volume-uid (dbVolumeOwner): a proxy
// service must not reuse this section, its data keeps the legacy 999 owner.
func (cluster *Cluster) OpenSVCGetVolumeDataSection() map[string]string {
	return cluster.openSVCGetVolumeDataSection(cluster.xtrabackupBundleImage())
}

// openSVCGetVolumeDataSection renders the data volume from a helper image already resolved for this template.
func (cluster *Cluster) openSVCGetVolumeDataSection(xtrabackupImage string) map[string]string {
	svcvol := make(map[string]string)
	ownerUID, ownerGID, _ := cluster.dbVolumeOwner()
	svcvol["name"] = "{name}"
	svcvol["pool"] = cluster.Conf.ProvVolumeData
	svcvol["size"] = "{env.size}"
	svcvol["directories"] = "run/mysqld"
	if xtrabackupImage != "" {
		svcvol["directories"] += " xtrabackup"
	}
	svcvol["user"] = strconv.Itoa(ownerUID)
	svcvol["group"] = strconv.Itoa(ownerGID)
	return svcvol
}

func (cluster *Cluster) OpenSVCGetJobsVolumeSecret() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-sec"
	svcvol["type"] = "shm"
	svcvol["size"] = "1m"
	svcvol["secrets"] = "env/MYSQL_ROOT_PASSWORD:/"
	svcvol["user"] = "99"
	svcvol["perm"] = "600"
	svcvol["dirperm"] = "700"
	return svcvol
}

/*func (cluster *Cluster) OpenSVCGetVolumeSystemSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-system"
	svcvol["pool"] = cluster.Conf.ProvVolumeSystem
	svcvol["size"] = cluster.Conf.ProvDiskSystemSize + "g"
	return svcvol
}*/

func (cluster *Cluster) OpenSVCGetVolumeDockerSection() map[string]string {
	svcvol := make(map[string]string)
	svcvol["name"] = "{name}-docker"
	svcvol["pool"] = cluster.Conf.ProvVolumeDocker
	svcvol["size"] = cluster.Conf.ProvDiskDockerSize + "g"
	return svcvol
}

func (server *ServerMonitor) GenerateDBTemplateV2() ([]byte, error) {

	svcsection := server.GenerateDBTemplateMap()

	svcsectionJson, err := json.MarshalIndent(svcsection, "", "\t")
	if err != nil {
		return []byte(""), err
	}

	return svcsectionJson, nil
}

func (server *ServerMonitor) GenerateDBTemplateV3() ([]byte, error) {

	svcsection := server.GenerateDBTemplateMap()
	if !server.ClusterGroup.Conf.ProvDBDockerRunArgsLimit {
		// The container cap lives on the om3 PG SLICE, not the docker scope (see
		// WARN0214): same memory ceiling the docker run-args carried (tier + 1 DBU
		// headroom) plus the cpu quota, so a live pg update can move BOTH axes.
		// om3 syntax only (v3 template): "<cores*100>%" -- see OpenSVCCPUQuotaKeyword.
		svcsection["DEFAULT"]["pg_mem_limit"] = strconv.FormatInt(int64(server.ClusterGroup.GetDBContainerMemoryCapMB())*1024*1024, 10)
		if q := OpenSVCCPUQuotaKeyword(server.ClusterGroup.GetDBContainerCPUCapCores()); q != "" {
			svcsection["DEFAULT"]["pg_cpu_quota"] = q
		}
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
	_, err := cfg.WriteTo(&buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil

}

func (server *ServerMonitor) GenerateDBTemplateMap() map[string]map[string]string {

	svcsection := make(map[string]map[string]string)
	// A queue-full registry verdict is deliberately not cached, so resolve once and use that answer throughout this
	// template. Otherwise a later call could add a helper without the matching volume directory or jobs mount.
	xtrabackupImage := server.ClusterGroup.xtrabackupBundleImage()
	svcsection["DEFAULT"] = server.OpenSVCGetDBDefaultSection()
	svcsection["ip#01"] = server.ClusterGroup.OpenSVCGetNetSection()
	if server.ClusterGroup.Conf.ProvDiskType != "volume" {
		if server.ClusterGroup.Conf.ProvDiskType != "pool" {

			svcsection["disk#0000"] = server.ClusterGroup.OpenSVCGetDiskZpoolDockerPrivateSection()
			svcsection["disk#00"] = server.ClusterGroup.OpenSVCGetDiskLoopbackDockerPrivateSection()
			svcsection["disk#01"] = server.ClusterGroup.OpenSVCGetDiskLoopbackPodSection()
			svcsection["disk#0001"] = server.ClusterGroup.OpenSVCGetDiskLoopbackSnapshotPodSection()
		}
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcsection["fs#00"] = server.ClusterGroup.OpenSVCGetFSDockerPrivateSection()
		}

		svcsection["fs#01"] = server.ClusterGroup.OpenSVCGetFSPodSection()
		svcsection["fs#03"] = server.ClusterGroup.OpenSVCGetFSTmpSection()
		if server.ClusterGroup.Conf.ProvDiskSnapshot {
			svcsection["sync#01"] = server.OpenSVCGetZFSSnapshotSection()
			svcsection["task#02"] = server.OpenSVCGetTaskZFSSnapshotSection()
		}
	} else {
		if server.ClusterGroup.Conf.ProvDockerDaemonPrivate {
			svcsection["volume#00"] = server.ClusterGroup.OpenSVCGetVolumeDockerSection()
		}
		svcsection["volume#01"] = server.ClusterGroup.openSVCGetVolumeDataSection(xtrabackupImage)
		//	svcsection["volume#02"] = server.ClusterGroup.OpenSVCGetVolumeSystemSection()
		//	svcsection["volume#03"] = server.ClusterGroup.OpenSVCGetVolumeTempSection()
	}
	svcsection["container#01"] = server.ClusterGroup.OpenSVCGetNamespaceContainerSection(server.ClusterGroup.dbStartTimeout())
	svcsection["container#02"] = server.ClusterGroup.OpenSVCGetDBInitContainerSection(server.Port)
	// only a complete section: an empty one would leave a resource without a type in the service
	if section := server.ClusterGroup.openSVCGetXtrabackupBundleContainerSection(xtrabackupImage); len(section) > 0 {
		svcsection["container#03"] = section
	}
	svcsection["container#db"] = server.OpenSVCGetDBContainerSection()
	svcsection["volume#02"] = server.ClusterGroup.OpenSVCGetJobsVolumeSecret()
	svcsection["container#jobs"] = server.openSVCGetJobsContainerSection(xtrabackupImage)

	//	svcsection["task#01"] = server.ClusterGroup.OpenSVCGetTaskJobsSection()
	svcsection["env"] = server.OpenSVCGetDBEnvSection()

	return svcsection
}

func (server *ServerMonitor) GenerateDBTemplate(collector opensvc.Collector, servers []string, ports []string, agents []opensvc.Host, name string, agent string) (string, error) {

	ipPods := ""
	portPods := ""

	conf := ""
	//if zfs snap
	if collector.ProvFSPool == "zpool" && server.ClusterGroup.GetConf().AutorejoinZFSFlashback && server.IsPrefered() {

		conf = `
[DEFAULT]
nodes = {env.nodes}
cluster_type = failover
rollback = true
orchestrate = start
`
	} else {
		conf = `
[DEFAULT]
nodes = {env.nodes}
flex_primary = {env.nodes[0]}
topology = flex
rollback = false
`
	}
	conf += "app = " + server.ClusterGroup.Conf.ProvCodeApp
	conf = conf + server.ClusterGroup.GetDockerDiskTemplate(collector)
	//main loop over db instances
	for i, host := range servers {
		pod := fmt.Sprintf("%02d", i+1)
		conf = conf + server.ClusterGroup.GetPodDiskTemplate(collector, pod, agent)
		conf = conf + server.GetInitContainer(collector)
		//		conf = conf + `post_provision =  {svcmgr} -s  {svcpath} push status;{svcmgr} -s {svcpath} compliance fix --attach --moduleset mariadb.svc.mrm.db;
		//	`
		conf = conf + server.GetSnapshot(collector)
		conf = conf + server.ClusterGroup.GetPodNetTemplate(collector, pod, i)
		conf = conf + server.GetPodDockerDBTemplate(collector, pod, i)
		conf = conf + server.ClusterGroup.GetPodPackageTemplate(collector, pod)
		ipPods = ipPods + `ip_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + host + `
	`
		portPods = portPods + `port_pod` + fmt.Sprintf("%02d", i+1) + ` = ` + ports[i] + `
	`
	}

	conf = conf + `[task#01]
schedule = @1
command = svcmgr -s {svcpath} docker exec -i {namespace}..{svcname}.container.2001 /bin/bash /docker-entrypoint-initdb.d/dbjobs
user = root
run_requires = fs#01(up,stdby up) container#01(up,stdby up)

`
	ips := strings.Split(collector.ProvNetGateway, ".")
	masks := strings.Split(collector.ProvNetMask, ".")
	for i, mask := range masks {
		if mask == "0" {
			ips[i] = "0"
		}
	}
	network := strings.Join(ips, ".")
	conf = conf + `
[env]
nodes = ` + agent + `
size = ` + server.ClusterGroup.provDiskSizeForOpenSVC() + `
docker_imgage = ` + collector.ProvDockerImg + `
` + ipPods + `
` + portPods + `
mysql_root_password = ` + server.ClusterGroup.GetDbPass() + `
mysql_root_user = ` + server.ClusterGroup.GetDbUser() + `
network = ` + network + `
gateway =  ` + collector.ProvNetGateway + `
netmask =  ` + collector.ProvNetMask + `
base_dir = /srv/{namespace}-{svcname}
max_iops = ` + collector.ProvIops + `
max_mem = ` + collector.ProvMem + `
max_cores = ` + collector.ProvCores + `
micro_srv = ` + collector.ProvMicroSrv + `
gcomm	 = ` + server.ClusterGroup.GetGComm() + `
mrm_api_addr = ` + server.ClusterGroup.Conf.MonitorAddress + ":" + server.ClusterGroup.Conf.HttpPort + `
mrm_cluster_name = ` + server.ClusterGroup.GetClusterName() + `
safe_ssl_ca_uuid = ` + server.ClusterGroup.Conf.ProvSSLCaUUID + `
safe_ssl_cert_uuid = ` + server.ClusterGroup.Conf.ProvSSLCertUUID + `
safe_ssl_key_uuid = ` + server.ClusterGroup.Conf.ProvSSLKeyUUID + `
server_id = ` + string(server.Id[2:10]) + `

`
	server.ClusterGroup.LogModulePrintf(server.ClusterGroup.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlDbg, conf)

	return conf, nil
}

func (server *ServerMonitor) GetInitContainer(collector opensvc.Collector) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#02]
detach = false
type = docker
image = alpine
netns = container#01
rm = true
start_timeout = 30s
volume_mounts = /etc/localtime:/etc/localtime:ro {env.base_dir}/pod01:/data
command = sh -c 'wget -qO- http://{env.mrm_api_addr}/api/clusters/{env.mrm_cluster_name}/servers/{env.ip_pod01}/{env.port_pod01}/config|tar xzvf - -C /data'

 `
	}
	return vm
}

func (server *ServerMonitor) GetPodDockerDBTemplate(collector opensvc.Collector, pod string, i int) string {
	var vm string
	if collector.ProvMicroSrv == "docker" {
		vm = vm + `
[container#00` + pod + `]
type = docker
hostname = {svcname}.{namespace}.svc.{clustername}
image = ghcr.io/opensvc/pause
rm = true


[container#20` + pod + `]
tags = pod` + pod + `
type = docker
rm = true
netns = container#01
run_image = {env.docker_image}
run_args = -e MYSQL_ROOT_PASSWORD={env.mysql_root_password}
 -e MYSQL_INITDB_SKIP_TZINFO=yes
 -v /etc/localtime:/etc/localtime:ro
 -v {env.base_dir}/data:/var/lib/mysql:rw
 -v {env.base_dir}/etc/mysql:/etc/mysql:rw
 -v {env.base_dir}/init:/docker-entrypoint-initdb.d:rw

`
		if server.ClusterGroup.GetTopology() == config.TopoMultiMasterWsrep && server.ClusterGroup.TopologyClusterDown() {
			//Proceed with galera specific
			if server.ClusterGroup.GetMaster() == nil {
				server.ClusterGroup.vmaster = server
				vm = vm + `run_command = mysqld --wsrep_new_cluster
`
			}
		}
	}
	return vm
}

// deployImage is the image a deployment render uses: the running image a rolling restart
// keeps on the server (#1861), else the explicit release prov-db-image resolved to
// (cluster.deployImage, #1862).
func (server *ServerMonitor) deployImage() string {
	if server.DeployImageOverride != "" {
		return server.DeployImageOverride
	}
	return server.ClusterGroup.deployImage()
}

// provDiskSizeForOpenSVC is prov-db-disk-size as an OpenSVC size: the setting carries its
// unit ("20G"), the templates used to append a hard-coded "g" to a bare number, and
// "20Gg" gave the volume a size of 0 (ZFS then refused quota=0 and the provisioning of
// mahebourg failed, 2026-10-06). Gigabytes, rounded down, at least 1.
func (cluster *Cluster) provDiskSizeForOpenSVC() string {
	gb, err := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	if err != nil || gb < 1 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlWarn, "prov-db-disk-size %q is not a size (%v): 1g used", cluster.Conf.ProvDisk, err)
		gb = 1
	}
	return strconv.Itoa(gb) + "g"
}

// startTimeoutOrDefault: the configured container start timeout, 2m when unset.
func startTimeoutOrDefault(v string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return "2m"
}

// dbStartTimeout is the start timeout written on the database containers and their jobs
// sidecar: prov-db-start-timeout, 2m when unset. The orchestrator's own default is 5s,
// which a container whose image was purged exceeds (#1924). The image pull has its own
// pull_timeout in the orchestrator (2m by default).
func (cluster *Cluster) dbStartTimeout() string {
	return startTimeoutOrDefault(cluster.Conf.ProvDbStartTimeout)
}

// sensorStartTimeout: the sensor sidecar follows its kind's container start timeout.
func (cluster *Cluster) sensorStartTimeout(kind string) string {
	switch kind {
	case string(KindProxy):
		return cluster.proxyStartTimeout()
	case string(KindApp):
		return startTimeoutOrDefault(cluster.Conf.ProvAppStartTimeout)
	}
	return cluster.dbStartTimeout()
}

// proxyStartTimeout is the same for the proxy containers: prov-proxy-start-timeout (#1924).
func (cluster *Cluster) proxyStartTimeout() string {
	return startTimeoutOrDefault(cluster.Conf.ProvProxyStartTimeout)
}
