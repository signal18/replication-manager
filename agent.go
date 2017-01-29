// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	//	"encoding/json"
	"log"
	"net/http"
	//	"os"
	//	"strings"

	"github.com/spf13/cobra"
	//	"github.com/tanji/replication-manager/misc"
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
		agentFlagCheck()
		http.HandleFunc("/agent/", handlerAgent)
		log.Println("Starting agent on port 10001")
		http.ListenAndServe("0.0.0.0:10001", nil)
	},
}

func handlerAgent(w http.ResponseWriter, r *http.Request) {
	/*	db, err := newServerMonitor(conf.Hosts)
		if err != nil {
			log.Println("Error opening database connection: ", err)
			http.Error(w, "Service is down", 503)
			return
		}
		db.refresh()
		e := json.NewEncoder(w)
		err = e.Encode(db)
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}*/
}

func agentFlagCheck() {
	/*	if conf.LogFile != "" {
			var err error
			logPtr, err = os.Create(conf.LogFile)
			if err != nil {
				log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
				conf.LogFile = ""
			}
		}
		// if slaves option has been supplied, split into a slice.
		if conf.Hosts != "" {
			hostList = strings.Split(conf.Hosts, ",")
		} else {
			log.Fatal("ERROR: No hosts list specified.")
		}
		if len(hostList) > 1 {
			log.Fatal("ERROR: Agent can only monitor a single host")
		}
		// validate users.
		if conf.User == "" {
			log.Fatal("ERROR: No master user/pair specified.")
		}
		dbUser, dbPass = misc.SplitPair(conf.User)*/
}
