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
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
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
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
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
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
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
		server.copyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, "PROV_DB")
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
		cluster.provCopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
	}()
	go func() {
		defer wg.Done()
		cluster.provCopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
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
		cluster.provCopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
	}()
	go func() {
		defer wg.Done()
		cluster.provCopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
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
		cluster.provCopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
	}()
	go func() {
		defer wg.Done()
		cluster.provCopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
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
		cluster.provCopyLogs(stdoutIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
	}()
	go func() {
		defer wg.Done()
		cluster.provCopyLogs(stderrIn, config.ConstLogModOrchestrator, config.LvlInfo, server.GetName(), "PROV_PRX")
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModOrchestrator, config.LvlErr, " %s", err)
		return err
	}
	return nil
}
