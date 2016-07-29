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
	Faillimit      string `json:"faillimit"`
	LastFailover   string `json:"lastfailover"`
	Uptime         string `json:"uptime"`
	UptimeFailable string `json:"uptimefailable"`
	UptimeSemiSync string `json:"uptimesemisync"`
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
	http.HandleFunc("/force", handlerForce)
	http.HandleFunc("/bootstrap", handlerBootstrap)
	if verbose {
		logprint("INFO : Starting http monitor on port " + httpport)
	}
	http.ListenAndServe(bindaddr+":"+httpport, nil)
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
	s.FailoverCtr = fmt.Sprintf("%d", failoverCtr)
	s.Faillimit = fmt.Sprintf("%d", faillimit)
	s.Uptime = sme.GetUptime()
	s.UptimeFailable = sme.GetUptimeFailable()
	s.UptimeSemiSync = sme.GetUptimeSemiSync()
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
	logprint("INFO: Sending switchover message to channel")
	swChan <- true
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

func handlerForce(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	logprint(fmt.Sprintf("INFO: Force to ingnore conditions %b" , force) )
	force = !force
	return
}

func handlerResetFailoverCtr(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	failoverCtr = 0
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
