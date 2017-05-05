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

	_ "github.com/mattn/go-sqlite3"

	log "github.com/Sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/dbhelper"
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

type heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
	Status  string `json:"status"`
	Hosts   int    `json:"hosts"`
	Failed  int    `json:"failed"`
}

type response struct {
	Arbitration   string `json:"arbitration"`
	ElectedMaster string `json:"master"`
}

var (
	arbitratorPort int
)

func init() {
	rootCmd.AddCommand(arbitratorCmd)
	arbitratorCmd.Flags().IntVar(&arbitratorPort, "arbitrator-port", 8080, "Arbitrator API port")
}

var arbitratorCmd = &cobra.Command{
	Use:   "arbitrator",
	Short: "Arbitrator environment",
	Long:  `The arbitrator is used for false positive detection`,
	Run: func(cmd *cobra.Command, args []string) {
		currentCluster = new(cluster.Cluster)
		var err error
		db, err := currentCluster.InitAgent(confs["arbitrator"])
		if err != nil {
			panic(err)
		}
		currentCluster.SetLogStdout()

		err = dbhelper.SetHeartbeatTable(db)
		if err != nil {
			log.WithError(err).Error("Error creating tables")
		}
		//http.HandleFunc("/heartbeat/", handlerHeartbeat)
		//	http.HandleFunc("/abritrator/", handlerArbitrator)
		router := newRouter()
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
	log.Info("Arbitration request received: ", string(body))
	if err := json.Unmarshal(body, &h); err != nil {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(422) // unprocessable entity
		if err = json.NewEncoder(w).Encode(err); err != nil {
			panic(err)
		}
	}
	var send response
	currentCluster = new(cluster.Cluster)
	db, err := dbhelper.MemDBConnect()
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

	currentCluster = new(cluster.Cluster)
	var send string
	db, err := dbhelper.MemDBConnect()
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

	currentCluster = new(cluster.Cluster)
	var send string
	db, err := dbhelper.MemDBConnect()
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

func fHeartbeat() {
	if cfgGroup == "arbitrator" {
		return
	}
	bcksplitbrain := splitBrain

	var peerList []string
	// try to found an active peer replication-manager
	if conf.ArbitrationPeerHosts != "" {
		peerList = strings.Split(conf.ArbitrationPeerHosts, ",")
	} else {
		return
	}
	splitBrain = true
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
			if bcksplitbrain == false {
				currentCluster.LogPrintf("ERROR", "%s", err)
			}
			continue

		}
		resp, err := client.Do(req)
		if err != nil {
			if bcksplitbrain == false {
				currentCluster.LogPrintf("ERROR", "%s", err)
			}
			continue
		}

		// Callers should close resp.Body
		// when done reading from it
		// Defer the closing of the body
		defer resp.Body.Close()
		monjson, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			currentCluster.LogPrintf("ERROR", "%s", err)
		}
		// Use json.Decode for reading streams of JSON data
		var h heartbeat
		if err := json.Unmarshal(monjson, &h); err != nil {
			currentCluster.LogPrintf("ERROR", "%s", err)
		} else {
			splitBrain = false
			if conf.LogLevel > 3 {
				currentCluster.LogPrintf("DEBUG", "RETURN :%s", h)
			}
			if h.Status == "S" {
				runStatus = "A"
			} else {
				runStatus = "S"
			}
		}

	}
	if splitBrain {
		if bcksplitbrain != splitBrain {
			currentCluster.LogPrintf("INFO", "Splitbrain")
		}

		// report to arbitrator
		for _, cl := range clusters {
			if cl.LostMajority() {
				if bcksplitbrain != splitBrain {
					currentCluster.LogPrintf("INFO", "Database cluster lost majority ")
				}
			}
			url := "http://" + conf.ArbitrationSasHosts + "/heartbeat"
			var mst string
			if cl.GetMaster() != nil {
				mst = cl.GetMaster().URL
			}
			var jsonStr = []byte(`{"uuid":"` + runUUID + `","secret":"` + conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(conf.ArbitrationSasUniqueId) + `,"status":"` + runStatus + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
			req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
			req.Header.Set("X-Custom-Header", "myvalue")
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: timeout}
			resp, err := client.Do(req)
			if err != nil {
				cl.LogPrintf("ERROR", "%s", err.Error())
				cl.SetActiveStatus("S")
				runStatus = "S"
				return
			}
			defer resp.Body.Close()

		}
		// give a chance to other partitions to report if just happened
		if bcksplitbrain != splitBrain {
			time.Sleep(5 * time.Second)
		}
		// request arbitration for all cluster
		for _, cl := range clusters {

			if bcksplitbrain != splitBrain {
				cl.LogPrintf("INFO", "External Arbitration check requested")
			}
			url := "http://" + conf.ArbitrationSasHosts + "/arbitrator"
			var mst string
			if cl.GetMaster() != nil {
				mst = cl.GetMaster().URL
			}
			var jsonStr = []byte(`{"uuid":"` + runUUID + `","secret":"` + conf.ArbitrationSasSecret + `","cluster":"` + cl.GetName() + `","master":"` + mst + `","id":` + strconv.Itoa(conf.ArbitrationSasUniqueId) + `,"status":"` + runStatus + `","hosts":` + strconv.Itoa(len(cl.GetServers())) + `,"failed":` + strconv.Itoa(cl.CountFailed(cl.GetServers())) + `}`)
			req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonStr))
			req.Header.Set("X-Custom-Header", "myvalue")
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{Timeout: timeout}
			resp, err := client.Do(req)
			if err != nil {
				cl.LogPrintf("ERROR", "%s", err.Error())
				cl.SetActiveStatus("S")
				cl.SetMasterReadOnly()
				runStatus = "S"
				return
			}
			defer resp.Body.Close()

			body, _ := ioutil.ReadAll(resp.Body)

			type response struct {
				Arbitration string `json:"arbitration"`
				Master      string `json:"master"`
			}
			var r response
			err = json.Unmarshal(body, &r)
			if err != nil {
				cl.LogPrintf("ERROR", "Arbitrator says invalid JSON")
				cl.SetActiveStatus("S")
				cl.SetMasterReadOnly()
				runStatus = "S"
				return

			}
			if r.Arbitration == "winner" {
				if bcksplitbrain != splitBrain {
					cl.LogPrintf("INFO", "Arbitrator says winner")
				}
				cl.SetActiveStatus("A")
				runStatus = "A"
				return
			}
			if bcksplitbrain != splitBrain {
				cl.LogPrintf("INFO", "Arbitrator says loser")
				if cl.GetMaster() != nil {
					mst = cl.GetMaster().URL
				}
				if r.Master != mst {
					cl.SetMasterReadOnly()
				}
			}
			cl.SetActiveStatus("S")
			runStatus = "S"
			return

		}

	}

}
