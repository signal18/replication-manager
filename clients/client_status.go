//go:build clients
// +build clients

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.
package clients

import (
	"encoding/json"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Request status ",
	Long:  `The status command is used to request monitor daemon or pecific cluster status`,
	Run: func(cmd *cobra.Command, args []string) {
		log.SetFormatter(&log.TextFormatter{})
		cliInit(false)
		type Result struct {
			Alive string `json:"alive"`
		}
		var ret Result

		if cfgGroup == "" {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/status"
			res, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "API call %s", err)
				os.Exit(1)
			} else {
				if res != "" {
					err = json.Unmarshal([]byte(res), &ret)
					if err != nil {
						fmt.Fprintf(os.Stderr, "API call %s", err)
						os.Exit(2)
					} else {
						fmt.Fprintf(os.Stdout, "%s\n", ret.Alive)
						os.Exit(0)
					}
				}
			}
		}
		if cfgGroup != "" {
			urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/status"
			res, err := cliAPICmd(urlpost, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "API call %s", err)
				os.Exit(1)
			} else {
				if res != "" {
					err = json.Unmarshal([]byte(res), &ret)
					if err != nil {
						fmt.Fprintf(os.Stderr, "API call %s", err)
						os.Exit(2)
					} else {
						if cliStatusErrors {
							urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/topology/alerts"
							res, err := cliAPICmd(urlpost, nil)
							if err != nil {
								fmt.Fprintf(os.Stderr, "API call %s", err)
								os.Exit(3)
							} else {
								fmt.Fprintf(os.Stdout, "%s\n", res)
							}
						} else {
							fmt.Fprintf(os.Stdout, "%s\n", ret.Alive)
						}
						os.Exit(0)
					}
				}
			}
		}
	},
}
