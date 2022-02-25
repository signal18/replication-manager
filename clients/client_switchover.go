//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"github.com/spf13/cobra"
)

var switchoverCmd = &cobra.Command{
	Use:   "switchover",
	Short: "Perform a master switch",
	Long: `Performs an online master switch by promoting a slave to master
and demoting the old master to slave`,
	Run: func(cmd *cobra.Command, args []string) {
		var slogs []string
		var prefMasterParam RequetParam
		var params []RequetParam

		cliInit(true)
		cliGetTopology()
		if cliPrefMaster != "" {
			prefMasterParam.key = "prefmaster"
			prefMasterParam.value = cliPrefMaster
			params = append(params, prefMasterParam)
			cliClusterCmd("actions/switchover", params)
		} else {
			cliClusterCmd("actions/switchover", nil)
		}
		slogs, _ = cliGetLogs()
		cliPrintLog(slogs)
		cliServers, _ = cliGetServers()
		cliGetTopology()

	},
	PostRun: func(cmd *cobra.Command, args []string) {
		// Close connections on exit.
	},
}
