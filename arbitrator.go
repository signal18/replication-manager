package main

import (
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/dbhelper"
)

type Route struct {
	Name        string
	Method      string
	Pattern     string
	HandlerFunc http.HandlerFunc
}

type Routes []Route

func NewRouter() *mux.Router {

	router := mux.NewRouter().StrictSlash(true)
	for _, route := range routes {
		router.
			Methods(route.Method).
			Path(route.Pattern).
			Name(route.Name).
			Handler(route.HandlerFunc)
	}

	return router
}

var routes = Routes{
	Route{
		"Heartbeat",
		"POST",
		"/heartbeat",
		handlerHeartbeat,
	},
	Route{
		"Arbitrator",
		"POST",
		"/arbitrator",
		handlerArbitrator,
	},
	Route{
		"Forget",
		"PST",
		"/forget/",
		handlerForget,
	},
}

type heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
	Status  string `json:"status"`
}

type response struct {
	Arbitration string `json:"arbitration"`
}

var (
	arbitratorPort int
)

func init() {
	rootCmd.AddCommand(arbitratorCmd)
	arbitratorCmd.Flags().IntVar(&arbitratorPort, "arbitrator-port", 80, "Arbitrator API port")
}

var arbitratorCmd = &cobra.Command{
	Use:   "arbitrator",
	Short: "Arbitrator environment",
	Long:  `The arbitrator is used for falspositiv detection `,
	Run: func(cmd *cobra.Command, args []string) {
		currentCluster = new(cluster.Cluster)
		db, err := currentCluster.InitAgent(confs["arbitrator"])
		if err != nil {
			panic(err)
		}
		currentCluster.SetLogStdout()

		err = dbhelper.SetHeartbeatTable(db.Conn)
		if err != nil {
			log.Printf("ERROR: Error creating tables")
			//panic(err)
		}
		db.Close()
		//http.HandleFunc("/heartbeat/", handlerHeartbeat)
		//	http.HandleFunc("/abritrator/", handlerArbitrator)
		router := NewRouter()
		log.Fatal(http.ListenAndServe("0.0.0.0:"+strconv.Itoa(arbitratorPort), router))
	},
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
	log.Printf("INFO: Arbitrator receive:%s", string(body))
	if err := json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
	}
	var send response
	currentCluster = new(cluster.Cluster)
	db, _ := currentCluster.InitAgent(confs["arbitrator"])
	res := dbhelper.RequestArbitration(db.Conn, h.UUID, h.Secret, h.Cluster, h.Master, h.UID)
	db.Close()
	if res {
		send.Arbitration = "winner"
	} else {
		send.Arbitration = "looser"
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
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	currentCluster = new(cluster.Cluster)
	db, _ := currentCluster.InitAgent(confs["arbitrator"])
	var send string
	res := dbhelper.WriteHeartbeat(db.Conn, h.UUID, h.Secret, h.Cluster, h.Master, h.UID)
	db.Close()
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

func handlerForget(w http.ResponseWriter, r *http.Request) {
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
		if err := json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
		return
	}

	currentCluster = new(cluster.Cluster)
	db, _ := currentCluster.InitAgent(confs["arbitrator"])
	var send string
	res := dbhelper.ForgetArbitration(db.Conn, h.Secret)
	db.Close()
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

func Heartbeat() {
	if cfgGroup == "arbitrator" {
		return
	}
	var peerList []string
	// try to found an active peer replication-manager
	if conf.ArbitrationPeerHosts != "" {
		peerList = strings.Split(conf.ArbitrationPeerHosts, ",")
	} else {
		return
	}
	splitbrain := true
	timeout := time.Duration(2 * time.Second)
	for _, peer := range peerList {
		url := "http://" + peer + "/heartbeat"
		client := &http.Client{
			Timeout: timeout,
		}
		// Send the request via a client
		// Do sends an HTTP request and
		// returns an HTTP response
		// Build the request
		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			currentCluster.LogPrintf("ERROR :%s", err)
			continue

		}
		resp, err := client.Do(req)
		if err != nil {
			currentCluster.LogPrintf("ERROR :%s", err)
			continue
		}

		// Callers should close resp.Body
		// when done reading from it
		// Defer the closing of the body
		defer resp.Body.Close()
		monjson, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			currentCluster.LogPrintf("ERROR :%s", err)
		}
		// Use json.Decode for reading streams of JSON data
		var h heartbeat
		if err := json.Unmarshal(monjson, &h); err != nil {
			currentCluster.LogPrintf("ERROR :%s", err)
		} else {
			splitbrain = false
			if conf.LogLevel > 3 {
				currentCluster.LogPrintf("RETURN :%s", h)
			}
			if h.Status == "S" {
				runStatus = "A"
			} else {
				runStatus = "S"
			}
		}

	}
	if splitbrain {
		currentCluster.LogPrintf("INFO : Splitbrain")
		for _, cl := range clusters {

			url := "http://" + conf.ArbitrationSasHosts + "/heartbeat"
			var mst string
			if cl.GetMaster() != nil {
				mst = cl.GetMaster().URL
			}
			var jsonStr = []byte(`{"uuid":"` + runUUID + `","secret":"` + conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(conf.ArbitrationSasUniqueId) + `,"status":"` + runStatus + `"}`)
			req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
			req.Header.Set("X-Custom-Header", "myvalue")
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: timeout}
			resp, err := client.Do(req)
			if err != nil {
				cl.LogPrintf("ERROR :%s", err.Error())
				cl.SetActiceStatus("S")
				runStatus = "S"
				return
			}
			defer resp.Body.Close()

			body, _ := ioutil.ReadAll(resp.Body)
			//if string(body) == `{\"heartbeat\":\"succed\"}` {
			//	cl.LogPrintf("response :%s", string(body))
			//}

			// request arbitration for the cluster
			cl.LogPrintf("CHECK: External Abitration")

			url = "http://" + conf.ArbitrationSasHosts + "/arbitrator"
			req, err = http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
			req.Header.Set("X-Custom-Header", "myvalue")
			req.Header.Set("Content-Type", "application/json")

			client = &http.Client{Timeout: timeout}
			resp, err = client.Do(req)
			if err != nil {
				cl.LogPrintf("ERROR :%s", err.Error())
				cl.SetActiceStatus("S")
				runStatus = "S"
				return
			}
			defer resp.Body.Close()

			body, _ = ioutil.ReadAll(resp.Body)

			type response struct {
				Arbitration string `json:"arbitration"`
			}
			var r response
			err = json.Unmarshal(body, &r)
			if err != nil {
				cl.LogPrintf("ERROR :abitrator says invalid JSON")
				cl.SetActiceStatus("S")
				runStatus = "S"
				return

			}
			if r.Arbitration == "winner" {
				cl.LogPrintf("INFO :Arbitrator say :winner")
				cl.SetActiceStatus("A")
				runStatus = "A"
				return
			}
			cl.LogPrintf("INFO :Arbitrator say :looser")
			cl.SetActiceStatus("S")
			runStatus = "S"
			return

		}

	}

}
