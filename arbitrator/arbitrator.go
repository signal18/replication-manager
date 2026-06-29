//go:build arbitrator
// +build arbitrator

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package arbitrator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/jmoiron/sqlx"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/server"
	"github.com/signal18/replication-manager/utils/dbhelper"
	"github.com/signal18/replication-manager/utils/misc"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	_ "modernc.org/sqlite"
)

var (
	RepMan     *server.ReplicationManager
	memprofile string
	// Version is the semantic version number, e.g. 1.0.1
	Version string
	// Provisoning to add flags for compile
	// FullVersion is the semantic version number + git commit hash
	FullVersion string
	// Build is the build date of replication-manager
	Build       string
	GoOS        string = "linux"
	GoArch      string = "amd64"
	conf        config.Config
	WithTarball string
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
		"Health",
		"GET",
		"/health",
		handlerHealth,
	},
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
		"POST",
		"/forget/",
		handlerForget,
	},
}

type response struct {
	Arbitration   string `json:"arbitration"`
	ElectedMaster string `json:"master"`
}

var (
	arbitratorDB *sqlx.DB
)

func init() {
	cobra.OnInitialize()
	rootCmd.AddCommand(arbitratorCmd)
	arbitratorCmd.Flags().StringVar(&conf.ArbitratorAddress, "arbitrator-bind-address", "0.0.0.0:10001", "Arbitrator API port")
	arbitratorCmd.Flags().StringVar(&conf.ArbitratorDriver, "arbitrator-driver", "sqlite", "sqlite|mysql, use a local sqllite or use a mysql backend")

}

var arbitratorCmd = &cobra.Command{
	Use:   "arbitrator",
	Short: "Arbitrator environment",
	Long:  `The arbitrator is used for false positive detection`,
	Run: func(cmd *cobra.Command, args []string) {
		RepMan = new(server.ReplicationManager)
		RepMan.InitConfig(conf, false)

		if _, ok := RepMan.Confs["arbitrator"]; !ok {
			log.Fatal("Could not find [arbitrator] configuration section. Provide a config file with an [arbitrator] section or use the Docker env-backed entrypoint configuration.")
		}

		var err error
		arbitratorDB, err = getArbitratorBackendStorageConnection()
		if err != nil {
			log.Fatal("Error opening arbitrator database: ", err)
		}

		if RepMan.Confs["arbitrator"].ArbitratorDriver == "sqlite" {
			arbitratorDB.SetMaxOpenConns(1)
			arbitratorDB.SetMaxIdleConns(1)
		}

		err = arbitratorDB.Ping()
		if err != nil {
			log.Fatal(err)
		}

		err = dbhelper.SetHeartbeatTable(arbitratorDB)
		if err != nil {
			log.WithError(err).Error("Error creating tables")
		}
		router := newRouter()
		log.Infof("Arbitrator listening on %s", RepMan.Confs["arbitrator"].ArbitratorAddress)
		log.Fatal(http.ListenAndServe(RepMan.Confs["arbitrator"].ArbitratorAddress, router))
	},
}

func getArbitratorBackendStorageConnection() (*sqlx.DB, error) {

	var err error
	var db *sqlx.DB
	if RepMan.Confs["arbitrator"].ArbitratorDriver == "sqlite" {
		db, err = dbhelper.SQLiteConnect(conf.WorkingDir)
	}
	if RepMan.Confs["arbitrator"].ArbitratorDriver == "mysql" {
		hosts := strings.Split(RepMan.Confs["arbitrator"].Hosts, ",")
		if len(hosts) == 0 || hosts[0] == "" {
			return nil, fmt.Errorf("arbitrator mysql backend requires [arbitrator].db-servers-hosts (example: \"127.0.0.1:3306\")")
		}
		arbConf := RepMan.Confs["arbitrator"]
		credential := arbConf.DecryptSecretValue("db-servers-credential", arbConf.User)
		if !strings.Contains(credential, ":") {
			return nil, fmt.Errorf("arbitrator mysql backend requires [arbitrator].db-servers-credential in \"user:password\" format")
		}
		user, pass := misc.SplitPair(credential)
		for _, h := range hosts {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			host, port := misc.SplitHostPort(h)
			db, err = dbhelper.MySQLConnect(user, pass, dbhelper.GetAddress(host, port, ""), fmt.Sprintf("timeout=%ds", RepMan.Confs["arbitrator"].Timeout))
			if err == nil {
				if pingErr := db.Ping(); pingErr == nil {
					log.Infof("Arbitrator connected to MySQL backend %s", h)
					return db, nil
				}
				db.Close()
			}
			log.Warnf("Arbitrator failed to connect to MySQL backend %s: %s", h, err)
		}
		return nil, fmt.Errorf("arbitrator could not connect to any MySQL backend from: %s", RepMan.Confs["arbitrator"].Hosts)
	}
	return db, err
}

func getArbitratorDB() (*sqlx.DB, error) {
	if arbitratorDB == nil {
		return nil, fmt.Errorf("arbitrator database is not initialized")
	}
	return arbitratorDB, nil
}

func handlerHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")

	db, err := getArbitratorDB()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
		return
	}

	if err := db.Ping(); err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "failed"})
		return
	}

	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "ok"}); err != nil {
		log.Errorln(err)
	}
}

func handlerArbitrator(w http.ResponseWriter, r *http.Request) {
	var h server.Heartbeat
	body, err := io.ReadAll(io.LimitReader(r.Body, 1048576))
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
			w.WriteHeader(500)
			log.Errorln(err)
			return
		}
		return
	}
	var send response

	db, err := getArbitratorDB()
	if err != nil {
		log.Errorf("Error opening arbitrator database: %s", err)
		w.WriteHeader(500)
		return
	}
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
		log.Errorln(err)
		return
	}

}
func handlerHeartbeat(w http.ResponseWriter, r *http.Request) {
	var h server.Heartbeat
	body, err := io.ReadAll(io.LimitReader(r.Body, 1048576))

	if err != nil {
		log.Errorln(err)
		w.WriteHeader(500)
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

	var send string
	db, err := getArbitratorDB()
	if err != nil {
		log.Errorf("Error opening arbitrator database: %s", err)
		w.WriteHeader(500)
		return
	}
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
	var h server.Heartbeat
	body, err := io.ReadAll(io.LimitReader(r.Body, 1048576))

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
	db, err := getArbitratorDB()
	if err != nil {
		log.Errorf("Error opening arbitrator database: %s", err)
		w.WriteHeader(500)
		return
	}
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
