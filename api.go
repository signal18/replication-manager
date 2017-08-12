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

	log "github.com/Sirupsen/logrus"

	"github.com/codegangsta/negroni"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gorilla/mux"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/regtest"
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
	router.Handle("/api/clusters/{clusterName}/actions/services/bootstrap", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxBootstrapServices)),
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

	log.Println("JWT API listening on " + conf.APIBind + ":" + conf.APIPort)
	http.ListenAndServeTLS(conf.APIBind+":"+conf.APIPort, conf.ShareDir+"/server.crt", conf.ShareDir+"/server.key", router)
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
}

func handlerMuxSlaves(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])

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
}

func handlerMuxProxies(w http.ResponseWriter, r *http.Request) {
	//marshal unmarchal for ofuscation deep copy of struc
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
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
}

func handlerMuxAlerts(w http.ResponseWriter, r *http.Request) {
	a := new(alerts)
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
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
}

func handlerMuxFailover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.MasterFailover(true)
	return
}

func handlerMuxStartTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SetTraffic(true)
	return
}

func handlerMuxStopTraffic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SetTraffic(false)
	return
}

func handlerMuxBootstrapReplicationCleanup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	err := mycluster.BootstrapReplicationCleanup()
	if err != nil {
		mycluster.LogPrintf("ERROR", "API Error Cleanup Replication: %s", err)
		http.Error(w, err.Error(), 500)
		return
	}
	return
}

func handlerMuxBootstrapReplication(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])

	switch vars["topology"] {
	case "master-slave":
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
	case "master-slave-no-gtid":
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(true)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
	case "multi-master":
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(true)
		mycluster.SetBinlogServer(false)
	case "multi-tier-slave":
		mycluster.SetMultiTierSlave(true)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(false)
	case "maxscale-binlog":
		mycluster.SetMultiTierSlave(false)
		mycluster.SetForceSlaveNoGtid(false)
		mycluster.SetMultiMaster(false)
		mycluster.SetBinlogServer(true)
	case "multi-master-ring":
	}
	err := mycluster.BootstrapReplication()
	if err != nil {
		mycluster.LogPrintf("ERROR", "API Error Bootstrap Replication: %s", err)
		http.Error(w, err.Error(), 500)
		return
	}
	return
}

func handlerMuxBootstrapServices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	err := mycluster.BootstrapServices()
	if err != nil {
		mycluster.LogPrintf("ERROR", "API Error Bootstrap Micro Services: ", err)
		http.Error(w, err.Error(), 500)
		return
	}
	return
}

func handlerMuxBootstrap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	err := mycluster.Bootstrap()
	if err != nil {
		mycluster.LogPrintf("ERROR", "API Error Bootstrap Micro Services + replication ", err)
		http.Error(w, err.Error(), 500)
		return
	}
	return
}

func handlerMuxResetFailoverControl(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.ResetFailoverCtr()

	return
}

func handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if mycluster.IsMasterFailed() {
		mycluster.LogPrintf("ERROR", " Master failed, cannot initiate switchover")
		http.Error(w, "Master failed", http.StatusBadRequest)
		return
	}
	mycluster.LogPrintf("INFO", "Rest API receive switchover request")
	mycluster.SwitchoverWaitTest()
	return
}

func handlerMuxMaster(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
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
		mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxSwitchInteractive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.ToggleInteractive()
	return
}

func handlerMuxSwitchVerbosity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchVerbosity()
	return
}
func handlerMuxSwitchRejoin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchRejoin()
	return
}
func handlerMuxSwitchRejoinMysqldump(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchRejoinDump()
	return
}
func handlerMuxSwitchRejoinFlashback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchRejoinFlashback()
	return
}
func handlerMuxSwitchRejoinSemisync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchRejoinSemisync()
	return
}
func handlerMuxSwitchRplchecks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchRplChecks()
	return
}
func handlerMuxSwitchSwitchoverSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchSwitchoverSync()
	return
}
func handlerMuxSwitchFailoverSync(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchFailSync()
	return
}
func handlerMuxSwitchReadOnly(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.SwitchReadOnly()
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
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")
	err := e.Encode(mycluster.GetCrashes())
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxOneTest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	regtest := new(regtest.RegTest)
	res := regtest.RunAllTests(mycluster, vars["testName"])
	e := json.NewEncoder(w)
	e.SetIndent("", "\t")

	if len(res) > 0 {
		err := e.Encode(res[0])
		if err != nil {
			mycluster.LogPrintf("ERROR", "API Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
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
			return
		}

	}
	return
}

func handlerMuxTests(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
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
	return
}

func handlerMuxSettings(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
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
	s.MaxDelay = fmt.Sprintf("%v", mycluster.GetConf().SwitchMaxDelay)
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
}

func handlerMuxSettingsReload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	initConfig()
	mycluster.ReloadConfig(confs[vars["clusterName"]])

}

func handlerMuxServerAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.LogPrintf("INFO", "Rest API receive new server to be added %s", vars["host"]+":"+vars["port"])
	mycluster.AddSeededServer(vars["host"] + ":" + vars["port"])

}

func handlerMuxServerStop(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetServerFromName(vars["serverName"])
	mycluster.StopDatabaseService(node)
}

func handlerMuxServerStart(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetServerFromName(vars["serverName"])
	mycluster.StartDatabaseService(node)
}

func handlerMuxServerProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetServerFromName(vars["serverName"])
	mycluster.InitDatabaseService(node)
}

func handlerMuxServerUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetServerFromName(vars["serverName"])
	mycluster.UnprovisionDatabaseService(node)
}

func handlerMuxProxyProvision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetProxyFromName(vars["proxyName"])
	mycluster.InitProxyService(node)
}

func handlerMuxProxyUnprovision(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	node := mycluster.GetProxyFromName(vars["proxyName"])
	mycluster.UnprovisionProxyService(node)
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

func handlerMuxStatus(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if isStarted {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "starting"}`)
	}
}
