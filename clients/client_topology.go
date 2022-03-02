//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"fmt"

	"github.com/spf13/cobra"
)

var topologyCmd = &cobra.Command{
	Use:   "topology",
	Short: "Print replication topology",
	Long:  `Print the replication topology by detecting master and slaves`,
	Run: func(cmd *cobra.Command, args []string) {
		cliInit(true)
		cliGetTopology()
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}

func cliGetTopology() {

	headstr := ""

	if cliClusters[cliClusterIndex] != "" {
		headstr += fmt.Sprintf("| Group: %s", cliClusters[cliClusterIndex])
	}
	if cliSettings.Conf.FailMode == "automatic" {
		headstr += " |  Mode: Automatic "
	} else {
		headstr += " |  Mode: Manual "
	}

	headstr += fmt.Sprintf("\n%19s %15s %6s %15s %10s %12s %20s %20s %30s %6s %3s", "Id", "Host", "Port", "Status", "Failures", "Using GTID", "Current GTID", "Slave GTID", "Replication Health", "Delay", "RO")

	for _, server := range cliServers {
		var gtidCurr string
		var gtidSlave string
		if server.CurrentGtid != nil {
			gtidCurr = server.CurrentGtid.Sprint()
		} else {
			gtidCurr = ""
		}
		if server.SlaveGtid != nil {
			gtidSlave = server.SlaveGtid.Sprint()
		} else {
			gtidSlave = ""
		}

		headstr += fmt.Sprintf("\n%19s %15s %6s %15s %10d %12s %20s %20s %30s %6d %3s", server.Id, server.Host, server.Port, server.State, server.FailCount, server.GetReplicationUsingGtid(), gtidCurr, gtidSlave, "", server.GetReplicationDelay(), server.ReadOnly)

	}
	fmt.Printf(headstr)
	fmt.Printf("\n")
}
