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
	"sync"
	"time"

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
		handler := r.HandlerFunc
		if r.Pattern != "/health" && r.Pattern != "/status" && r.Pattern != "/stats" && RepMan.Confs["arbitrator"].ArbitratorURICheck {
			handler = uriCheckMiddleware(handler)
		}
		router.
			Methods(r.Method).
			Path(r.Pattern).
			Name(r.Name).
			Handler(handler)
	}

	return router
}

var (
	uriCacheMu sync.RWMutex
	uriCache   = make(map[string]time.Time)
	uriCacheTTL = 5 * time.Minute
	crmClient  = &http.Client{Timeout: 5 * time.Second}
)

func uriCheckMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		uri := r.URL.Query().Get("uri")
		if uri == "" {
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "missing required uri parameter"})
			log.Warnf("Rejected request to %s: missing uri parameter", r.URL.Path)
			return
		}

		uriCacheMu.RLock()
		if t, ok := uriCache[uri]; ok && time.Since(t) < uriCacheTTL {
			uriCacheMu.RUnlock()
			next(w, r)
			return
		}
		uriCacheMu.RUnlock()

		checkURL := RepMan.Confs["arbitrator"].ArbitratorURICheckURL
		if checkURL == "" {
			log.Warn("arbitrator-uri-check enabled but arbitrator-uri-check-url not set, rejecting all requests")
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "uri check not configured"})
			return
		}

		resp, err := crmClient.Get(checkURL + "?uri=" + uri)
		if err != nil {
			log.Warnf("CRM uri check failed for %s: %s", uri, err)
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]string{"error": "uri check unavailable"})
			return
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			log.Warnf("Rejected request to %s: CRM returned %d for uri %s", r.URL.Path, resp.StatusCode, uri)
			w.Header().Set("Content-Type", "application/json; charset=UTF-8")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]string{"error": "uri not registered"})
			return
		}

		uriCacheMu.Lock()
		uriCache[uri] = time.Now()
		uriCacheMu.Unlock()

		next(w, r)
	}
}

var rs = routes{
	route{
		"Health",
		"GET",
		"/health",
		handlerHealth,
	},
	route{
		"Status",
		"GET",
		"/status",
		handlerHealth,
	},
	route{
		"Stats",
		"GET",
		"/stats",
		handlerStats,
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
	arbitratorCmd.Flags().BoolVar(&conf.ArbitratorURICheck, "arbitrator-uri-check", false, "When true, validate client URI against CRM before accepting requests")
	arbitratorCmd.Flags().StringVar(&conf.ArbitratorURICheckURL, "arbitrator-uri-check-url", "", "CRM health endpoint URL for URI validation (e.g. https://crm.cloud18.io/api/health)")

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

type clusterStats struct {
	Cluster   string           `json:"cluster"`
	Instances []instanceStats  `json:"instances"`
}

type instanceStats struct {
	UID    int    `json:"uid"`
	Status string `json:"status"`
	Date   string `json:"last_heartbeat"`
	Hosts  int    `json:"hosts"`
	Failed int    `json:"failed"`
}

func handlerStats(w http.ResponseWriter, r *http.Request) {
	db, err := getArbitratorDB()
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "Database unavailable")
		return
	}

	statsQuery := "SELECT cluster, uid, status, date, hosts, failed FROM heartbeat ORDER BY cluster, uid"
	if db.DriverName() == "mysql" {
		statsQuery = "SELECT cluster, uid, status, date, hosts, failed FROM replication_manager_schema.heartbeat ORDER BY cluster, uid"
	}
	rows, err := db.Queryx(statsQuery)
	if err != nil {
		w.WriteHeader(500)
		fmt.Fprint(w, "Query error")
		return
	}
	defer rows.Close()

	clusters := make(map[string]*clusterStats)
	var order []string
	for rows.Next() {
		var cluster, status, date string
		var uid, hosts, failed int
		if err := rows.Scan(&cluster, &uid, &status, &date, &hosts, &failed); err != nil {
			continue
		}
		statusLabel := "Standby"
		if status == "E" {
			statusLabel = "Active"
		}
		if _, ok := clusters[cluster]; !ok {
			clusters[cluster] = &clusterStats{Cluster: cluster}
			order = append(order, cluster)
		}
		clusters[cluster].Instances = append(clusters[cluster].Instances, instanceStats{
			UID:    uid,
			Status: statusLabel,
			Date:   date,
			Hosts:  hosts,
			Failed: failed,
		})
	}

	crmStatus := ""
	crmOK := false
	if RepMan.Confs["arbitrator"].ArbitratorURICheck {
		checkURL := RepMan.Confs["arbitrator"].ArbitratorURICheckURL
		if checkURL == "" {
			crmStatus = "Not configured"
		} else {
			resp, err := crmClient.Get(checkURL)
			if err != nil {
				crmStatus = "Unreachable"
			} else {
				resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					crmStatus = "OK"
					crmOK = true
				} else {
					crmStatus = fmt.Sprintf("HTTP %d", resp.StatusCode)
				}
			}
		}
	}

	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		result := map[string]interface{}{}
		var clusterList []clusterStats
		for _, name := range order {
			clusterList = append(clusterList, *clusters[name])
		}
		result["clusters"] = clusterList
		if RepMan.Confs["arbitrator"].ArbitratorURICheck {
			result["crm"] = map[string]interface{}{"status": crmStatus, "ok": crmOK}
		}
		json.NewEncoder(w).Encode(result)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=UTF-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head><title>Arbitrator Stats</title>
