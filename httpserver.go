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
	Status         string `json:"runstatus"`
	ConfGroup      string `json:"confgroup"`
}

func httpserver() {
	http.HandleFunc("/", handlerApp)
	http.HandleFunc("/servers", handlerServers)
	http.HandleFunc("/master", handlerMaster)
	http.HandleFunc("/log", handlerLog)
	http.HandleFunc("/switchover", handlerSwitchover)
	http.HandleFunc("/failover", handlerFailover)
	http.HandleFunc("/interactive", handlerInteractiveToggle)
	http.HandleFunc("/settings", handlerSettings)
	http.HandleFunc("/resetfail", handlerResetFailoverCtr)
	http.HandleFunc("/rplchecks", handlerRplChecks)
	http.HandleFunc("/bootstrap", handlerBootstrap)
	http.HandleFunc("/failsync", handlerFailSync)
	http.HandleFunc("/tests", handlerTests)
	http.HandleFunc("/setactive", handlerSetActive)
	http.HandleFunc("/dashboard.js", handlerJS)

	http.Handle("/static/", http.FileServer(http.Dir(conf.HttpRoot)))

	if conf.Verbose {
		logprint("INFO : Starting http monitor on port " + conf.HttpPort)
	}
	log.Fatal(http.ListenAndServe(conf.BindAddr+":"+conf.HttpPort, nil))
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, conf.HttpRoot+"/app.html")
}

func handlerJS(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, conf.HttpRoot+"/dashboard.js")
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
	s.Interactive = fmt.Sprintf("%v", conf.Interactive)
	s.RplChecks = fmt.Sprintf("%v", conf.RplChecks)
	s.FailSync = fmt.Sprintf("%v", conf.FailSync)
	s.MaxDelay = fmt.Sprintf("%v", conf.MaxDelay)
	s.FailoverCtr = fmt.Sprintf("%d", failoverCtr)
	s.Faillimit = fmt.Sprintf("%d", conf.FailLimit)
	s.Uptime = sme.GetUptime()
	s.UptimeFailable = sme.GetUptimeFailable()
	s.UptimeSemiSync = sme.GetUptimeSemiSync()
	s.Test = fmt.Sprintf("%v", conf.Test)
	s.Heartbeat = fmt.Sprintf("%v", conf.Heartbeat)
	s.Status = fmt.Sprintf("%v", runStatus)
	s.ConfGroup = fmt.Sprintf("%s", cfgGroup)
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
	logprint("INFO: Force to ignore conditions %v", conf.RplChecks)
	conf.RplChecks = !conf.RplChecks
	return
}

func handlerFailSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprintf("INFO: Force failover on status sync %v", conf.FailSync)
	conf.FailSync = !conf.FailSync
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
	cleanall = true
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
