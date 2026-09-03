// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"os/exec"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) UnprovisionDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbCleanupScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbCleanupScript, misc.Unbracket(server.Host), server.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) ProvisionDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbBootstrapScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbBootstrapScript, misc.Unbracket(server.Host), server.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

// RunDynamicResourceChangeScript calls the client-overridable
// prov-db-dynamic-resource-change-script to resize the four provisioned resources
// (mem, cpu, disk, io) of a RUNNING server live — the real cgroup / disk / io
// change repman cannot do itself. Per F7 it is a client OVERRIDE that applies in
// ALL orchestrator cases (OpenSVC / K8s included): when set it takes precedence
// over the native orchestrator resize.
//
// The four target resource values and the direction are passed as environment
// variables on top of GetExecEnv (which carries the API credentials via env,
// never argv). Only non-secret context (host, port, direction, cluster) is argv.
// Directional sequencing is the caller's (ResizeDynamicResources).
func (cluster *Cluster) RunDynamicResourceChangeScript(server *ServerMonitor, grow bool) error {
	if cluster.Conf.ProvDBDynamicResourceChangeScript == "" {
		return nil
	}
	direction := "shrink"
	if grow {
		direction = "grow"
	}
	cfg := &cluster.Configurator
	scriptCmd := exec.Command(cluster.Conf.ProvDBDynamicResourceChangeScript, misc.Unbracket(server.Host), server.Port, direction, cluster.Name)
	scriptCmd.Env = append(cluster.GetExecEnv(),
		"REPMAN_RESIZE_DIRECTION="+direction,
		"REPMAN_PROV_DB_MEMORY="+cfg.GetConfigDBMemory(),
		"REPMAN_PROV_DB_CORES="+cfg.GetConfigDBCores(),
		"REPMAN_PROV_DB_DISK_SIZE="+cfg.GetConfigDBDisk(),
		"REPMAN_PROV_DB_DISK_IOPS="+cfg.GetConfigDBDiskIOPS(),
		"REPMAN_SERVER_HOST="+misc.Unbracket(server.Host),
		"REPMAN_SERVER_PORT="+server.Port,
	)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo,
		"Dynamic resource change script (%s) on %s", direction, server.URL)

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "%s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StopDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbStopScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbStopScript, misc.Unbracket(server.Host), server.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StartDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbStartScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbStartScript, misc.Unbracket(server.Host), server.Port, cluster.GetDbUser(), cluster.GetDbPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.GetDbPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) UnprovisionProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyCleanupScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyCleanupScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) ProvisionProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyBootstrapScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyBootstrapScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StartProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyStartScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyStartScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StopProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyStopScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyStopScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	go func() {
		defer wg.Done()
		cluster.CopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlDbg, server.GetName())
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}
