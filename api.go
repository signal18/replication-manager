// +build server

// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017 Signal 18 SARL
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/codegangsta/negroni"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/regtest"
)

//RSA KEYS AND INITIALISATION

var signingKey, verificationKey []byte
var apiPass string
var apiUser string

func initKeys() {
	var (
		err         error
		privKey     *rsa.PrivateKey
		pubKey      *rsa.PublicKey
		pubKeyBytes []byte
	)

	privKey, err = rsa.GenerateKey(cryptorand.Reader, 2048)
	if err != nil {
		log.Fatal("Error generating private key")
	}
	pubKey = &privKey.PublicKey //hmm, this is stdlib manner...

	// Create signingKey from privKey
	// prepare PEM block
	var privPEMBlock = &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey), // serialize private key bytes
	}
	// serialize pem
	privKeyPEMBuffer := new(bytes.Buffer)
	pem.Encode(privKeyPEMBuffer, privPEMBlock)
	//done
	signingKey = privKeyPEMBuffer.Bytes()

	//fmt.Println(string(signingKey))

	// create verificationKey from pubKey. Also in PEM-format
	pubKeyBytes, err = x509.MarshalPKIXPublicKey(pubKey) //serialize key bytes
	if err != nil {
		// heh, fatality
		log.Fatal("Error marshalling public key")
	}

	var pubPEMBlock = &pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: pubKeyBytes,
	}
	// serialize pem
	pubKeyPEMBuffer := new(bytes.Buffer)
	pem.Encode(pubKeyPEMBuffer, pubPEMBlock)
	// done
	verificationKey = pubKeyPEMBuffer.Bytes()

	//	fmt.Println(string(verificationKey))
}

//STRUCT DEFINITIONS

type userCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type apiresponse struct {
	Data string `json:"data"`
}

type token struct {
	Token string `json:"token"`
}

//SERVER ENTRY POINT

func apiserver() {
	initKeys()
	//PUBLIC ENDPOINTS
	router := mux.NewRouter()
	router.HandleFunc("/api/login", loginHandler)
	router.Handle("/api/clusters", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusters)),
	))
	router.Handle("/api/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxStatus)),
	))
	router.Handle("/api/timeout", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxTimeout)),
	))
	router.Handle("/api/clusters/{clusterName}/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/master-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/slave-status", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortSlaveStatus)),
	))

	//PROTECTED ENDPOINTS FOR SETTINGS
	router.Handle("/api/clusters/{clusterName}/settings", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/reload", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/interactive", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchInteractive)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/readonly", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchReadOnly)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/verbosity", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchVerbosity)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/autorejoin", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchRejoin)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/rejoinflashback", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchRejoinFlashback)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/rejoinmysqldump", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchRejoinMysqldump)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/failoversync", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchFailoverSync)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/switch/swithoversync", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchSwitchoverSync)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/reset/failovercontrol", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxResetFailoverControl)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS ACTIONS

	router.Handle("/api/clusters/{clusterName}/actions/switchover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/failover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxFailover)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/bootstrap/{topology}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxBootstrapReplication)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/replication/cleanup", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxBootstrapReplicationCleanup)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/services/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProvisionServices)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStopTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStartTraffic)),
	))
	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStopTraffic)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/{clusterName}/topology/servers", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/master", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxMaster)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/slaves", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSlaves)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/logs", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxLog)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/proxies", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxies)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/alerts", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxAlerts)),
	))
	router.Handle("/api/clusters/{clusterName}/topology/crashes", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxCrashes)),
	))

	//PROTECTED ENDPOINTS FOR TESTS

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/all", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxTests)),
	))
	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxOneTest)),
	))

	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/servers/actions/add/{host}/{port}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerAdd)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/start", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerStart)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/stop", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerStop)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/maintenance", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerMaintenance)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerUnprovision)),
	))

	//PROTECTED ENDPOINTS FOR PROXIES

	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyUnprovision)),
	))

	err := http.ListenAndServeTLS(conf.APIBind+":"+conf.APIPort, conf.ShareDir+"/server.crt", conf.ShareDir+"/server.key", router)
	if err != nil {
		log.Println("ERROR", "JWT API can't start: %s", err)
	}
	log.Println("JWT API listening on " + conf.APIBind + ":" + conf.APIPort)

}

