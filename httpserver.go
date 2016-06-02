package main

import (
	"encoding/json"
	"log"
	"net/http"
)

func httpserver() {
	http.HandleFunc("/", handlerApp)
	http.HandleFunc("/servers", handlerServers)
	http.HandleFunc("/log", handlerLog)
	http.HandleFunc("/switchover", handlerSwitchover)
	log.Println("Starting agent on port " + httpport)
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
	if master.State != stateFailed || failCount > 0 {
		masterFailover(false)
		return
	}
	logprint("ERROR: Master failed, cannot initiate switchover")
	http.Error(w, "Master failed", http.StatusBadRequest)
	return
}
