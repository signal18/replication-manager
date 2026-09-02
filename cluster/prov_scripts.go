// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
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

// ChangePlanScript is the client-overridable service-plan (DBU) change gate,
// the bash-hook counterpart of the built-in CanChangePlan check. It is passed
// every argument needed to reproduce the change — cluster, credentials, the
// from/to plan and the target plan's specs — so a client script can validate,
// log, or reproduce it. A non-zero exit refuses the plan change. Empty config
// is a no-op (the built-in immutable/preserved gate applies instead).
func (cluster *Cluster) ChangePlanScript(fromPlan string, toPlan string) error {
	if cluster.Conf.ProvDbChangePlanScript == "" {
		return nil
	}

	// Resolve the target plan's specs so the script has full context.
	var mem, cores, disk, iops string
	for _, plan := range cluster.GetServicePlans() {
		if plan.Plan == toPlan {
			mem = strconv.Itoa(plan.DbMemory)
			cores = strconv.Itoa(plan.DbCores)
			disk = strconv.Itoa(plan.DbDataSize)
			iops = strconv.Itoa(plan.DbIops)
			break
		}
	}

	// Credentials go through the environment, never argv (argv is world-visible
	// in the process list and would leak into logs). Plan + specs stay as args.
	scriptCmd := exec.Command(cluster.Conf.ProvDbChangePlanScript, cluster.Name, fromPlan, toPlan, mem, cores, disk, iops)
	scriptCmd.Env = append(os.Environ(),
		"REPLICATION_MANAGER_CLUSTER="+cluster.Name,
		"REPLICATION_MANAGER_DB_USER="+cluster.GetDbUser(),
		"REPLICATION_MANAGER_DB_PASSWORD="+cluster.GetDbPass(),
		"REPLICATION_MANAGER_FROM_PLAN="+fromPlan,
		"REPLICATION_MANAGER_TO_PLAN="+toPlan,
	)
	cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "%s", scriptCmd.String())

	out, err := scriptCmd.CombinedOutput()
	if len(out) > 0 {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlInfo, "prov-db-change-plan-script output: %s", strings.TrimSpace(string(out)))
	}
	if err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, "Service plan change refused by %s: %s", cluster.Conf.ProvDbChangePlanScript, err)
		return errors.New("plan change refused by prov-db-change-plan-script: " + err.Error())
	}
	return nil
}