//////////////////////////////////////////
/////////////ENDPOINT HANDLERS////////////
/////////////////////////////////////////

func loginHandler(w http.ResponseWriter, r *http.Request) {

	var user userCredentials

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error in request")
		return
	}

	//validate user credentials
	if user.Username != apiUser || user.Password != apiPass {
		w.WriteHeader(http.StatusForbidden)
		fmt.Println("Error logging in")
		fmt.Fprint(w, "Invalid credentials")
		return
	}

	//create a rsa 256 signer
	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	//set claims
	claims["iss"] = "admin"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Minute * 120).Unix()
	claims["jti"] = "1" // should be user ID(?)
	claims["CustomUserInfo"] = struct {
		Name string
		Role string
	}{user.Username, "Member"}
	signer.Claims = claims
	sk, _ := jwt.ParseRSAPrivateKeyFromPEM(signingKey)
	//sk, _ := jwt.ParseRSAPublicKeyFromPEM(signingKey)

	tokenString, err := signer.SignedString(sk)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintln(w, "Error while signing the token")
		log.Printf("Error signing token: %v\n", err)
	}

	//create a token instance using the token string
	resp := token{tokenString}
	jsonResponse(resp, w)

}

//AUTH TOKEN VALIDATION

func validateTokenMiddleware(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {

	//validate token
	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor,
		func(token *jwt.Token) (interface{}, error) {
			vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
			return vk, nil
		})

	if err == nil {
		if token.Valid {
			next(w, r)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, "Token is not valid")
		}
	} else {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, "Unauthorised access to this resource"+err.Error())
	}
}

//HELPER FUNCTIONS

