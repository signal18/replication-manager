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

import "github.com/signal18/replication-manager/config"

func (server *ServerMonitor) CheckMDevIssues() {
	ver := server.DBVersion
	cluster := server.ClusterGroup

	if server.MDevIssues == nil {
		server.MDevIssues = make([]string, 0)
	}

	if !server.IsSuspect() && !server.IsFailed() {
		chkf := func(key string, issue *config.MDevIssue) bool {
			if ver.GreaterEqualReleaseList(issue.Versions...) && ver.LowerReleaseList(issue.FixVersions...) {
				server.MDevIssues = append(server.MDevIssues, key)
			}

			//Always true
			return true
		}
		cluster.MDevIssues.Callback(chkf)
		server.IsCheckedForMDevIssues = true
	}
}
