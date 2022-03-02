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
	"os"

	"github.com/spf13/cobra"

	log "github.com/sirupsen/logrus"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run some actions on a server",
	Long:  `The server command is used to stop , start or put a server in maintenace`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(true)
		urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/servers/" + cliServerID + "/actions/maintenance"
		_, err := cliAPICmd(urlpost, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", err)
			os.Exit(1)
		}
		os.Exit(0)
	},
}
