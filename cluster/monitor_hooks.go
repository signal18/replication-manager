// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

// MonitorHook is what the two generic lifecycle scripts receive (#1858): one
// contract for the three monitor kinds. Type is database, proxy or app; Name is
// host:port for a database or proxy and the app name for an app; Version is the
// declared image tag at add time and the observed version at drop time; Flavor is
// the engine or proxy or template; the resources are the declared axes of the kind
// and Units the whole units they amount to (dbu for a database, apu otherwise).
type MonitorHook struct {
	Type    string
	Name    string
	Host    string
	Port    string
	Version string
	Flavor  string
	Cores   string
	MemMB   string
	DiskGB  string
	Iops    string
	Unit    string
	Units   string
}

// monitorHookScriptTimeout bounds a lifecycle script: a hang is a veto on add,
// a logged failure on drop, never a stuck API call.
const monitorHookScriptTimeout = 30 * time.Second

// imageRepoTag splits "repo/name:tag" into its repo and tag.
func imageRepoTag(img string) (repo, tag string) {
	img = strings.TrimSpace(img)
	if i := strings.LastIndex(img, ":"); i > 0 && !strings.Contains(img[i:], "/") {
		return img[:i], img[i+1:]
	}
	return img, ""
}

func lastPathPart(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// monitorHookDatabase describes a database monitor: at add time srv is nil and the
// version is the declared prov-db-image tag; at drop time the observed one.
func (cluster *Cluster) monitorHookDatabase(host, port string, srv *ServerMonitor) MonitorHook {
	repo, tag := imageRepoTag(cluster.Conf.ProvDbImg)
	h := MonitorHook{Type: "database", Name: host + ":" + port, Host: host, Port: port,
		Version: tag, Flavor: lastPathPart(repo),
		Cores: cluster.Conf.ProvCores, Iops: cluster.Conf.ProvIops, Unit: "dbu"}
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvMem, true)
	diskGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvDisk, true)
	h.MemMB = strconv.Itoa(int(memMB))
	h.DiskGB = strconv.Itoa(int(diskGB))
	h.Units = strconv.Itoa(cluster.GetProvDbuFromConfigPerNode())
	if srv != nil && srv.DBVersion != nil {
		h.Version = srv.DBVersion.ToString()
		switch {
		case srv.DBVersion.IsPostgreSQL():
			h.Flavor = "postgres"
		case srv.DBVersion.IsMariaDB():
			h.Flavor = "mariadb"
		default:
			h.Flavor = "mysql"
		}
	}
	return h
}

// monitorHookProxy describes a proxy monitor of the given type (haproxy, maxscale,
// proxysql, shardproxy, ...): the declared proxy image tag at add, the observed
// version at drop.
func (cluster *Cluster) monitorHookProxy(prxType, host, port string, prx DatabaseProxy) MonitorHook {
	img := ""
	switch prxType {
	case config.ConstProxyHaproxy:
		img = cluster.Conf.ProvProxHaproxyImg
	case config.ConstProxyMaxscale:
		img = cluster.Conf.ProvProxMaxscaleImg
	case config.ConstProxySqlproxy:
		img = cluster.Conf.ProvProxProxysqlImg
	case config.ConstProxySpider:
		img = cluster.Conf.ProvProxShardingImg
	case config.ConstProxyMysqlrouter:
		img = cluster.Conf.ProvProxMysqlRouterImg
	}
	_, tag := imageRepoTag(img)
	h := MonitorHook{Type: "proxy", Name: host + ":" + port, Host: host, Port: port,
		Version: tag, Flavor: prxType, Cores: cluster.Conf.ProvProxCores, Unit: "apu"}
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cluster.Conf.ProvProxMem, true)
	diskGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cluster.Conf.ProvProxDisk, true)
	h.MemMB = strconv.Itoa(int(memMB))
	h.DiskGB = strconv.Itoa(int(diskGB))
	if cluster.resources != nil {
		h.Units = strconv.Itoa(int(cluster.computePlanAPUReading(time.Now(), cluster.Conf.ProvProxMem, cluster.Conf.ProvProxCores, cluster.Conf.ProvProxDisk).Apu + 0.5))
	}
	if prx != nil {
		if v := strings.TrimSpace(prx.GetVersion()); v != "" {
			h.Version = v
		}
	}
	return h
}

