// replication-manager - Replication Manager Monitoring and CLI for MariaDB
// Authors: Guillaume Lefranc <guillaume.lefranc@mariadb.com>
//          Stephane Varoqui  <stephane.varoqui@mariadb.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

type settings struct {
	Interactive    string `json:"interactive"`
	FailoverCtr    string `json:"failoverctr"`
	MaxDelay       string `json:"maxdelay"`
	Faillimit      string `json:"faillimit"`
	LastFailover   string `json:"lastfailover"`
	Uptime         string `json:"uptime"`
	UptimeFailable string `json:"uptimefailable"`
	UptimeSemiSync string `json:"uptimesemisync"`
	RplChecks      string `json:"rplchecks"`
	FailSync       string `json:"failsync"`
	Test           string `json:"test"`
	Heartbeat      string `json:"heartbeat"`
	Status         string `json:"run_status"`
}

func httpserver() {
	http.HandleFunc("/", handlerApp)
	http.HandleFunc("/dashboard.js", handlerJS)
	http.HandleFunc("/servers", handlerServers)
	http.HandleFunc("/master", handlerMaster)
	http.HandleFunc("/log", handlerLog)
	http.HandleFunc("/switchover", handlerSwitchover)
	http.HandleFunc("/failover", handlerFailover)
	http.HandleFunc("/interactive", handlerInteractiveToggle)
	http.HandleFunc("/settings", handlerSettings)
	http.HandleFunc("/resetfail", handlerResetFailoverCtr)
	http.HandleFunc("/rplchecks", handlerRplChecks)
	http.HandleFunc("/failsync", handlerFailSync)
	http.HandleFunc("/tests", handlerTests)
	http.HandleFunc("/setactive", handlerSetActive)
	if verbose {
		logprint("INFO : Starting http monitor on port " + httpport)
	}
	log.Fatal(http.ListenAndServe(bindaddr+":"+httpport, nil))
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, httproot+"/app.html")
}

func handlerJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, httproot+"/dashboard.js")
}

func handlerServers(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(servers)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMaster(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(master)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSettings(w http.ResponseWriter, r *http.Request) {
	s := new(settings)
	s.Interactive = fmt.Sprintf("%v", interactive)
	s.RplChecks = fmt.Sprintf("%v", rplchecks)
	s.FailSync = fmt.Sprintf("%v", failsync)
	s.MaxDelay = fmt.Sprintf("%v", maxDelay)
	s.FailoverCtr = fmt.Sprintf("%d", failoverCtr)
	s.Faillimit = fmt.Sprintf("%d", faillimit)
	s.Uptime = sme.GetUptime()
	s.UptimeFailable = sme.GetUptimeFailable()
	s.UptimeSemiSync = sme.GetUptimeSemiSync()
	s.Test = fmt.Sprintf("%v", test)
	s.Heartbeat = fmt.Sprintf("%v", heartbeat)
	s.Status = fmt.Sprintf("%v", run_status)
	if failoverTs != 0 {
		t := time.Unix(failoverTs, 0)
		s.LastFailover = t.String()
	} else {
		s.LastFailover = "N/A"
	}
	e := json.NewEncoder(w)
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerLog(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(tlog.Buffer)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSwitchover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if master.State == stateFailed {
		logprint("ERROR: Master failed, cannot initiate switchover")
		http.Error(w, "Master failed", http.StatusBadRequest)
		return
	}
	swChan <- true
	return
}

func handlerSetActive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	getActiveStatus()
	return
}

func handlerFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	masterFailover(true)
	return
}

func handlerInteractiveToggle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	toggleInteractive()
	return
}

func handlerRplChecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprint("INFO: Force to ignore conditions %v", rplchecks)
	rplchecks = !rplchecks
	return
}

func handlerFailSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprintf("INFO: Force failover on status sync %v", failsync)
	failsync = !failsync
	return
}

func handlerResetFailoverCtr(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	failoverCtr = 0
	failoverTs = 0
	return
}

func handlerBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := bootstrap(); err != nil {
		logprint("ERROR: Could not bootstrap replication")
		logprint(err)
	}
	return
}

func handlerTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	err := runAllTests()
	if err == false {
		logprint("ERROR: Some tests failed")
	}
	return
}