func jsonResponse(apiresponse interface{}, w http.ResponseWriter) {

	json, err := json.Marshal(apiresponse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	w.Write(json)
}

func handlerMuxServers(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetServers())
		var srvs []*cluster.ServerMonitor

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}

		for i := range srvs {
			srvs[i].Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srvs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxSlaves(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetSlaves())
		var srvs []*cluster.ServerMonitor

		err := json.Unmarshal(data, &srvs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		for i := range srvs {
			srvs[i].Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(srvs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxProxies(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		data, _ := json.Marshal(mycluster.GetProxies())
		var prxs []*cluster.Proxy
		err := json.Unmarshal(data, &prxs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err = e.Encode(prxs)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxAlerts(w http.ResponseWriter, r *http.Request) {
	a := new(cluster.Alerts)
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		a.Errors = mycluster.GetStateMachine().GetOpenErrors()
		a.Warnings = mycluster.GetStateMachine().GetOpenWarnings()
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(a)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.MasterFailover(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SetTraffic(true)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SetTraffic(false)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		err := mycluster.BootstrapReplicationCleanup()
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error Cleanup Replication: %s", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {

		switch vars["topology"] {
		case "master-slave":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "master-slave-no-gtid":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(true)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(true)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-tier-slave":
			mycluster.SetMultiTierSlave(true)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterWsrep(false)
		case "maxscale-binlog":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(true)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master-ring":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterRing(true)
			mycluster.SetMultiMasterWsrep(false)
		case "multi-master-wsrep":
			mycluster.SetMultiTierSlave(false)
			mycluster.SetForceSlaveNoGtid(false)
			mycluster.SetMultiMaster(false)
			mycluster.SetBinlogServer(false)
			mycluster.SetMultiMasterRing(false)
			mycluster.SetMultiMasterWsrep(true)

		}
		err := mycluster.BootstrapReplication()
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxBootstrapServices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		err := mycluster.ProvisionServices()
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error Bootstrap Micro Services: ", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxProvisionServices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		err := mycluster.Bootstrap()
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error Bootstrap Micro Services + replication ", err)
			http.Error(w, err.Error(), 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.ResetFailoverCtr()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.LogPrintf("INFO", "Rest API receive switchover request")
		savedPrefMaster := mycluster.GetConf().PrefMaster
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if mycluster.IsMasterFailed() {
			mycluster.LogPrintf("ERROR", "Master failed, cannot initiate switchover")
			http.Error(w, "Master failed", http.StatusBadRequest)
			return
		}
		r.ParseForm() // Parses the request body
		newPrefMaster := r.Form.Get("prefmaster")
		mycluster.LogPrintf("INFO", "Was ask for prefered master: %s", newPrefMaster)
		if mycluster.IsInHostList(newPrefMaster) {
			mycluster.SetPrefMaster(newPrefMaster)
		}
		mycluster.SwitchoverWaitTest()
		mycluster.SetPrefMaster(savedPrefMaster)
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxMaster(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		m := mycluster.GetMaster()
		var srvs *cluster.ServerMonitor
		if m != nil {

			data, _ := json.Marshal(m)

			err := json.Unmarshal(data, &srvs)
			if err != nil {
				mycluster.LogPrintf("ERROR", "API Error decoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				return
			}
			srvs.Pass = "XXXXXXXX"
		}
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(srvs)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxSwitchInteractive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.ToggleInteractive()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSwitchVerbosity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchVerbosity()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchRejoin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchRejoin()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchRejoinMysqldump(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchRejoinDump()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchRejoinFlashback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchRejoinFlashback()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchRejoinSemisync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchRejoinSemisync()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchRplchecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchRplChecks()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchSwitchoverSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchSwitchoverSync()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchFailoverSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchFailSync()
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxSwitchReadOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.SwitchReadOnly()
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}
func handlerMuxLog(w http.ResponseWriter, r *http.Request) {
	var clusterlogs []string
	vars := mux.Vars(r)
	for _, slog := range tlog.Buffer {
		if strings.Contains(slog, vars["clusterName"]) {
			clusterlogs = append(clusterlogs, slog)
		}
	}
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(clusterlogs)
	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxCrashes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycluster.GetCrashes())
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		r.ParseForm() // Parses the request body
		if r.Form.Get("provision") == "true" {
			mycluster.SetTestStartCluster(true)
		}
		if r.Form.Get("unprovision") == "true" {
			mycluster.SetTestStopCluster(true)
		}
		regtest := new(regtest.RegTest)
		res := regtest.RunAllTests(mycluster, vars["testName"])
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")

		if len(res) > 0 {
			err := e.Encode(res[0])
			if err != nil {
				mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}
		} else {
			var test cluster.Test
			test.Result = "FAIL"
			test.Name = vars["testName"]
			err := e.Encode(test)
			if err != nil {
				mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
				http.Error(w, "Encoding error", 500)
				mycluster.SetTestStartCluster(false)
				mycluster.SetTestStopCluster(false)
				return
			}

		}
	} else {

		http.Error(w, "No cluster", 500)
		mycluster.SetTestStartCluster(false)
		mycluster.SetTestStopCluster(false)
		return
	}
	mycluster.SetTestStartCluster(false)
	mycluster.SetTestStopCluster(false)
	return
}

func handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		regtest := new(regtest.RegTest)

		res := regtest.RunAllTests(mycluster, "ALL")
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(res)
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
	return
}

func handlerMuxSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		s := new(Settings)
		s.Enterprise = fmt.Sprintf("%v", mycluster.GetConf().Enterprise)
		s.Interactive = fmt.Sprintf("%v", mycluster.GetConf().Interactive)
		s.RplChecks = fmt.Sprintf("%v", mycluster.GetConf().RplChecks)
		s.FailSync = fmt.Sprintf("%v", mycluster.GetConf().FailSync)
		s.SwitchSync = fmt.Sprintf("%v", mycluster.GetConf().SwitchSync)
		s.Rejoin = fmt.Sprintf("%v", mycluster.GetConf().Autorejoin)
		s.RejoinBackupBinlog = fmt.Sprintf("%v", mycluster.GetConf().AutorejoinBackupBinlog)
		s.RejoinSemiSync = fmt.Sprintf("%v", mycluster.GetConf().AutorejoinSemisync)
		s.RejoinFlashback = fmt.Sprintf("%v", mycluster.GetConf().AutorejoinFlashback)
		s.RejoinDump = fmt.Sprintf("%v", mycluster.GetConf().AutorejoinMysqldump)
		s.RejoinUnsafe = fmt.Sprintf("%v", mycluster.GetConf().FailRestartUnsafe)
		s.MaxDelay = fmt.Sprintf("%v", mycluster.GetConf().FailMaxDelay)
		s.FailoverCtr = fmt.Sprintf("%d", mycluster.GetFailoverCtr())
		s.Faillimit = fmt.Sprintf("%d", mycluster.GetConf().FailLimit)
		s.MonHearbeats = fmt.Sprintf("%d", mycluster.GetStateMachine().GetHeartbeats())
		s.Uptime = mycluster.GetStateMachine().GetUptime()
		s.UptimeFailable = mycluster.GetStateMachine().GetUptimeFailable()
		s.UptimeSemiSync = mycluster.GetStateMachine().GetUptimeSemiSync()
		s.Test = fmt.Sprintf("%v", mycluster.GetConf().Test)
		s.Heartbeat = fmt.Sprintf("%v", mycluster.GetConf().Heartbeat)
		s.Status = fmt.Sprintf("%v", runStatus)
		s.ConfGroup = fmt.Sprintf("%s", mycluster.GetName())
		s.MonitoringTicker = fmt.Sprintf("%d", mycluster.GetConf().MonitoringTicker)
		s.FailResetTime = fmt.Sprintf("%d", mycluster.GetConf().FailResetTime)
		s.ToSessionEnd = fmt.Sprintf("%d", mycluster.GetConf().SessionLifeTime)
		s.HttpAuth = fmt.Sprintf("%v", mycluster.GetConf().HttpAuth)
		s.HttpBootstrapButton = fmt.Sprintf("%v", mycluster.GetConf().HttpBootstrapButton)
		s.Clusters = cfgGroupList
		regtest := new(regtest.RegTest)
		s.RegTests = regtest.GetTests()
		if mycluster.GetLogLevel() > 0 {
			s.Verbose = fmt.Sprintf("%v", true)
		} else {
			s.Verbose = fmt.Sprintf("%v", false)
		}
		if currentCluster.GetFailoverTs() != 0 {
			t := time.Unix(mycluster.GetFailoverTs(), 0)
			s.LastFailover = t.String()
		} else {
			s.LastFailover = "N/A"
		}
		s.Topology = mycluster.GetTopology()
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(s)
		if err != nil {
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		initConfig()
		mycluster.ReloadConfig(confs[vars["clusterName"]])
	} else {

		http.Error(w, "No cluster", 500)
		return
	}

}

func handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		mycluster.LogPrintf("INFO", "Rest API receive new server to be added %s", vars["host"]+":"+vars["port"])
		mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])
	} else {

		http.Error(w, "No cluster", 500)
		return
	}

}

func handlerMuxServerStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		mycluster.StopDatabaseService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServerMaintenance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if node != nil {
			mycluster.SwitchServerMaintenance(node.ServerID)

		} else {
			http.Error(w, "No cluster", 500)
			return
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServerStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		mycluster.StartDatabaseService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServerProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		mycluster.InitDatabaseService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServerUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		mycluster.UnprovisionDatabaseService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxProxyProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetProxyFromName(vars["proxyName"])
		mycluster.InitProxyService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxProxyUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetProxyFromName(vars["proxyName"])
		mycluster.UnprovisionProxyService(node)
	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxClusters(w http.ResponseWriter, r *http.Request) {
	s := new(Settings)
	s.Clusters = cfgGroupList
	regtest := new(regtest.RegTest)
	s.RegTests = regtest.GetTests()
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(s)
	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxServersMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Master!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServersPortMasterStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if node == nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Node not Found!"))
		}
		if mycluster.IsInFailover() == false && mycluster.IsActive() && node.IsMaster() && node.IsDown() == false && node.IsMaintenance == false && node.IsReadOnly() == false {
			w.Write([]byte("200 -Valid Master!"))
			return

		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Master!"))
		}
	} else {
		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxServersSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromName(vars["serverName"])
		if mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && node.HasReplicationIssue() == false {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Slave!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}
func handlerMuxServersPortSlaveStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	if mycluster != nil {
		node := mycluster.GetServerFromURL(vars["serverName"] + ":" + vars["serverPort"])
		if mycluster.IsActive() && node.IsDown() == false && node.IsMaintenance == false && node.HasReplicationIssue() == false {
			w.Write([]byte("200 -Valid Slave!"))
			return
		} else {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("503 -Not a Valid Slave!"))
		}

	} else {

		http.Error(w, "No cluster", 500)
		return
	}
}

func handlerMuxStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if isStarted {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "starting"}`)
	}
}

func handlerMuxClusterStatus(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if mycluster.GetStatus() {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "errors"}`)
	}
}

func handlerMuxTimeout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	time.Sleep(1200 * time.Second)
	io.WriteString(w, `{"alive": "running"}`)
}
