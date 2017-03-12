package main

import (
	"encoding/json"
	"io"
	"io/ioutil"
	"net/http"
	"strconv"

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
}

type heartbeat struct {
	UUID    string `json:"uuid"`
	Secret  string `json:"secret"`
	Cluster string `json:"cluster"`
	Master  string `json:"master"`
	UID     int    `json:"id"`
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
			log.Printf("ERROR: Error creating tables.")
			panic(err)
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
