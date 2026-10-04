// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package repmanmcp

import (
	"context"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
)

// appView is what the app tools answer per app: identity, where it runs, its URL.
func appView(a *cluster.App) map[string]any {
	template := ""
	if a.AppConfig != nil {
		template = a.AppConfig.ProvAppTemplate
	}
	return map[string]any{
		"id": a.Id, "name": a.Name, "type": a.Type, "host": a.Host, "port": a.Port,
		"state": a.State, "template": template, "image": func() string {
			if a.AppConfig != nil {
				return a.AppConfig.ProvAppDockerImg
			}
			return ""
		}(),
		"url": a.GetPublicURL(),
		"db": func() any { // the database the app asked the cluster for (#1870), nil otherwise
			if a.AppConfig == nil || !a.AppConfig.AppDbAutoCreate {
				return nil
			}
			return map[string]any{"schema": a.AppConfig.AppDbSchema, "user": a.AppConfig.AppDbUser, "owned": a.AppConfig.AppDbOwned, "error": a.DbProvisionError}
		}(),
		"provisioned": a.HasProvisionCookie(), "running": a.IsRunning(),
	}
}

// registerAppTools: the generic app deployment, one tool = one REST route.
func (s *MCPServer) registerAppTools() {
	s.addTool(
		mcp.NewTool("list-app-templates",
			mcp.WithDescription("List the app templates this replication-manager can deploy on a cluster: the template repository (prov-app-template-repo) and the cluster's local templates. A name is a template path such as phpmyadmin/phpmyadmin; app-add takes it, or the short name when it is unambiguous."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			return mcp.NewToolResultText(toJSON(map[string]any{"cluster": cl.Name, "templates": s.repman.ListAppTemplates(cl)})), nil
		},
	)
	s.addTool(
		mcp.NewTool("list-cluster-apps",
			mcp.WithDescription("List the applications of a cluster with their state, template, image, provisioning state and URL (https on the primary route of the app once provisioned, the internal address otherwise)."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			out := []map[string]any{}
			for _, a := range cl.GetAppsCopy() {
				if a != nil {
					out = append(out, appView(a))
				}
			}
			return mcp.NewToolResultText(toJSON(map[string]any{"cluster": cl.Name, "apps": out})), nil
		},
	)
	s.addTool(
		mcp.NewTool("app-add",
			mcp.WithDescription("Add an application to a cluster from a template (list-app-templates): the app is declared with its name and port and its service definition is generated from the template; it runs once app-provision is called. The answer gives the URL the app will answer on. A template that does not exist is refused."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("template", mcp.Required(), mcp.Description("Template path (phpmyadmin/phpmyadmin) or short name (phpmyadmin)")),
			mcp.WithString("name", mcp.Description("Name of the app, default the template's short name followed by 1 (phpmyadmin1)")),
			mcp.WithString("port", mcp.Description("Port the app listens on, default the template's app-port")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			templates := s.repman.ListAppTemplates(cl)
			template := resolveAppTemplate(req.GetString("template", ""), templates)
			if template == "" {
				return mcp.NewToolResultError("template " + req.GetString("template", "") + " not available (templates: " + strings.Join(templates, ", ") + ")"), nil
			}
			short := template
			if k := strings.LastIndex(short, "/"); k >= 0 {
				short = short[k+1:]
			}
			name := strings.TrimSpace(req.GetString("name", ""))
			if name == "" {
				name = short + "1"
			}
			port := strings.TrimSpace(req.GetString("port", "")) // "" = the template's app-port
			if err := cl.AddSeededApp(name, port, "", template); err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			a := appByNameOrID(cl, name)
			out := map[string]any{"cluster": cl.Name, "name": name, "template": template, "port": port, "status": "added, call app-provision to run it"}
			if a != nil {
				out["app"] = appView(a)
			}
			return mcp.NewToolResultText(toJSON(out)), nil
		},
	)
	for _, action := range []string{"provision", "unprovision"} {
		action := action
		s.addTool(
			mcp.NewTool("app-"+action,
				mcp.WithDescription(map[string]string{
					"provision":   "Provision an application of the cluster on the orchestrator from its service definition (container, routes, DNS); the URL answers once the app is up. Asynchronous.",
					"unprovision": "Unprovision an application of the cluster: its service is destroyed on the orchestrator, the app stays declared. Asynchronous.",
				}[action]),
				mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
				mcp.WithString("app_name", mcp.Required(), mcp.Description("Name or id of the app (list-cluster-apps)")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
				if errResult != nil {
					return errResult, nil
				}
				if cl.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
					return mcp.NewToolResultError("app " + action + " is implemented for the OpenSVC orchestrator only"), nil
				}
				a := appByNameOrID(cl, req.GetString("app_name", ""))
				if a == nil {
					return mcp.NewToolResultError("app not found: " + req.GetString("app_name", "")), nil
				}
				switch action {
				case "provision":
					// Provisioned and answering: nothing to do (a double call, a retry by a
					// client). A fresh app, or one whose service is gone, provisions.
					// IsRunning is "not Failed": a freshly added app (Suspect, never checked) passes it,
					// so require the checked Running state before calling a double provision.
					if a.HasProvisionCookie() && a.State == cluster.StateAppRunning && !a.HasPendingCheckFailures() {
						return mcp.NewToolResultText(toJSON(map[string]any{"cluster": cl.Name, "app": appView(a), "status": "already provisioned and running, nothing to do"})), nil
					}
					go func() {
						if err := cl.InitAppService(a); err != nil {
							cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "MCP app-provision %s: %s", a.Name, err)
						}
					}()
				case "unprovision":
					go func() {
						if err := cl.OpenSVCUnprovisionAppService(a); err != nil {
							cl.LogModulePrintf(cl.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "MCP app-unprovision %s: %s", a.Name, err)
							return
						}
						cl.ClearAppProvisioned(a)
					}()
				}
				return mcp.NewToolResultText(toJSON(map[string]any{"cluster": cl.Name, "app": appView(a), "status": action + " started"})), nil
			},
		)
	}
}

// registerAppLifecycleTools: start, stop, restart one app on its orchestrator and
// resize its plan; one tool = one REST route (#1870 follow-up, Stéphane 2026-10-02).
func (s *MCPServer) registerAppLifecycleTools() {
	for _, action := range []string{"start", "stop", "restart"} {
		action := action
		s.addTool(
			mcp.NewTool("app-"+action,
				mcp.WithDescription(map[string]string{
					"start":   "Start a provisioned application of the cluster on the orchestrator (all its nodes, or one node). Asynchronous: list-cluster-apps gives the state.",
					"stop":    "Stop a running application of the cluster on the orchestrator (all its nodes, or one node); the service stays provisioned. Asynchronous.",
					"restart": "Restart a running application of the cluster on the orchestrator (all its nodes, or one node). Asynchronous.",
				}[action]),
				mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
				mcp.WithString("app_name", mcp.Required(), mcp.Description("Name or id of the app (list-cluster-apps)")),
				mcp.WithString("node", mcp.Description("One orchestrator node (agent) only; empty = every node of the app")),
			),
			func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
				if errResult != nil {
					return errResult, nil
				}
				if cl.GetOrchestrator() != config.ConstOrchestratorOpenSVC {
					return mcp.NewToolResultError("app " + action + " is implemented for the OpenSVC orchestrator only"), nil
				}
				a := appByNameOrID(cl, req.GetString("app_name", ""))
				if a == nil {
					return mcp.NewToolResultError("app not found: " + req.GetString("app_name", "")), nil
				}
				if !a.HasProvisionCookie() {
					return mcp.NewToolResultError("app " + a.Name + " is not provisioned: call app-provision first"), nil
				}
				node := strings.TrimSpace(req.GetString("node", ""))
				var err error
				switch action {
				case "start":
					err = cl.OpenSVCStartAppService(a, node)
				case "stop":
					err = cl.OpenSVCStopAppService(a, node)
				case "restart":
					err = cl.OpenSVCRestartAppService(a, node, "")
				}
				if err != nil {
					return mcp.NewToolResultError("app " + action + " " + a.Name + ": " + err.Error()), nil
				}
				return mcp.NewToolResultText(toJSON(map[string]any{"cluster": cl.Name, "app": appView(a), "node": node, "status": action + " requested"})), nil
			},
		)
	}

	s.addTool(
		mcp.NewTool("app-resize",
			mcp.WithDescription("Resize the plan of an application: the number of whole units (APU, or DBU for a stateful app) it is sized at; the declared cores, memory and disk follow the infrastructure's ratio. The running service keeps its shape until the app is reprovisioned (app-unprovision then app-provision); the answer says so. Refused in manual sizing mode."),
			mcp.WithString("cluster_name", mcp.Required(), mcp.Description("Name of the cluster")),
			mcp.WithString("app_name", mcp.Required(), mcp.Description("Name or id of the app (list-cluster-apps)")),
			mcp.WithNumber("units", mcp.Required(), mcp.Description("Whole units per instance, >= 1")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			cl, errResult := clusterOrError(s.repman, req.GetString("cluster_name", ""))
			if errResult != nil {
				return errResult, nil
			}
			a := appByNameOrID(cl, req.GetString("app_name", ""))
			if a == nil {
				return mcp.NewToolResultError("app not found: " + req.GetString("app_name", "")), nil
			}
			units := req.GetInt("units", 0)
			if units < 1 {
				return mcp.NewToolResultError("units must be a whole number >= 1"), nil
			}
			if err := a.SetSetting("prov-app-units", strconv.Itoa(units)); err != nil {
				return mcp.NewToolResultError("app-resize " + a.Name + ": " + err.Error()), nil
			}
			if cl.ConfigManager != nil {
				cl.ConfigManager.SaveConfig(cl, false)
			}
			out := map[string]any{"cluster": cl.Name, "app": appView(a), "units": units,
				"cpu_cores": a.AppConfig.ProvAppCpuCores, "memory_mb": a.AppConfig.ProvAppMem, "disk_gb": a.AppConfig.ProvAppDisk,
				"reprov_needed": a.HasReprovCookie(), "status": "plan set"}
			if a.HasProvisionCookie() {
				out["next"] = "the service keeps its current shape: call app-unprovision then app-provision to apply the new one"
			}
			return mcp.NewToolResultText(toJSON(out)), nil
		},
	)
}

// resolveAppTemplate maps a short app name to a template of the list: an exact
// name, "<name>/<name>", or the only template whose last path element is the name.
func resolveAppTemplate(name string, templates []string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	candidates := []string{}
	for _, t := range templates {
		lt := strings.ToLower(t)
		if lt == name || lt == name+"/"+name {
			return t
		}
		if strings.HasSuffix(lt, "/"+name) {
			candidates = append(candidates, t)
		}
	}
	if len(candidates) == 1 {
		return candidates[0]
	}
	return ""
}

// appByNameOrID: the REST routes take the app id, people and the tools use its name.
func appByNameOrID(cl *cluster.Cluster, key string) *cluster.App {
	key = strings.TrimSpace(key)
	apps := cl.GetAppsCopy()
	for _, match := range []func(*cluster.App) bool{
		func(a *cluster.App) bool { return a.Id == key },
		func(a *cluster.App) bool { return a.Name == key },
		func(a *cluster.App) bool { return a.Host == key },
	} {
		for _, a := range apps {
			if a != nil && match(a) {
				return a
			}
		}
	}
	return nil
}
