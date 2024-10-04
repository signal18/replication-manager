// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

// Package server replication-manager
//
// Replication Manager Monitoring and CLI for MariaDB and MySQL
//
//	Schemes: https
//	Host: localhost
//	BasePath: /
//	Version: 0.0.1
//	License: GPL http://opensource.org/licenses/GPL
//	Contact: Stephane Varoqui  <svaroqui@gmail.com>
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Security:
//	  api_key:
//
// swagger:meta
package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	_ "net/http/pprof"
	"net/url"
	"os"
	"strconv"

	"github.com/codegangsta/negroni"
	"github.com/gorilla/mux"
	"github.com/iu0v1/gelada"
	"github.com/iu0v1/gelada/authguard"
	log "github.com/sirupsen/logrus"
)

type HandlerManager struct {
	Gelada    *gelada.Gelada
	AuthGuard *authguard.AuthGuard
}

func (repman *ReplicationManager) testFile(fn string) error {

	f, err := os.Open(repman.Conf.HttpRoot + "/" + fn)
	if err != nil {
		log.Printf("error no file %s", repman.Conf.HttpRoot+"/"+fn)
		return err
	}
	f.Close()
	return nil
}

func (repman *ReplicationManager) httpserver() {
	//PUBLIC ENDPOINTS
	router := mux.NewRouter()
	router.Use(repman.RecoveryMiddleware)
	router.PathPrefix("/debug/pprof/").Handler(http.DefaultServeMux)

	graphiteHost := repman.Conf.GraphiteCarbonHost
	if repman.Conf.GraphiteEmbedded {
		graphiteHost = "127.0.0.1"
	}
	graphiteURL, err := url.Parse(fmt.Sprintf("http://%s:%d", graphiteHost, repman.Conf.GraphiteCarbonApiPort))
	if err == nil {
		// Set up the reverse proxy target for Graphite API
		graphiteProxy := httputil.NewSingleHostReverseProxy(graphiteURL)
		// Set up a route that forwards the request to the Graphite API
		router.PathPrefix("/graphite/").Handler(http.StripPrefix("/graphite/", graphiteProxy))
	}

	router.PathPrefix("/meet/").Handler(http.StripPrefix("/meet/", repman.proxyToURL("https://meet.signal18.io/api/v4")))

	//router.HandleFunc("/", repman.handlerApp)
	// page to view which does not need authorization
	if repman.Conf.Test {
		// before starting the http server, check that the dashboard is present
		file2test := "index.html"
		if !repman.Conf.HttpUseReact {
			file2test = "app.html"
		}
		if err := repman.testFile(file2test); err != nil {
			log.Printf("ERROR: Dashboard app.html file missing - will not start http server %s\n", err)
			return
		}
		router.HandleFunc("/", repman.handlerApp)
		router.PathPrefix("/images/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/assets/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", http.FileServer(http.Dir(repman.Conf.ShareDir+"/grafana"))))
	} else {
		router.HandleFunc("/", repman.rootHandler)
		router.PathPrefix("/images/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/assets/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/static/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/app/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", repman.SharedirHandler("grafana")))
	}

	router.HandleFunc("/api/login", repman.loginHandler)
	router.HandleFunc("/api/login-git", repman.loginHandler)

	router.Handle("/api/clusters", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusters)),
	))
	router.Handle("/api/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStatus)),
	))
	router.Handle("/api/auth/callback", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAuthCallback)),
	))

	router.Handle("/api/prometheus", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxPrometheus)),
	))

	router.Handle("/api/configs/grafana", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGrafana)),
	))

	router.Handle("/api/timeout", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxTimeout)),
	))
	router.Handle("/api/heartbeat", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxMonitorHeartbeat)),
	))

	router.Handle("/api/clusters/{clusterName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterStatus)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/master-physical-backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterMasterPhysicalBackup)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServerMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsSlaveStatus)),
	))
	// handle API 2.0 compatibility for external checks
	router.Handle("/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/clusters/{clusterName}/servers/{serverName}/{serverPort}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortIsSlaveStatus)),
	))

	router.Handle("/clusters/{clusterName}/sphinx-indexes", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSphinxIndexes)),
	))

	router.Handle("/api/clusters/{clusterName}/sphinx-indexes", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSphinxIndexes)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxServersPortBackup)),
	))

	//USER PROTECTED ENDPOINTS

	repman.apiClusterUnprotectedHandler(router)
	repman.apiDatabaseUnprotectedHandler(router)
	if !repman.Conf.APIHttpsBind {
		router.Handle("/api/monitor", negroni.New(
			negroni.Wrap(http.HandlerFunc(repman.handlerMuxReplicationManager)),
		))
		repman.apiClusterProtectedHandler(router)
		repman.apiDatabaseProtectedHandler(router)
		repman.apiProxyProtectedHandler(router)
	}
	// create mux router
	router.HandleFunc("/repocomp/current", repman.handlerRepoComp)
	router.HandleFunc("/heartbeat", repman.handlerHeartbeat)

	if repman.Conf.Verbose {
		log.Printf("Starting HTTP server on " + repman.Conf.BindAddr + ":" + repman.Conf.HttpPort)
	}
	log.Fatal(http.ListenAndServe(repman.Conf.BindAddr+":"+repman.Conf.HttpPort, router))

}

func (repman *ReplicationManager) handlerApp(w http.ResponseWriter, r *http.Request) {
	if repman.Conf.HttpUseReact {
		http.ServeFile(w, r, repman.Conf.HttpRoot+"/index.html")
	} else {
		http.ServeFile(w, r, repman.Conf.HttpRoot+"/app.html")
	}
}

func (repman *ReplicationManager) handlerRepoComp(w http.ResponseWriter, r *http.Request) {

	data, err := os.ReadFile(string(repman.Conf.ShareDir + "/opensvc/current"))

	if err != nil {
		w.WriteHeader(404)
		w.Write([]byte("404 Something went wrong - " + http.StatusText(404)))
		return
	}
	w.Write(data)

}

func (repman *ReplicationManager) handlerAgents(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(repman.Agents)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func (repman *ReplicationManager) handlerHeartbeat(w http.ResponseWriter, r *http.Request) {
	repman.Lock()
	var send Heartbeat
	send.UUID = repman.UUID
	send.UID = repman.Conf.ArbitrationSasUniqueId
	send.Secret = repman.Conf.ArbitrationSasSecret
	send.Status = repman.Status
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(send); err != nil {
		http.Error(w, "Encoding error", 500)
	}
	repman.Unlock()
}

func (repman *ReplicationManager) handlerLog(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	values := r.URL.Query()
	off := values.Get("offset")
	if off == "" {
		off = "1000"
	}
	noff, _ := strconv.Atoi(off)
	err := e.Encode(repman.Logs.Buffer[:noff])
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}
