// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package cluster

import (
	"bytes"
	"net"
	"os"
	"runtime"
	"strconv"
)

func readPidFromFile(pidfile string) (string, error) {
	d, err := os.ReadFile(pidfile)
	if err != nil {
		return "", err
	}
	return string(bytes.TrimSpace(d)), nil
}

func (cluster *Cluster) LocalhostGetNodes() ([]Agent, error) {
	var info runtime.MemStats
	runtime.ReadMemStats(&info)

	name, err := os.Hostname()
	if err != nil {
		name = "127.0.0.1"
	}
	agents := []Agent{}
	/*	m.Alloc = rtm.Alloc
		m.TotalAlloc = rtm.TotalAlloc
		m.Sys = rtm.Sys
		m.Mallocs = rtm.Mallocs
		m.Frees = rtm.Frees
	*/

	var agent Agent
	agent.Id = "1"
	agent.OsName = cluster.Conf.GoOS
	agent.OsKernel = cluster.Conf.GoArch
	agent.CpuCores = int64(runtime.NumCPU())
	agent.CpuFreq = 0
	agent.MemBytes = int64(info.Sys)
	agent.HostName = name
	agents = append(agents, agent)

	return agents, nil
}

func (cluster *Cluster) LocalhostGetFreePort() (string, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return "", err
	}
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return "", err
	}
	port := strconv.Itoa(listener.Addr().(*net.TCPAddr).Port)
	defer listener.Close()
	return port, nil
}
