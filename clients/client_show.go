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
	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/server"
	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Print json informations",
	Long:  `To use for support issues`,
	Run: func(cmd *cobra.Command, args []string) {
		//cliClusters, err = cliGetClusters()
		cliInit(false)
		urlpost := ""
		type Objects struct {
			Name     string
			Settings server.Settings         `json:"settings"`
			Servers  []cluster.ServerMonitor `json:"servers"`
			Master   cluster.ServerMonitor   `json:"master"`
			Slaves   []cluster.ServerMonitor `json:"slaves"`
			Crashes  []cluster.Crash         `json:"crashes"`
			Alerts   cluster.Alerts          `json:"alerts"`
		}
		type Report struct {
			Clusters []Objects `json:"clusters"`
		}
		var myReport Report

		for _, cluster := range cliClusters {

			var myObjects Objects
			myObjects.Name = cluster
			if strings.Contains(cliShowObjects, "settings") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {

					json.Unmarshal([]byte(res), &myObjects.Settings)
				}
			}
			if strings.Contains(cliShowObjects, "servers") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/servers"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Servers)
				}
			}
			if strings.Contains(cliShowObjects, "master") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/master"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Master)
				}
			}
			if strings.Contains(cliShowObjects, "slaves") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/master"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Slaves)
				}
			}
			if strings.Contains(cliShowObjects, "crashes") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/crashes"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Crashes)
				}
			}
			if strings.Contains(cliShowObjects, "alerts") {
				urlpost = "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cluster + "/topology/alerts"
				res, err := cliAPICmd(urlpost, nil)
				if err == nil {
					json.Unmarshal([]byte(res), &myObjects.Alerts)
				}
			}
			myReport.Clusters = append(myReport.Clusters, myObjects)

		}
		data, err := json.MarshalIndent(myReport, "", "\t")
		if err != nil {
			fmt.Println(err)
			os.Exit(10)
		}

		fmt.Fprintf(os.Stdout, "%s\n", data)
	},
	PostRun: func(cmd *cobra.Command, args []string) {

	},
}
