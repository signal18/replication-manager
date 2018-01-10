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
	arbitratorCluster *cluster.Cluster
)

func init() {
	cobra.OnInitialize(initConfig)
	rootCmd.AddCommand(arbitratorCmd)
	arbitratorCmd.Flags().StringVar(&conf.ArbitratorAddress, "arbitrator-bind-address", "0.0.0.0:10001", "Arbitrator API port")
	arbitratorCmd.Flags().StringVar(&conf.ArbitratorDriver, "arbitrator-driver", "sqlite", "sqlite|mysql, use a local sqllite or use a mysql backend")

}

var arbitratorCmd = &cobra.Command{
	Use:   "arbitrator",
	Short: "Arbitrator environment",
	Long:  `The arbitrator is used for false positive detection`,
	Run: func(cmd *cobra.Command, args []string) {

		if _, ok := confs["arbitrator"]; !ok {
			log.Fatal("Could not find arbitrator configuration section")
		}

		if confs["arbitrator"].ArbitratorDriver == "mysql" {
			arbitratorCluster = new(cluster.Cluster)
			arbitratorCluster.InitAgent(confs["arbitrator"])
			arbitratorCluster.SetLogStdout()
		}

		db, err := getArbitratorBackendStorageConnection()
		if err != nil {
			log.Fatal("Error opening arbitrator database: ", err)
		}

		err = db.Ping()
		if err != nil {
			log.Fatal(err)
		}

		err = dbhelper.SetHeartbeatTable(db)
		if err != nil {
			log.WithError(err).Error("Error creating tables")
		}
		router := newRouter()
		log.Infof("Arbitrator listening on %s", confs["arbitrator"].ArbitratorAddress)
		log.Fatal(http.ListenAndServe(confs["arbitrator"].ArbitratorAddress, router))
	},
}

func getArbitratorBackendStorageConnection() (*sqlx.DB, error) {

	var err error
	var db *sqlx.DB
	if confs["arbitrator"].ArbitratorDriver == "sqlite" {
		db, err = dbhelper.SQLiteConnect(conf.WorkingDir)
	}
	if confs["arbitrator"].ArbitratorDriver == "mysql" {
		db, err = dbhelper.MySQLConnect(arbitratorCluster.GetServers()[0].User, arbitratorCluster.GetServers()[0].Pass, arbitratorCluster.GetServers()[0].Host+":"+arbitratorCluster.GetServers()[0].Port, fmt.Sprintf("?timeout=%ds", confs["arbitrator"].Timeout))
	}
	return db, err
}

func handlerArbitrator(w http.ResponseWriter, r *http.Request) {
	var h heartbeat
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))
	if err != nil {
		log.Errorln(err)
		w.WriteHeader(500)
		return
	}
	if err := r.Body.Close(); err != nil {
		log.Errorln(err)
		w.WriteHeader(500)
		return
	}
	log.Info("Arbitration request received: ", string(body))
	if err := json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			log.Errorln(err)
			w.WriteHeader(500)
		}
	}
	var send response

	db, err := getArbitratorBackendStorageConnection()
	if err != nil {
		arbitratorCluster.LogPrintf("ERROR", "Error opening arbitrator database: %s", err)
		return
	}
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
	if err = r.Body.Close(); err != nil {
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}
	if err = json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			w.WriteHeader(500)
			log.Errorln(err)
			return
		}
		return
	}

	var send string
	db, err := getArbitratorBackendStorageConnection()
	if err != nil {
		arbitratorCluster.LogPrintf("ERROR", "Error opening arbitrator database: %s", err)
		w.WriteHeader(500)
		log.Errorln(err)
		return
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
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}

}

func handlerForget(w http.ResponseWriter, r *http.Request) {
	var h heartbeat
	body, err := ioutil.ReadAll(io.LimitReader(r.Body, 1048576))

	if err != nil {
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}
	//log.Printf("INFO: Hearbeat receive:%s", string(body))
	if err = r.Body.Close(); err != nil {
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}
	if err = json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			w.WriteHeader(500)
			log.Errorln(err)
			return
		}
		return
	}

	//	currentCluster := new(cluster.Cluster)
	var send string
	db, err := getArbitratorBackendStorageConnection()
	if err != nil {
		arbitratorCluster.LogPrintf("ERROR", "Error opening arbitrator database: %s", err)
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}
	defer db.Close()
	res := dbhelper.ForgetArbitration(db, h.Secret)
	if res == nil {
		send = `{"heartbeat":"succed"}`
	} else {
		send = `{"heartbeat":"failed"}`
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	if err := json.NewEncoder(w).Encode(send); err != nil {
		w.WriteHeader(500)
		log.Errorln(err)
		return
	}

}
