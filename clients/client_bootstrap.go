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

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var bootstrapCmd = &cobra.Command{
	Use:   "bootstrap",
	Short: "Bootstrap a replication environment",
	Long:  `The bootstrap command is used to create a new replication environment from scratch`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(true)

		if cliBootstrapWithProvisioning == true {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/services/provision"
			_, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s", err)
				os.Exit(1)
			} else {
				fmt.Println("Provisioning done")
				os.Exit(0)
			}
		} else {

			if cliBootstrapCleanall == true {
				urlclean := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/cleanup"
				_, err := cliAPICmd(urlclean, nil)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s", err)
					os.Exit(1)
				} else {
					fmt.Println("Replication cleanup done")
				}
			}
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/actions/replication/bootstrap/" + cliBootstrapTopology
			_, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s", err)
				os.Exit(2)
			} else {
				fmt.Println("Replication bootsrap done")
			}
			//		slogs, _ := cliGetLogs()
			//	cliPrintLog(slogs)
			cliGetTopology()
		}
	},
}
