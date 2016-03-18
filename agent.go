package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
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
		http.ListenAndServe("localhost:10001", nil)
	},
}

func handlerAgent(w http.ResponseWriter, r *http.Request) {
	db, err := newServerMonitor(hosts)
	if err != nil {
		log.Println("Error opening database connection: ", err)
		http.NotFound(w, r)
		return
	}
	db.refresh()
	e := json.NewEncoder(w)
	err = e.Encode(db)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func agentFlagCheck() {
	if logfile != "" {
		var err error
		logPtr, err = os.Create(logfile)
		if err != nil {
			log.Println("ERROR: Error opening logfile, disabling for the rest of the session.")
			logfile = ""
		}
	}
	// if slaves option has been supplied, split into a slice.
	if hosts != "" {
		hostList = strings.Split(hosts, ",")
	} else {
		log.Fatal("ERROR: No hosts list specified.")
	}
	if len(hostList) > 1 {
		log.Fatal("ERROR: Agent can only monitor a single host")
	}
	// validate users.
	if user == "" {
		log.Fatal("ERROR: No master user/pair specified.")
	}
	dbUser, dbPass = splitPair(user)
}
