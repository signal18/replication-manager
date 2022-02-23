//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

var failoverCmd = &cobra.Command{
	Use:   "failover",
	Short: "Failover a dead master",
	Long:  `Trigger failover on a dead master by promoting a slave.`,
	Run: func(cmd *cobra.Command, args []string) {
		var slogs []string
		cliInit(true)
		cliGetTopology()
		cliClusterCmd("actions/failover", nil)
		slogs, _ = cliGetLogs()
		cliPrintLog(slogs)
		cliServers, _ = cliGetServers()
		cliGetTopology()
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}
