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
	"strings"

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
		urlpost := "https://" + cliHost + ":" + cliPort + "/api/clusters/" + cliClusters[cliClusterIndex] + "/servers"
		if strings.Contains(strings.ToLower(cliServerSet), "maintenance=on") {
			urlpost += "/" + cliServerID + "/actions/set-maintenance"
		}
		if strings.Contains(strings.ToLower(cliServerSet), "maintenance=off") {
			urlpost += "/" + cliServerID + "/actions/del-maintenance"
		}
		if strings.Contains(strings.ToLower(cliServerSet), "maintenance=switch") {
			urlpost += "/" + cliServerID + "/actions/maintenance"
		}
		if strings.Contains(strings.ToLower(cliServerSet), "prefered=switch") {
			urlpost += "/" + cliServerID + "/actions/set-prefered"
		}
		if strings.Contains(strings.ToLower(cliServerSet), "ignored=switch") {
			urlpost += "/" + cliServerID + "/actions/set-ignored"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "processlist") {
			urlpost += "/" + cliServerID + "/processlist"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "errors") {
			urlpost += "/" + cliServerID + "/errorlog"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "slow-query") {
			urlpost += "/" + cliServerID + "/slow-query"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "digest-statements-pfs") {
			urlpost += "/" + cliServerID + "/digest-statements-pfs"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "tables") {
			urlpost += "/" + cliServerID + "/tables"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "variables") {
			urlpost += "/" + cliServerID + "/variables"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "meta-data-locks") {
			urlpost += "/" + cliServerID + "/meta-data-locks"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "status-delta") {
			urlpost += "/" + cliServerID + "/status-delta"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "innodb-status") {
			urlpost += "/" + cliServerID + "/status-delta"
		}
		if strings.Contains(strings.ToLower(cliServerGet), "query-response-time") {
			urlpost += "/" + cliServerID + "/innodb-status"
		}
		if strings.Contains(strings.ToLower(cliServerAction), "stop") {
			urlpost += "/" + cliServerID + "/actions/stop"
		}
		if strings.Contains(strings.ToLower(cliServerAction), "start") {
			urlpost += "/" + cliServerID + "/actions/start"
		}
		if strings.Contains(strings.ToLower(cliServerAction), "provision") {
			urlpost += "/" + cliServerID + "/actions/provision"
		}
		if strings.Contains(strings.ToLower(cliServerAction), "unprovision") {
			urlpost += "/" + cliServerID + "/actions/unprovision"
		}

		_, err := cliAPICmd(urlpost, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", err)
			os.Exit(1)
		}
		os.Exit(0)
	},
}
