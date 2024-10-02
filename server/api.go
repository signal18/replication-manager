// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bytes"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/iancoleman/strcase"
	log "github.com/sirupsen/logrus"

	"github.com/codegangsta/negroni"
	jwt "github.com/golang-jwt/jwt"
	"github.com/golang-jwt/jwt/request"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/auth"
	"github.com/signal18/replication-manager/auth/user"
	"github.com/signal18/replication-manager/cert"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/regtest"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/githelper"
)

//RSA KEYS AND INITIALISATION

var signingKey, verificationKey []byte
var apiPass string
var apiUser string

type Route struct {
	Level      auth.PermissionType                          `json:"level"`
	Permission string                                       `json:"permission"`
	URL        string                                       `json:"url"`
	Handler    func(w http.ResponseWriter, r *http.Request) `json:"handler"`
}

func (repman *ReplicationManager) initKeys() {
	repman.Lock()
	defer repman.Unlock()
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
	repman.Auth.SecureKey.PrivateKey = privKeyPEMBuffer.Bytes()

	// fmt.Println(string(repman.Auth.SecureKey.PrivateKey))

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
	repman.Auth.SecureKey.PublicKey = pubKeyPEMBuffer.Bytes()

	// fmt.Println("Pub key: " + string(repman.Auth.SecureKey.PublicKey))
}

//STRUCT DEFINITIONS

type userCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ApiResponse struct {
	Data    string `json:"data"`
	Success bool   `json:"success"`
}

type token struct {
	Token string `json:"token"`
}

// Proxy function that forwards the request to the target URL
func (repman *ReplicationManager) proxyToURL(target string) http.Handler {
	url, err := url.Parse(target)
	if err != nil {
		log.Fatalf("Failed to parse target URL: %v", err)
	}

	proxy := httputil.NewSingleHostReverseProxy(url)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Modify the request as needed before forwarding
		r.Host = url.Host
		proxy.ServeHTTP(w, r)
	})
}

func (repman *ReplicationManager) SharedirHandler(folder string) http.Handler {
	sub, err := fs.Sub(share.EmbededDbModuleFS, folder)
	if err != nil {
		log.Printf("folder read error [%s]: %s", folder, err)
	}

	return http.FileServer(http.FS(sub))
}

