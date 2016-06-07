package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

type settings struct {
	Interactive string `json:"interactive"`
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
	if verbose {
		logprint("INFO : Starting http monitor on port " + httpport)
	}
	http.ListenAndServe(bindaddr+":"+httpport, nil)
}

func handlerApp(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "dashboard/app.html")
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
	err := e.Encode(tlog.buffer)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerSwitchover(w http.ResponseWriter, r *http.Request) {
	if master.State != stateFailed && failCount == 0 {
		masterFailover(false)
		return
	}
	logprint("ERROR: Master failed, cannot initiate switchover")
	http.Error(w, "Master failed", http.StatusBadRequest)
	return
}

func handlerFailover(w http.ResponseWriter, r *http.Request) {
	masterFailover(true)
	return
}

func handlerInteractiveToggle(w http.ResponseWriter, r *http.Request) {
	if interactive == true {
		interactive = false
	} else {
		interactive = true
	}
	return
}
