// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package cluster

import (
	"os"
)

func (server *ServerMonitor) DelProvisionCookie() {
	err := os.Remove(server.Datadir + "/@cookie_prov")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie %s", err)
	}
}

func (server *ServerMonitor) DelWaitStartCookie() {
	err := os.Remove(server.Datadir + "/@cookie_waitstart")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie %s", err)
	}
}

func (server *ServerMonitor) DelWaitStopCookie() {
	err := os.Remove(server.Datadir + "/@cookie_waitstop")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie %s", err)
	}
}

func (server *ServerMonitor) DelReprovisionCookie() {
	err := os.Remove(server.Datadir + "/@cookie_reprov")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie %s", err)
	}
}

func (server *ServerMonitor) DelRestartCookie() {
	err := os.Remove(server.Datadir + "/@cookie_restart")
	if err != nil {
		server.ClusterGroup.LogPrintf(LvlDbg, "Remove cookie %s", err)
	}
}
