//go:build agent
// +build agent

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	//	"encoding/json"
	"encoding/json"
	"log"
	"net/http"
	//	"os"
	//	"strings"

	"github.com/signal18/replication-manager/cluster"
	"github.com/spf13/cobra"
	//	"github.com/signal18/replication-manager/misc"
)

func init() {
	rootCmd.AddCommand(agentCmd)
}

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Starts replication monitoring agent",
	Long: `The replication monitoring agent is used by the failover monitor to
take consensus decisions`,
	Run: func(cmd *cobra.Command, args []string) {
		http.HandleFunc("/agent/", handlerAgent)
		log.Println("Starting agent on port 10001")
		http.ListenAndServe("0.0.0.0:10001", nil)
	},
}

func handlerAgent(w http.ResponseWriter, r *http.Request) {

	currentCluster = new(cluster.Cluster)
	err := currentCluster.InitAgent(conf)
	e := json.NewEncoder(w)
	err = e.Encode(db)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}
