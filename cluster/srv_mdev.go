// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//
//	Stephane Varoqui  <svaroqui@gmail.com>
//
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package cluster

import (
	"fmt"
	"strings"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

func (server *ServerMonitor) CheckMDevIssues() {

	cluster := server.ClusterGroup

	if server.MDevIssues.Replication == nil {
		server.MDevIssues.Replication = make([]string, 0)
		server.MDevIssues.Service = make([]string, 0)
	}

	if !server.IsSuspect() && !server.IsFailed() {
		chkf := func(key string, issue *config.MDevIssue) bool {
			server.SearchMDevIssue(issue)

			//Always true
			return true
		}
		cluster.MDevIssues.Callback(chkf)
		server.IsCheckedForMDevIssues = true
	}
}

func (server *ServerMonitor) SearchMDevIssue(issue *config.MDevIssue) bool {
	var hasIssue bool
	cluster := server.ClusterGroup
	ver := server.DBVersion
	strState := strings.Replace(issue.Key, "-", "", 1)
	mdstate := state.State{
		ErrType:   "WARNING",
		ErrFrom:   "MDEV",
		ServerUrl: server.URL,
	}
	// Will also check unresolved cases
	if ver.GreaterEqualReleaseList(issue.Versions...) && (issue.Status == "Unresolved" || ver.LowerReleaseList(issue.FixVersions...)) {

		//Blocker Area (Can break replication integrity)
		switch issue.Key {
		case "MDEV-27512":
			// if server.Variables.Get(strings.ToUpper("slave_skip_errors")) == "ALL" {
			// 	server.MDevIssues.Replication = append(server.MDevIssues.Replication, issue.Key)
			// 	mdstate.ErrDesc = fmt.Sprintf(config.BugString, issue.GetURL())
			// 	cluster.SetState(strState, mdstate)
			// }
			server.MDevIssues.Replication = append(server.MDevIssues.Replication, issue.Key)
			mdstate.ErrDesc = fmt.Sprintf(config.BugString, issue.GetURL())
			cluster.SetState(strState, mdstate)
		}

		//Critical Area (Can affect replication or service due to locking/crash)
		switch issue.Key {
		case "MDEV-31779":
			server.MDevIssues.Service = append(server.MDevIssues.Service, issue.Key)
		}
	}

	return hasIssue
}
