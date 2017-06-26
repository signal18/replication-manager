package main

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/codegangsta/negroni"
	jwt "github.com/dgrijalva/jwt-go"
	"github.com/dgrijalva/jwt-go/request"
	"github.com/gorilla/mux"
	"github.com/tanji/replication-manager/cluster"
	"github.com/tanji/replication-manager/regtest"
)

//RSA KEYS AND INITIALISATION

var signingKey, verificationKey []byte

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

	fmt.Println(string(signingKey))

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

	fmt.Println(string(verificationKey))
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

	//PROTECTED ENDPOINTS
	router.Handle("/api/clusters", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxClusters)),
	))

	router.Handle("/api/clusters/{clusterName}/settings", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSettings)),
	))

	router.Handle("/api/clusters/{clusterName}/switchover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxSwitchover)),
	))
	router.Handle("/api/clusters/{clusterName}/failover", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxFailover)),
	))
	router.Handle("/api/clusters/{clusterName}/servers", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxServers)),
	))
	router.Handle("/api/clusters/{clusterName}/master", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxMaster)),
	))

	router.Handle("/api/clusters/{clusterName}/interactive", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxInteractive)),
	))
	router.Handle("/api/logs", negroni.New(
		negroni.HandlerFunc(validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(handlerMuxLog)),
	))

	log.Println("Now listening localhost:3000")
	http.ListenAndServeTLS("0.0.0.0:3000", conf.ShareDir+"/server.crt", conf.ShareDir+"/server.key", router)
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

	fmt.Println(user.Username, user.Password)

	//validate user credentials
	if user.Username != "admin" || user.Password != "mariadb" {
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
	claims["exp"] = time.Now().Add(time.Minute * 20).Unix()
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
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}

	for i := range srvs {
		srvs[i].Pass = "XXXXXXXX"
	}
	e := json.NewEncoder(w)

	err = e.Encode(srvs)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
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

func handlerMuxSwitchover(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if mycluster.IsMasterFailed() {
		mycluster.LogPrintf("ERROR", " Master failed, cannot initiate switchover")
		http.Error(w, "Master failed", http.StatusBadRequest)
		return
	}
	mycluster.LogPrintf("INFO", "Rest API receive Switchover request")
	mycluster.SwitchOver()
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
			log.Println("Error encoding JSON: ", err)
			http.Error(w, "Encoding error", 500)
			return
		}
		srvs.Pass = "XXXXXXXX"
	}
	e := json.NewEncoder(w)
	err := e.Encode(srvs)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxInteractive(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := getClusterByName(vars["clusterName"])
	mycluster.ToggleInteractive()
	return
}

func handlerMuxLog(w http.ResponseWriter, r *http.Request) {
	e := json.NewEncoder(w)
	err := e.Encode(tlog.Buffer)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
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
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}

func handlerMuxClusters(w http.ResponseWriter, r *http.Request) {

	s := new(Settings)
	s.Clusters = cfgGroupList
	regtest := new(regtest.RegTest)
	s.RegTests = regtest.GetTests()
	e := json.NewEncoder(w)
	err := e.Encode(s)
	if err != nil {
		log.Println("Error encoding JSON: ", err)
		http.Error(w, "Encoding error", 500)
		return
	}
}
