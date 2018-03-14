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
	"github.com/signal18/replication-manager/crypto"
	"github.com/signal18/replication-manager/misc"
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
	router.HandleFunc("/", handlerApp)
	// page to view which does not need authorization
	router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
	router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(confs[currentClusterName].HttpRoot)))
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
	router.Handle("/api/clusters/{clusterName}/actions/master-physical-backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterMasterPhysicalBackup)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-master", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsMasterStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/is-slave", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortIsSlaveStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/{serverPort}/backup", negroni.New(
		negroni.Wrap(http.HandlerFunc(handlerMuxServersPortBackup)),
	))

	//PROTECTED ENDPOINTS FOR SETTINGS
	router.Handle("/api/monitor", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxReplicationManager)),
	))

	router.Handle("/api/clusters/{clusterName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxCluster)),
	))

	router.Handle("/api/clusters/{clusterName}/settings/actions/reload", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSettingsReload)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/switch/{settingName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchSettings)),
	))
	router.Handle("/api/clusters/{clusterName}/settings/actions/set/{settingName}/{settingValue}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSetSettings)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS ACTIONS

	router.Handle("/api/clusters/{clusterName}/actions/reset-failover-control", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterResetFailoverControl)),
	))

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
	router.Handle("/api/clusters/{clusterName}/services/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServicesProvision)),
	))
	router.Handle("/api/clusters/{clusterName}/services/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServicesUnprovision)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/stop-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStopTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/start-traffic", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxStartTraffic)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/sysbench", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterSysbench)),
	))

	router.Handle("/api/clusters/{clusterName}/actions/addserver/{host}/{port}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerAdd)),
	))

	//PROTECTED ENDPOINTS FOR CLUSTERS TOPOLOGY

	router.Handle("/api/clusters/actions/add/{clusterName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusterAdd)),
	))

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

	router.Handle("/api/clusters/{clusterName}/tests/actions/run/{testName}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxOneTest)),
	))

	//PROTECTED ENDPOINTS FOR SERVERS
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/processlist", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerProcesslist)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/variables", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/status", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerVariables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/errorlog", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerErrorLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/slowlog", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerSlowLog)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/tables", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerTables)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/schemas", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerSchemas)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/innodb-status", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerInnoDBStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/all-slaves-status", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerAllSlavesStatus)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/master-status", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerMasterStatus)),
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
		negroni.Wrap(http.HandlerFunc(handlerMuxServerUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerProvision)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-physical", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupPhysical)),
	))
	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-logical", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupLogical)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-error-log", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupErrorLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/backup-slowquery-log", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerBackupSlowQueryLog)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/optimize", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerOptimize)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/reseed/{backupMethod}", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServerReseed)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/toogle-innodb-monitor", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSetInnoDBMonitor)),
	))

	router.Handle("/api/clusters/{clusterName}/servers/{serverName}/actions/skip-replication-event", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSkipReplicationEvent)),
	))

	//PROTECTED ENDPOINTS FOR PROXIES

	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/unprovision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyUnprovision)),
	))
	router.Handle("/api/clusters/{clusterName}/proxies/{proxyName}/actions/provision", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxProxyProvision)),
	))

	log.Info("Starting JWT API on " + conf.APIBind + ":" + conf.APIPort)
	err := http.ListenAndServeTLS(conf.APIBind+":"+conf.APIPort, conf.ShareDir+"/server.crt", conf.ShareDir+"/server.key", router)
	if err != nil {
		log.Errorf("JWT API can't start: %s", err)
	}

}

//////////////////////////////////////////
/////////////ENDPOINT HANDLERS////////////
/////////////////////////////////////////

func loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var user userCredentials

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error in request")
		return
	}
	k, err := readKey()
	if err != nil {
		k = nil
	}
	for _, cluster := range RepMan.Clusters {
		//validate user credentials
		apiUser, apiPass = misc.SplitPair(cluster.Conf.APIUser)
		if k != nil {
			p := crypto.Password{Key: k}
			p.CipherText = apiPass
			p.Decrypt()
			apiPass = p.PlainText
		}
		if user.Username == apiUser && user.Password == apiPass {

			signer := jwt.New(jwt.SigningMethodRS256)
			claims := signer.Claims.(jwt.MapClaims)
			//set claims
			claims["iss"] = "https://api.replication-manager.signal18.io"
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
			return
		}
	}

	w.WriteHeader(http.StatusForbidden)
	fmt.Println("Error logging in")
	fmt.Fprint(w, "Invalid credentials")
	return

	//create a rsa 256 signer

}

//AUTH TOKEN VALIDATION

func validateTokenMiddleware(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json, err := json.Marshal(apiresponse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	w.Write(json)
}

func handlerMuxClusterAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	RepMan.AddCluster(vars["clusterName"])

}

func handlerMuxReplicationManager(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err == nil {
		claims := token.Claims.(jwt.MapClaims)

		mycopy := RepMan
		var cl []string

		userinfo := claims["CustomUserInfo"]
		mycutinfo := userinfo.(map[string]interface{})
		meuser := mycutinfo["Name"].(string)

		for _, cluster := range RepMan.Clusters {
			apiUser, apiPass = misc.SplitPair(conf.APIUser)

			if strings.Contains(meuser, apiUser) {
				cl = append(cl, cluster.Name)
			}
		}
		mycopy.ClusterList = cl
		e := json.NewEncoder(w)
		e.SetIndent("", "\t")
		err := e.Encode(mycopy)

		//err := e.Encode(RepMan)
		if err != nil {
			http.Error(w, "Encoding error", 500)
			return
		}
	} else {
		http.Error(w, "token parse error", 500)
	}
}

func handlerMuxClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

func handlerMuxStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if RepMan.isStarted {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "starting"}`)
	}
}

func handlerMuxTimeout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	time.Sleep(1200 * time.Second)
	io.WriteString(w, `{"alive": "running"}`)
}