func (repman *ReplicationManager) DashboardFSHandler() http.Handler {

	sub, err := fs.Sub(share.EmbededDbModuleFS, "dashboard")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

func (repman *ReplicationManager) DashboardFSHandlerApp() http.Handler {
	sub, err := fs.Sub(share.EmbededDbModuleFS, "dashboard/index.html")
	if !repman.Conf.HttpUseReact {
		sub, err = fs.Sub(share.EmbededDbModuleFS, "dashboard/app.html")
	}
	if err != nil {
		panic(err)
	}

	return http.FileServer(http.FS(sub))
}

func (repman *ReplicationManager) rootHandler(w http.ResponseWriter, r *http.Request) {
	html, err := share.EmbededDbModuleFS.ReadFile("dashboard/index.html")
	if !repman.Conf.HttpUseReact {
		html, err = share.EmbededDbModuleFS.ReadFile("dashboard/app.html")
	}
	if err != nil {
		log.Printf("rootHandler read error : %s", err)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(html)
}

func (repman *ReplicationManager) RouteParser(router *mux.Router, routes []Route) {
	for _, route := range routes {
		router.Handle(route.URL, negroni.New(
			negroni.HandlerFunc(auth.CheckPermission(route.Permission, route.Level, repman.Auth, repman.Conf.OAuthProvider)),
			negroni.Wrap(http.HandlerFunc(route.Handler)),
		))
	}
}

func (repman *ReplicationManager) GetServerPublicRoutes() []Route {
	return []Route{
		{auth.PublicPermission, config.GrantNone, "/api/login", repman.loginHandler},
		{auth.PublicPermission, config.GrantNone, "/api/login-git", repman.loginHandler},
		{auth.PublicPermission, config.GrantNone, "/api/auth/callback", repman.handlerMuxAuthCallback},
		{auth.PublicPermission, config.GrantNone, "/api/prometheus", repman.handlerMuxPrometheus},
		{auth.PublicPermission, config.GrantNone, "/api/status", repman.handlerMuxStatus},
		{auth.PublicPermission, config.GrantNone, "/api/timeout", repman.handlerMuxTimeout},
		{auth.PublicPermission, config.GrantNone, "/api/configs/grafana", repman.handlerMuxGrafana},
		{auth.PublicPermission, config.GrantNone, "/api/heartbeat", repman.handlerMuxMonitorHeartbeat},
		{auth.PublicPermission, config.GrantNone, "/api/repocomp/current", repman.handlerRepoComp},
	}
}

func (repman *ReplicationManager) GetServerProtectedRoutes() []Route {
	return []Route{
		// Empty grant allow url hit but clusters will be shown based on cluster ACL
		{auth.AuthPermission, config.GrantNone, "/api/clusters", repman.handlerMuxClusters},
		{auth.AuthPermission, config.GrantNone, "/api/clusters/peers", repman.handlerMuxPeerClusters},

		// Check current user
		{auth.AuthPermission, config.GrantNone, "/api/auth/user", repman.handlerMuxGetCurrentUser},

		// For adding user with server-wide scope
		{auth.ServerPermission, config.GrantServerUsers, "/api/monitor/actions/user-add", repman.handlerMuxAddUser},
		{auth.ServerPermission, config.GrantServerUsers, "/api/monitor/actions/user-drop", repman.handlerMuxDropUser},

		// For adding user with server-wide scope
		{auth.ServerPermission, config.GrantServerUsers, "/api/monitor/actions/user-acl-allow", repman.handlerMuxAddACL},
		{auth.ServerPermission, config.GrantServerUsers, "/api/monitor/actions/user-acl-revoke", repman.handlerMuxRevokeACL},
	}
}

func (repman *ReplicationManager) GetAPIProtectedRoutes() []Route {
	return []Route{
		// Empty grant allow url hit but clusters will be shown based on cluster ACL
		{auth.PublicPermission, config.GrantNone, "/api/monitor", repman.handlerMuxReplicationManager},
	}
}

func (repman *ReplicationManager) apiserver() {
	var err error
	//PUBLIC ENDPOINTS
	router := mux.NewRouter()
	//router.HandleFunc("/", repman.handlerApp)
	// page to view which does not need authorization
	graphiteHost := repman.Conf.GraphiteCarbonHost
	if repman.Conf.GraphiteEmbedded {
		graphiteHost = "127.0.0.1"
	}

	graphiteURL, err := url.Parse(fmt.Sprintf("http://%s:%d", graphiteHost, repman.Conf.GraphiteCarbonApiPort))
	if err == nil {
		// Set up the reverse proxy target for Graphite API
		graphiteProxy := httputil.NewSingleHostReverseProxy(graphiteURL)
		// Set up a route that forwards the request to the Graphite API
		router.PathPrefix("/graphite/").Handler(http.StripPrefix("/graphite/", graphiteProxy))
	}

	router.PathPrefix("/meet/").Handler(http.StripPrefix("/meet/", repman.proxyToURL("https://meet.signal18.io/api/v4")))

	if repman.Conf.Test {
		router.HandleFunc("/", repman.handlerApp)
		router.PathPrefix("/images/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/assets/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))

		router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", http.FileServer(http.Dir(repman.Conf.ShareDir+"/grafana"))))
	} else {
		router.HandleFunc("/", repman.rootHandler)
		router.PathPrefix("/static/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/app/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/images/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/assets/").Handler(repman.DashboardFSHandler())
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", repman.SharedirHandler("grafana")))
	}

	repman.RouteParser(router, repman.GetServerPublicRoutes())
	repman.RouteParser(router, repman.GetServerProtectedRoutes())
	repman.RouteParser(router, repman.GetAPIProtectedRoutes())

	// repman.apiDatabaseUnprotectedHandler(router)
	// repman.apiDatabaseProtectedHandler(router)
	// repman.apiClusterUnprotectedHandler(router)
	// repman.apiClusterProtectedHandler(router)
	// repman.apiProxyProtectedHandler(router)
	repman.RouteParser(router, repman.GetDatabasePublicRoutes())
	repman.RouteParser(router, repman.GetDatabaseProtectedRoutes())
	repman.RouteParser(router, repman.GetClusterPublicRoutes())
	repman.RouteParser(router, repman.GetClusterProtectedRoutes())
	repman.RouteParser(router, repman.GetProxyProtectedRoutes())

	tlsConfig := Repmanv3TLS{
		Enabled: false,
	}
	// Add default unsecure cert if not set
	if repman.Conf.MonitoringSSLCert == "" {
		host := repman.Conf.APIBind
		if host == "0.0.0.0" {
			host = "localhost," + host + ",127.0.0.1"
		}
		cert.Host = host
		cert.Organization = "Signal18 Replication-Manager"
		tmpKey, tmpCert, err := cert.GenerateTempKeyAndCert()
		if err != nil {
			log.Errorf("Cannot generate temporary Certificate and/or Key: %s", err)
		}
		log.Info("No TLS certificate provided using generated key (", tmpKey, ") and certificate (", tmpCert, ")")
		defer os.Remove(tmpKey)
		defer os.Remove(tmpCert)

		tlsConfig = Repmanv3TLS{
			Enabled:            true,
			CertificatePath:    tmpCert,
			CertificateKeyPath: tmpKey,
			SelfSigned:         true,
		}
	}

	if repman.Conf.MonitoringSSLCert != "" {
		log.Info("Starting HTTPS & JWT API on " + repman.Conf.APIBind + ":" + repman.Conf.APIPort)
		tlsConfig = Repmanv3TLS{
			Enabled:            true,
			CertificatePath:    repman.Conf.MonitoringSSLCert,
			CertificateKeyPath: repman.Conf.MonitoringSSLKey,
		}
	} else {
		log.Info("Starting HTTP & JWT API on " + repman.Conf.APIBind + ":" + repman.Conf.APIPort)
	}

	repman.SetV3Config(Repmanv3Config{
		Listen: Repmanv3ListenAddress{
			Address: repman.Conf.APIBind,
			Port:    repman.Conf.APIPort,
		},
		TLS: tlsConfig,
	})

	// pass the router to the V3 server that will multiplex the legacy API and the
	// new gRPC + JSON Gateway API.
	err = repman.StartServerV3(true, router)

	if err != nil {
		log.Errorf("JWT API can't start: %s", err)
	}

}

//////////////////////////////////////////
/////////////ENDPOINT HANDLERS////////////
/////////////////////////////////////////

func (repman *ReplicationManager) isValidRequest(r *http.Request) (bool, error) {

	_, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err == nil {
		return true, nil
	}
	return false, err
}

func (repman *ReplicationManager) IsValidClusterACL(r *http.Request, cluster *cluster.Cluster) (bool, string) {

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err == nil {
		claims := token.Claims.(jwt.MapClaims)
		userinfo := claims["CustomUserInfo"]
		mycutinfo := userinfo.(map[string]interface{})
		meuser := mycutinfo["Name"].(string)
		mepwd := mycutinfo["Password"].(string)
		_, ok := mycutinfo["profile"]

		if ok {
			if strings.Contains(mycutinfo["profile"].(string), repman.Conf.OAuthProvider) /*&& strings.Contains(mycutinfo["email_verified"]*/ {
				meuser = mycutinfo["email"].(string)
				return cluster.IsValidACL(meuser, mepwd, r.URL.Path, "oidc"), meuser
			}
		}
		return cluster.IsValidACL(meuser, mepwd, r.URL.Path, "password"), meuser
	}
	return false, ""
}

func (repman *ReplicationManager) loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var cred user.UserCredentials
	var u *user.User
	var ok bool

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&cred)
	if err != nil {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}

	auth_try, err := repman.Auth.LogAttempt(cred)
	if err != nil {
		http.Error(w, "Too many request: "+err.Error(), http.StatusTooManyRequests)
		return
	}

	if strings.Contains(r.URL.Path, "login-git") {

		u, ok = repman.Auth.LoadUser(cred.Username)
		if !ok {
			res, _ := json.Marshal(repman.Auth.GetUsers())

			http.Error(w, "Error logging in: "+string(res), http.StatusUnauthorized)
			return
		}

		token, _, err := githelper.GetGitLabTokenBasicAuth(cred.Username, cred.Password)

		// if repman.Conf.IsEligibleForPrinting(config.ConstLogModGit, config.LvlDbg) {
		// log.Infof("Git Body Response: %s", string(resp))
		// }

		if err != nil {
			http.Error(w, "Error logging in to gitlab: "+err.Error(), http.StatusUnauthorized)
			return
		}

		if token == "" {
			http.Error(w, "Error logging in to gitlab: Token is empty", http.StatusUnauthorized)
			return
		}

		u.Password = cred.Password
		u.GitUser = cred.Username
		u.GitToken = token

	} else {
		u, ok = repman.Auth.LoadUser(cred.Username)
		if !ok || u.Password != cred.Password {
			http.Error(w, "Error logging in: "+err.Error(), http.StatusUnauthorized)
			return
		}
	}

	auth_try.User = cred.Username
	auth_try.Try = 1
	auth_try.Time = time.Now()

	tokenString, err := auth.IssueJWT(u, repman.Conf.TokenTimeout, repman.Auth.SecureKey.PrivateKey)
	if err != nil {
		http.Error(w, "Error while signing the token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	//create a token instance using the token string
	specs := r.Header.Get("Accept")
	resp := token{tokenString}
	if strings.Contains(specs, "text/html") {
		w.Write([]byte(tokenString))
		return
	} else {
		repman.jsonResponse(resp, w)
		return
	}
}

func (repman *ReplicationManager) handlerMuxAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	config := repman.Conf
	OAuthSecret := repman.Conf.GetDecryptedPassword("api-oauth-client-secret", repman.Conf.OAuthClientSecret)
	RedirectURL := repman.Conf.APIPublicURL + "/api/auth/callback"

	oauth2Token, userInfo, err := auth.OAuthGetTokenAndUser(config.OAuthProvider, config.OAuthClientID, OAuthSecret, RedirectURL, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	repman.OAuthAccessToken = oauth2Token

	u, ok := repman.Auth.LoadUser(userInfo.Email)
	if !ok {
		http.Error(w, "User not found in user list", http.StatusInternalServerError)
		return
	}

	tmp := strings.Split(userInfo.Profile, "/")
	u.GitUser = tmp[len(tmp)-1]
	u.GitToken = oauth2Token.AccessToken

	tokenString, err := auth.IssueJWT(u, repman.Conf.TokenTimeout, repman.Auth.SecureKey.PrivateKey)
	if err != nil {
		http.Error(w, "Error while signing the token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	specs := r.Header.Get("Accept")
	if strings.Contains(specs, "text/html") {
		http.Redirect(w, r, repman.Conf.APIPublicURL+"/#!/dashboard?token="+tokenString, http.StatusTemporaryRedirect)
		return
	} else {
		//create a token instance using the token string
		resp := token{tokenString}
		repman.jsonResponse(resp, w)
		return
	}
}

//AUTH TOKEN VALIDATION

func (repman *ReplicationManager) handlerMuxReplicationManager(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	u, err := auth.GetUserFromRequest(r)
	if err != nil {
		http.Error(w, "Error Getting User Context: "+err.Error(), 500)
		return
	}

	mycopy := *repman
	var cl []string

	for _, cluster := range repman.Clusters {
		if u.HasClusterPermission(cluster.Name, "any") {
			cl = append(cl, cluster.Name)
		}
	}

	mycopy.ClusterList = cl

	res, err := json.Marshal(mycopy)
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	for crkey, _ := range mycopy.Conf.Secrets {
		res, err = jsonparser.Set(res, []byte(`"*:*" `), "config", strcase.ToLowerCamel(crkey))
		if err != nil {
			http.Error(w, "Parse error", 500)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

func (repman *ReplicationManager) handlerMuxAddUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var cred user.UserForm

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&cred)
	if err != nil {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}

	// This is for cluster wide scope
	vars := mux.Vars(r)
	if _, ok := vars["clusterName"]; ok {
		cred.Clusters = vars["clusterName"]
	}

	u, err := repman.AddUser(cred)
	if err != nil {
		http.Error(w, "Error creating user: "+err.Error(), http.StatusBadRequest)
		return
	}

	resp := UserResponse{
		Message: "User created",
		User:    u,
	}

	res, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error Marshal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

func (repman *ReplicationManager) handlerMuxDropUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	// Server scope
	_, ok := repman.Auth.LoadUser(vars["username"])
	if !ok {
		http.Error(w, "Error in request: username is not exists", http.StatusBadRequest)
		return
	}

	repman.Auth.DeleteUser(vars["username"])
	repman.Conf.DropUser(vars["username"])

	w.WriteHeader(http.StatusNoContent)
	w.Write([]byte("User has been deleted"))
}

func (repman *ReplicationManager) handlerMuxSetRole(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	u, ok := repman.Auth.LoadUser(vars["username"])
	if !ok {
		http.Error(w, "Error in request: username is not exists", http.StatusBadRequest)
		return
	}

	u.WG.Add(1)
	defer u.WG.Done()

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Error set cluster role: clustername is not registered", http.StatusBadRequest)
		return
	}

	u.SetClusterRole(vars["clusterName"], vars["role"])

	resp := UserResponse{
		Message: "Role updated",
		User:    u,
	}

	res, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error Marshal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)

}

func (repman *ReplicationManager) handlerMuxAddACL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var cred user.UserForm

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&cred)
	if err != nil {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}

	u, ok := repman.Auth.LoadUser(cred.Username)
	if !ok {
		http.Error(w, "Error in add ACL: User is not exists in this server", http.StatusInternalServerError)
		return
	}

	u.WG.Add(1)
	defer u.WG.Done()

	err = repman.AddUserACL(cred, u)
	if err != nil {
		http.Error(w, "Error in add ACL: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := UserResponse{
		Message: "ACL updated",
		User:    u,
	}

	res, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error Marshal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

func (repman *ReplicationManager) handlerMuxRevokeACL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var cred user.UserForm

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&cred)
	if err != nil {
		http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
		return
	}

	u, ok := repman.Auth.LoadUser(cred.Username)
	if !ok {
		http.Error(w, "Error in add ACL: User is not exists in this server", http.StatusInternalServerError)
		return
	}

	u.WG.Add(1)
	defer u.WG.Done()

	err = repman.RevokeUserACL(cred, u)
	if err != nil {
		http.Error(w, "Error in add ACL: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp := UserResponse{
		Message: "ACL revoked",
		User:    u,
	}

	res, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, "Error Marshal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

func (repman *ReplicationManager) handlerMuxDropClusterUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	// Server scope
	_, ok := repman.Auth.LoadUser(vars["username"])
	if !ok {
		http.Error(w, "Error in request: username is not exists", http.StatusBadRequest)
		return
	}

	// Equivalent to dropping ACL
	repman.Auth.DeleteClusterUser(vars["username"], vars["clusterName"])

	w.WriteHeader(http.StatusNoContent)
	w.Write([]byte("User has been deleted for cluster: " + vars["clusterName"]))
}

func (repman *ReplicationManager) handlerMuxGetCurrentUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	u, err := auth.GetUserFromRequest(r)
	if err != nil {
		http.Error(w, "Error getting user from token: "+err.Error(), http.StatusInternalServerError)
	}

	res, err := json.Marshal(u)
	if err != nil {
		http.Error(w, "Error Marshal", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

// swagger:route GET /api/clusters clusters
//
// This will show all the available clusters
//
//	Responses:
//	  200: clusters
func (repman *ReplicationManager) handlerMuxClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	u, err := auth.GetUserFromRequest(r)
	if err != nil {
		http.Error(w, "Unauthenticated resource: "+err.Error(), 401)
		return
	}

	var clusters []*cluster.Cluster

	for _, cluster := range repman.Clusters {
		// Has any cluster permission
		if u.HasClusterPermission(cluster.Name, "any") {
			clusters = append(clusters, cluster)
		}
	}

	sort.Sort(cluster.ClusterSorter(clusters))

	cl, err := json.MarshalIndent(clusters, "", "\t")
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	for i, cluster := range clusters {
		for crkey, _ := range cluster.Conf.Secrets {
			cl, err = jsonparser.Set(cl, []byte(`"*:*" `), fmt.Sprintf("[%d]", i), "config", strcase.ToLowerCamel(crkey))
			if err != nil {
				http.Error(w, "Encoding error", 500)
				return
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(cl)

}

func (repman *ReplicationManager) handlerMuxPeerClusters(w http.ResponseWriter, r *http.Request) {

	peerclusters := repman.Conf.GetCloud18PeerClusters()
	cl, err := json.MarshalIndent(peerclusters, "", "\t")
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(cl)

}

func (repman *ReplicationManager) validateTokenMiddleware(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
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
		fmt.Fprint(w, "Unauthorised access to this resource: "+err.Error())
	}
}

//HELPER FUNCTIONS

func (repman *ReplicationManager) jsonResponse(apiresponse interface{}, w http.ResponseWriter) {
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

func (repman *ReplicationManager) handlerMuxClusterAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	repman.AddCluster(vars["clusterName"], "")

}

func (repman *ReplicationManager) handlerMuxClusterDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	repman.DeleteCluster(vars["clusterName"])

}

// swagger:operation GET /api/prometheus prometheus
// Returns the Prometheus metrics for all database instances on the server
// in the Prometheus text format
//
// ---
// produces:
//   - text/plain; version=0.0.4
//
// responses:
//
//	'200':
//	  description: Prometheus file format
//	  schema:
//	    type: string
//	  headers:
//	    Access-Control-Allow-Origin:
//	      type: string
func (repman *ReplicationManager) handlerMuxPrometheus(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Access-Control-Allow-Origin", "*")
	for _, cluster := range repman.Clusters {
		for _, server := range cluster.Servers {
			res := server.GetPrometheusMetrics()
			w.Write([]byte(res))
		}
	}
}

func (repman *ReplicationManager) handlerMuxClustersOld(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	s := new(Settings)
	s.Clusters = repman.ClusterList
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

// The Status contains string value for the alive status.
// Possible values are: running, starting, errors
//
// swagger:response status
type StatusResponse struct {
	// Example: *
	AccessControlAllowOrigin string `json:"Access-Control-Allow-Origin"`
	// The status message
	// in: body
	Body struct {
		// Example: running
		// Example: starting
		// Example: errors
		Alive string `json:"alive"`
	}
}

// swagger:route GET /api/status status
//
// This will show the status of the cluster
//
//     Responses:
//       200: status

func (repman *ReplicationManager) handlerMuxStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	if repman.isStarted {
		io.WriteString(w, `{"alive": "running"}`)
	} else {
		io.WriteString(w, `{"alive": "starting"}`)
	}
}

// swagger:route GET /api/timeout timeout
//
//     Responses:
//       200: status

func (repman *ReplicationManager) handlerMuxTimeout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	time.Sleep(1200 * time.Second)
	io.WriteString(w, `{"alive": "running"}`)
}

// swagger:route GET /api/heartbeat heartbeat
//
//     Responses:
//       200: heartbeat

func (repman *ReplicationManager) handlerMuxMonitorHeartbeat(w http.ResponseWriter, r *http.Request) {
	var send Heartbeat
	send.UUID = repman.UUID
	send.UID = repman.Conf.ArbitrationSasUniqueId
	send.Secret = repman.Conf.ArbitrationSasSecret
	send.Status = repman.Status
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(send); err != nil {
		panic(err)
	}
}

func (repman *ReplicationManager) handlerStatic(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		w.Header().Set("Cache-Control", fmt.Sprintf("max-age=%d", repman.Conf.CacheStaticMaxAge))
		w.Header().Set("Etag", repman.Version)

		h.ServeHTTP(w, r)
	})
}

func (repman *ReplicationManager) handlerMuxGrafana(w http.ResponseWriter, r *http.Request) {
	var entries []fs.DirEntry
	var list []string = make([]string, 0)
	var err error
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if repman.Conf.Test {
		entries, err = os.ReadDir(conf.ShareDir + "/grafana")
	} else {
		entries, err = share.EmbededDbModuleFS.ReadDir("grafana")
	}
	if err != nil {
		http.Error(w, "Encoding reading directory", 500)
		return
	}
	for _, b := range entries {
		if !b.IsDir() {
			list = append(list, b.Name())
		}
	}

	err = json.NewEncoder(w).Encode(list)
	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}
}