<meta http-equiv="refresh" content="10">
<style>
body{font-family:sans-serif;margin:20px;background:#1a1a2e;color:#e0e0e0}
h1{color:#00d4aa}
table{border-collapse:collapse;margin-bottom:20px;width:100%;max-width:800px}
th{background:#16213e;color:#00d4aa;padding:8px 12px;text-align:left;border:1px solid #0f3460}
td{padding:6px 12px;border:1px solid #0f3460}
tr:nth-child(even){background:#16213e}
.active{color:#00d4aa;font-weight:bold}
.standby{color:#e94560}
.failed{color:#e94560;font-weight:bold}
.ok{color:#00d4aa}
.err{color:#e94560}
h2{color:#e0e0e0;margin-top:30px}
.footer{color:#666;font-size:12px;margin-top:40px}
.info{margin-bottom:20px}
</style></head><body>
<h1>Arbitrator Statistics</h1>`)

	if RepMan.Confs["arbitrator"].ArbitratorURICheck {
		crmClass := "err"
		if crmOK {
			crmClass = "ok"
		}
		fmt.Fprintf(w, `<div class="info">URI Check: enabled · CRM: <span class="%s">%s</span></div>`, crmClass, crmStatus)
	} else {
		fmt.Fprint(w, `<div class="info">URI Check: disabled</div>`)
	}

	if len(order) == 0 {
		fmt.Fprint(w, "<p>No clusters registered.</p>")
	}

	for _, name := range order {
		cs := clusters[name]
		fmt.Fprintf(w, `<h2>%s</h2>
<table>
<tr><th>Instance</th><th>Status</th><th>Last Heartbeat</th><th>Hosts</th><th>Failed</th></tr>`, cs.Cluster)
		for _, inst := range cs.Instances {
			statusClass := "standby"
			if inst.Status == "Active" {
				statusClass = "active"
			}
			failedClass := ""
			if inst.Failed > 0 {
				failedClass = ` class="failed"`
			}
			fmt.Fprintf(w, `<tr><td>%d</td><td class="%s">%s</td><td>%s</td><td>%d</td><td%s>%d</td></tr>`,
				inst.UID, statusClass, inst.Status, inst.Date, inst.Hosts, failedClass, inst.Failed)
		}
		fmt.Fprint(w, "</table>")
	}

	fmt.Fprint(w, `<div class="footer">Auto-refresh every 10s · <a href="/stats?format=json" style="color:#00d4aa">JSON</a></div></body></html>`)
}