// monitorHookApp describes an app monitor from its config: the docker image tag
// and the per-agent shape; the name is host:port at add time (no App object yet)
// and the app's name at drop time, with the version the app reported, if any.
func (cluster *Cluster) monitorHookApp(cnf *config.AppConfig, app *App) MonitorHook {
	if cnf == nil {
		return MonitorHook{Type: "app"}
	}
	repo, tag := imageRepoTag(cnf.ProvAppDockerImg)
	name := cnf.AppHost + ":" + cnf.AppPort
	if app != nil {
		name = app.GetName()
	}
	h := MonitorHook{Type: "app", Name: name, Host: cnf.AppHost, Port: cnf.AppPort,
		Version: tag, Flavor: cnf.ProvAppTemplate, Cores: cnf.ProvAppCpuCores, Iops: cnf.ProvAppDiskIops, Unit: "apu"}
	if h.Flavor == "" {
		h.Flavor = lastPathPart(repo)
	}
	memMB, _ := config.ParseUnitMeasurementToInt("M,bytes,required", cnf.ProvAppMem, true)
	diskGB, _ := config.ParseUnitMeasurementToInt("G,bytes,required", cnf.ProvAppDisk, true)
	h.MemMB = strconv.Itoa(int(memMB))
	h.DiskGB = strconv.Itoa(int(diskGB))
	if cluster.resources != nil {
		h.Units = strconv.Itoa(int(cluster.computePlanAPUReading(time.Now(), cnf.ProvAppMem, cnf.ProvAppCpuCores, cnf.ProvAppDisk).Apu + 0.5))
	}
	if app != nil && strings.TrimSpace(app.Version) != "" {
		h.Version = strings.TrimSpace(app.Version)
	}
	return h
}

// RunAddMonitorScript fires monitoring-add-monitor-script BEFORE a monitor is added.
// A non-zero exit or a timeout vetoes the add; the first output line (or the error)
// is the reason the caller returns. An empty setting allows.
func (cluster *Cluster) RunAddMonitorScript(h MonitorHook) error {
	script := strings.TrimSpace(cluster.Conf.MonitoringAddMonitorScript)
	if script == "" {
		return nil
	}
	out, err := cluster.runMonitorHookScript(script, "add", h)
	if err == nil {
		return nil
	}
	reason := firstLine(out)
	if reason == "" {
		reason = err.Error()
	}
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
		"monitoring-add-monitor-script refused the %s monitor %s: %s", h.Type, h.Name, reason)
	return fmt.Errorf("monitoring-add-monitor-script refused the %s monitor %s: %s", h.Type, h.Name, reason)
}

// RunDropMonitorScript fires monitoring-drop-monitor-script AFTER a monitor is gone.
// Informative only: a failure is logged, the drop stands.
func (cluster *Cluster) RunDropMonitorScript(h MonitorHook) {
	script := strings.TrimSpace(cluster.Conf.MonitoringDropMonitorScript)
	if script == "" {
		return
	}
	if out, err := cluster.runMonitorHookScript(script, "drop", h); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlWarn,
			"monitoring-drop-monitor-script failed on the %s monitor %s: %v (%s)", h.Type, h.Name, err, firstLine(out))
	}
}

// runMonitorHookScript runs one lifecycle script with the shared contract: argv =
// cluster, type, name, version, units; env = REPMAN_MONITOR_* and REPMAN_RESOURCE_*
// over the cluster exec env (API URL and admin credentials).
func (cluster *Cluster) runMonitorHookScript(script, phase string, h MonitorHook) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), monitorHookScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, script, cluster.Name, h.Type, h.Name, h.Version, h.Units)
	cmd.Env = append(cluster.GetExecEnv(),
		"REPMAN_CLUSTER="+cluster.Name,
		"REPMAN_MONITOR_PHASE="+phase,
		"REPMAN_MONITOR_TYPE="+h.Type,
		"REPMAN_MONITOR_NAME="+h.Name,
		"REPMAN_MONITOR_HOST="+misc.Unbracket(h.Host),
		"REPMAN_MONITOR_PORT="+h.Port,
		"REPMAN_MONITOR_VERSION="+h.Version,
		"REPMAN_MONITOR_FLAVOR="+h.Flavor,
		"REPMAN_RESOURCE_CORES="+h.Cores,
		"REPMAN_RESOURCE_MEMORY_MB="+h.MemMB,
		"REPMAN_RESOURCE_DISK_GB="+h.DiskGB,
		"REPMAN_RESOURCE_IOPS="+h.Iops,
		"REPMAN_RESOURCE_UNIT="+h.Unit,
		"REPMAN_RESOURCE_UNITS="+h.Units,
	)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo,
		"monitoring-%s-monitor-script on %s monitor %s (version %q, %s %s)", phase, h.Type, h.Name, h.Version, h.Units, h.Unit)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return string(out), fmt.Errorf("did not answer within %s", monitorHookScriptTimeout)
	}
	return string(out), err
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
