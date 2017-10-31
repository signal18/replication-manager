// +build arbitrator

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	_ "github.com/mattn/go-sqlite3"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/dbhelper"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type route struct {
	Name        string
	Method      string
	Pattern     string
	HandlerFunc http.HandlerFunc
}

type routes []route

func newRouter() *mux.Router {

	router := mux.NewRouter().StrictSlash(true)
	for _, r := range rs {
		router.
			Methods(r.Method).
			Path(r.Pattern).
			Name(r.Name).
			Handler(r.HandlerFunc)
	}

	return router
}

var rs = routes{
	route{
		"Heartbeat",
		"POST",
		"/heartbeat",
		handlerHeartbeat,
	},
	route{
		"Arbitrator",
		"POST",
		"/arbitrator",
		handlerArbitrator,
	},
	route{
		"Forget",
		"PST",
		"/forget/",
		handlerForget,
	},
}

type response struct {
	Arbitration   string `json:"arbitration"`
	ElectedMaster string `json:"master"`
}

var (
	arbitratorPort    int
	arbitratorDriver  string
	arbitratorCluster *cluster.Cluster
)

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.AddCommand(arbitratorCmd)
	arbitratorCmd.Flags().IntVar(&arbitratorPort, "arbitrator-port", 10001, "Arbitrator API port")
	arbitratorCmd.Flags().StringVar(&arbitratorDriver, "arbitrator-driver", "sqllite", "sqllite|mysql, use a local sqllite or use a mysql backend")

}

var arbitratorCmd = &cobra.Command{
	Use:   "arbitrator",
	Short: "Arbitrator environment",
	Long:  `The arbitrator is used for false positive detection`,
	Run: func(cmd *cobra.Command, args []string) {
		var err error
		var db *sqlx.DB
		arbitratorPort = confs["arbitrator"].arbitratorPort
		if arbitratorDriver == "mysql" {
			arbitratorCluster = new(cluster.Cluster)
			db, err = arbitratorCluster.InitAgent(confs["arbitrator"])
			if err != nil {
				panic(err)
			}
			arbitratorCluster.SetLogStdout()
		}

		db, err = getArbitratorBackendStorageConnection()

		err = dbhelper.SetHeartbeatTable(db)
		if err != nil {
			log.WithError(err).Error("Error creating tables")
		}
		router := newRouter()
		log.Infof("Arbitrator listening on port %d", arbitratorPort)
		log.Fatal(http.ListenAndServe("0.0.0.0:"+strconv.Itoa(arbitratorPort), router))
	},
}

func getArbitratorBackendStorageConnection() (*sqlx.DB, error) {

	var err error
	var db *sqlx.DB
	if arbitratorDriver == "sqllite" {
		db, err = dbhelper.MemDBConnect()
	}
	if arbitratorDriver == "mysql" {
		db, err = dbhelper.MySQLConnect(arbitratorCluster.GetServers()[0].User, arbitratorCluster.GetServers()[0].Pass, arbitratorCluster.GetServers()[0].Host+":"+arbitratorCluster.GetServers()[0].Port, fmt.Sprintf("?timeout=%ds", confs["arbitrator"].Timeout))
	}
	return db, err
}

func handlerArbitrator(w http.ResponseWriter, r *http.Request) {
	var h heartbeat
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		panic(err)
	}
	if err := r.Body.Close(); err != nil {
		panic(err)
	}
	log.Info("Arbitration request received: ", string(body))
	if err := json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
	}
	var send response

	db, err := getArbitratorBackendStorageConnection()
	defer db.Close()
	res := dbhelper.RequestArbitration(db, h.UUID, h.Secret, h.Cluster, h.Master, h.UID, h.Hosts, h.Failed)
	electedmaster := dbhelper.GetArbitrationMaster(db, h.Secret, h.Cluster)
	if res {
		send.Arbitration = "winner"
		send.ElectedMaster = electedmaster
	} else {
		send.Arbitration = "looser"
		send.ElectedMaster = electedmaster
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(http.StatusCreated)

	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}

}
func handlerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var h heartbeat
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))

	if err != nil {
		panic(err)
	}
	//log.Printf("INFO: Hearbeat receive:%s", string(body))
	if err := r.Body.Close(); err != nil {
		panic(err)
	}
	if err := json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	var send string
	db, err := getArbitratorBackendStorageConnection()
	if err != nil {
		arbitratorCluster.LogPrintf("ERROR", "Error opening arbitrator database: %s", err)
	}
	defer db.Close()
	res := dbhelper.WriteHeartbeat(db, h.UUID, h.Secret, h.Cluster, h.Master, h.UID, h.Hosts, h.Failed)
	if res == nil {
		send = `{"heartbeat":"succed"}`
	} else {
		log.Error("Error writing heartbeat, reason: ", res)
		send = `{"heartbeat":"failed"}`
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}

}

func handlerForget(w http.ResponseWriter, r *http.Request) {
	var h heartbeat
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))

	if err != nil {
		panic(err)
	}
	//log.Printf("INFO: Hearbeat receive:%s", string(body))
	if err = r.Body.Close(); err != nil {
		panic(err)
	}
	if err = json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	//	currentCluster := new(cluster.Cluster)
	var send string
	db, err := getArbitratorBackendStorageConnection()
	defer db.Close()
	res := dbhelper.ForgetArbitration(db, h.Secret)
	if res == nil {
		send = `{"heartbeat":"succed"}`
	} else {
		send = `{"heartbeat":"failed"}`
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}

}
