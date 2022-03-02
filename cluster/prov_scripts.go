// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bufio"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/signal18/replication-manager/utils/misc"
)

func (cluster *Cluster) UnprovisionDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbCleanupScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbCleanupScript, misc.Unbracket(server.Host), server.Port, cluster.dbUser, cluster.dbPass, cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.dbPass, "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) ProvisionDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbBootstrapScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbBootstrapScript, misc.Unbracket(server.Host), server.Port, cluster.dbUser, cluster.dbPass, cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.dbPass, "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StopDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbStopScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbStopScript, misc.Unbracket(server.Host), server.Port, cluster.dbUser, cluster.dbPass, cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.dbPass, "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StartDatabaseScript(server *ServerMonitor) error {
	if cluster.Conf.ProvDbStartScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvDbStartScript, misc.Unbracket(server.Host), server.Port, cluster.dbUser, cluster.dbPass, cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), cluster.dbPass, "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		server.copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		server.copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) UnprovisionProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyCleanupScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyCleanupScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()

	copyLogs := func(r io.Reader) {
		//	buf := make([]byte, 1024)
		s := bufio.NewScanner(r)
		for {
			if !s.Scan() {
				break
			} else {
				cluster.LogPrintf(LvlInfo, "%s", s.Text())
			}
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) ProvisionProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyBootstrapScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyBootstrapScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	copyLogs := func(r io.Reader) {
		//	buf := make([]byte, 1024)
		s := bufio.NewScanner(r)
		for {
			if !s.Scan() {
				break
			} else {
				cluster.LogPrintf(LvlInfo, "%s", s.Text())
			}
		}
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StartProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyStartScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyStartScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	copyLogs := func(r io.Reader) {
		//	buf := make([]byte, 1024)
		s := bufio.NewScanner(r)
		for {
			if !s.Scan() {
				break
			} else {
				cluster.LogPrintf(LvlInfo, "%s", s.Text())
			}
		}
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}

func (cluster *Cluster) StopProxyScript(server DatabaseProxy) error {
	if cluster.Conf.ProvProxyStopScript == "" {
		return nil
	}
	scriptCmd := exec.Command(cluster.Conf.ProvProxyStopScript, misc.Unbracket(server.GetHost()), server.GetPort(), server.GetUser(), server.GetPass(), cluster.Name)
	cluster.LogPrintf(LvlInfo, "%s", strings.Replace(scriptCmd.String(), server.GetPass(), "XXXX", 1))

	stdoutIn, _ := scriptCmd.StdoutPipe()
	stderrIn, _ := scriptCmd.StderrPipe()
	scriptCmd.Start()
	var wg sync.WaitGroup
	copyLogs := func(r io.Reader) {
		//	buf := make([]byte, 1024)
		s := bufio.NewScanner(r)
		for {
			if !s.Scan() {
				break
			} else {
				cluster.LogPrintf(LvlInfo, "%s", s.Text())
			}
		}
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		copyLogs(stdoutIn)
	}()
	go func() {
		defer wg.Done()
		copyLogs(stderrIn)
	}()
	wg.Wait()
	if err := scriptCmd.Wait(); err != nil {
		cluster.LogPrintf(LvlErr, " %s", err)
		return err
	}
	return nil
}
