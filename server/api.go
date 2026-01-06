// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as an additional term ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integral text.

package server

import (
	"bytes"
	"context"
	"crypto/md5"
	cryptorand "crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/iancoleman/strcase"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/sjson"
	"golang.org/x/oauth2"

	"github.com/codegangsta/negroni"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/signal18/replication-manager/cert"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	_ "github.com/signal18/replication-manager/docs"
	"github.com/signal18/replication-manager/peer"
	"github.com/signal18/replication-manager/regtest"
	"github.com/signal18/replication-manager/share"
	"github.com/signal18/replication-manager/utils/alert/mailer"
	"github.com/signal18/replication-manager/utils/githelper"
	"github.com/signal18/replication-manager/utils/meethelper"
	"github.com/signal18/replication-manager/utils/tty"
	httpSwagger "github.com/swaggo/http-swagger"
)

//RSA KEYS AND INITIALISATION

var signingKey, verificationKey []byte
var apiPass string
var apiUser string

type AuthToken struct {
	Token string `json:"token"`
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

	fileServer := http.FileServerFS(sub)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the requested file is a .js file
		if strings.HasSuffix(r.URL.Path, ".js") {
			// Check if the corresponding .gz file exists
			gzPath := r.URL.Path + ".gz"
			fs.WalkDir(sub, ".", func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					log.Println("WalkDir error:", err)
					return err
				}

				if "/"+path == gzPath {
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Type", "application/javascript")
					r.URL.Path = path
					return fs.SkipAll
				}
				return nil
			})

		}
		fileServer.ServeHTTP(w, r)
	})

}

var filewalked bool

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

func (repman *ReplicationManager) apiserver() {
	var err error
	//PUBLIC ENDPOINTS
	router := mux.NewRouter()

	router.Use(repman.RecoveryMiddleware)
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

	// Define the dynamic proxy route with Base64-encoded peer URL and arbitrary route
	router.HandleFunc("/peer/{encodedpeer}/{route:.*}", repman.DynamicPeerHandler)

	if repman.Conf.Test {
		router.HandleFunc("/", repman.handlerApp)
		router.PathPrefix("/terminal/").HandlerFunc(repman.handlerApp)
		router.PathPrefix("/images/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/assets/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))

		router.PathPrefix("/static/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/app/").Handler(http.FileServer(http.Dir(repman.Conf.HttpRoot)))
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", http.FileServer(http.Dir(repman.Conf.ShareDir+"/grafana"))))
	} else {
		router.HandleFunc("/", repman.rootHandler)
		router.PathPrefix("/terminal/").HandlerFunc(repman.rootHandler)
		router.PathPrefix("/static/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/app/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/images/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/assets/").Handler(repman.handlerStatic(repman.DashboardFSHandler()))
		router.PathPrefix("/grafana/").Handler(http.StripPrefix("/grafana/", repman.SharedirHandler("grafana")))
	}

	router.PathPrefix("/api-docs/").Handler(negroni.New(
		negroni.HandlerFunc(repman.validateSwaggerMiddleware),
		negroni.Wrap(http.HandlerFunc(httpSwagger.Handler(httpSwagger.URL("/api-docs/doc.json")))),
	))

	router.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check if the path starts with "/api"
		if len(r.URL.Path) >= 4 && r.URL.Path[:4] == "/api" {
			// Return 404 for /api paths
			http.NotFound(w, r)
		} else {
			// Redirect non /api paths to "/"
			http.Redirect(w, r, "/", http.StatusFound)
		}
	})

	router.Handle("/api/terminal/list", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerGetTerminalSessionList)),
	))

	router.Handle("/api/terminal/connect", http.HandlerFunc(repman.handlerTerminal))

	router.HandleFunc("/api/login", repman.loginHandler)
	router.HandleFunc("/api/version", repman.handlerVersion)

	router.Handle("/api/terms", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxTerms)),
	))

	router.Handle("/api/auth/callback", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAuthCallback)),
	))

	router.Handle("/api/whoami", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxWhoAmI)),
	))

	router.Handle("/api/clusters", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusters)),
	))
	router.Handle("/api/clusters/for-sale", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxPeerClustersForSale)),
	))
	router.Handle("/api/clusters/peers", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxPeerClusters)),
	))
	router.Handle("/api/peers", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxPeerNodes)),
	))
	router.Handle("/api/prometheus", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxPrometheus)),
	))
	router.Handle("/api/status", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxStatus)),
	))

	router.Handle("/api/health", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxHealth)),
	))

	router.Handle("/api/timeout", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxTimeout)),
	))
	router.Handle("/api/repocomp/current", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerRepoComp)),
	))
	router.Handle("/api/configs/grafana", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxGrafana)),
	))
	//UNPROTECTED ENDPOINTS FOR SETTINGS
	router.Handle("/api/monitor", negroni.New(
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReplicationManager)),
	))
	//PROTECTED ENDPOINTS FOR SETTINGS
	router.Handle("/api/monitor", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxReplicationManager)),
	))

	router.Handle("/api/email/send", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxSendEmail)),
	))
	repman.apiMeetProtectedHandler(router)
	repman.apiDatabaseUnprotectedHandler(router)
	repman.apiDatabaseProtectedHandler(router)
	repman.apiClusterUnprotectedHandler(router)
	repman.apiClusterProtectedHandler(router)
	repman.apiProxyProtectedHandler(router)
	repman.apiAppProtectedHandler(router)

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
	repman.IsApiListenerReady = true
}

//////////////////////////////////////////
/////////////ENDPOINT HANDLERS////////////
/////////////////////////////////////////

func (repman *ReplicationManager) handleOriginValidator(origin string) bool {
	return repman.PeerManager.HasPeerURL(origin)
}

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

func (repman *ReplicationManager) DecryptJWTPassword(r *http.Request) (string, error) {

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err == nil {
		claims := token.Claims.(jwt.MapClaims)
		userinfo := claims["CustomUserInfo"]
		mycutinfo := userinfo.(map[string]interface{})
		mepwd := mycutinfo["Password"].(string)
		_, ok := mycutinfo["profile"]

		if ok && strings.Contains(mycutinfo["profile"].(string), repman.Conf.OAuthProvider) /*&& strings.Contains(mycutinfo["email_verified"]*/ {
			return repman.Conf.GetDecryptedPassword("api-credentials", mepwd), nil
		}
		return "", fmt.Errorf("No Gitlab Profile in JWT")
	}
	return "", err
}

func (repman *ReplicationManager) GetUserInfoMap(token *jwt.Token) (map[string]string, error) {
	claims := token.Claims.(jwt.MapClaims)
	userinfo := claims["CustomUserInfo"]
	mycutinfo := userinfo.(map[string]interface{})
	UserInfoMap := make(map[string]string)
	UserInfoMap["Name"] = mycutinfo["Name"].(string)
	UserInfoMap["Password"] = mycutinfo["Password"].(string)
	UserInfoMap["Role"] = mycutinfo["Role"].(string)
	UserInfoMap["Email"] = ""
	_, ok := mycutinfo["profile"]
	if ok {
		profile := mycutinfo["profile"].(string)
		_, ok = mycutinfo["meet_user_id"]
		if ok {
			UserInfoMap["MeetUserID"] = mycutinfo["meet_user_id"].(string)
		}
		if strings.Contains(profile, repman.Conf.OAuthProvider) {
			UserInfoMap["User"] = mycutinfo["email"].(string)
			UserInfoMap["Email"] = mycutinfo["email"].(string)
			UserInfoMap["profile"] = profile
			return UserInfoMap, nil
		}
		return nil, fmt.Errorf("invalid oauth provider")
	}
	_, ok = mycutinfo["meet_user_id"]
	if ok {
		UserInfoMap["MeetUserID"] = mycutinfo["meet_user_id"].(string)
	}
	UserInfoMap["User"] = mycutinfo["Name"].(string)
	return UserInfoMap, nil
}

func (repman *ReplicationManager) GetJWTClaims(r *http.Request) (map[string]string, error) {

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})
	if err == nil {
		return repman.GetUserInfoMap(token)
	}
	return nil, err
}

func (repman *ReplicationManager) GetUserFromRequest(r *http.Request) string {

	token, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor, func(token *jwt.Token) (interface{}, error) {
		vk, _ := jwt.ParseRSAPublicKeyFromPEM(verificationKey)
		return vk, nil
	})

	if err == nil {
		claims := token.Claims.(jwt.MapClaims)
		userinfo := claims["CustomUserInfo"]
		mycutinfo := userinfo.(map[string]interface{})
		meuser := mycutinfo["Name"].(string)
		_, ok := mycutinfo["profile"]

		if ok {
			if strings.Contains(mycutinfo["profile"].(string), repman.Conf.OAuthProvider) /*&& strings.Contains(mycutinfo["email_verified"]*/ {
				return mycutinfo["email"].(string)
			}
		}
		return meuser
	}

	return ""
}

// loginHandler handles user login requests.
// @Summary User login
// @Description Authenticates a user and returns a JWT token upon successful login.
// @Tags Auth
// @Accept  json
// @Produce  json
// @Param userCredentials body userCredentials true "User credentials"
// @Success 200 {object} token "JWT token"
// @Failure 403 {string} string "Error in request"
// @Failure 429 {string} string "Too many requests"
// @Failure 401 {string} string "Invalid credentials"
// @Failure 500 {string} string "Error signing token"
// @Router /api/login [post]
func (repman *ReplicationManager) loginHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	var user userCredentials

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&user)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error in request")
		return
	}

	if v, ok := repman.UserAuthTry.Load(user.Username); ok {
		auth_try := v.(authTry)
		if auth_try.Try == 3 {
			if time.Now().Before(auth_try.Time.Add(3 * time.Minute)) {
				response := "3 authentication errors for the user " + user.Username + ", please try again in 3 minutes"
				http.Error(w, response, http.StatusTooManyRequests)
				return
			} else {
				auth_try.Try = 1
				auth_try.Time = time.Now()
				repman.UserAuthTry.Store(user.Username, auth_try)
			}
		} else {
			auth_try.Try += 1
			repman.UserAuthTry.Store(user.Username, auth_try)
		}
	} else {
		var auth_try authTry = authTry{
			User: user.Username,
			Try:  1,
			Time: time.Now(),
		}
		repman.UserAuthTry.Store(user.Username, auth_try)
	}

	var tok string
	var userInfo interface{}
	var meetUserID string

	if repman.Conf.Cloud18 {
		var meetUser, meetPassword string
		tok, _ = githelper.GetGitLabTokenBasicAuth(user.Username, user.Password, false)
		// if login successfull, get the email from gitlab
		if tok != "" {
			//	swapp the current user with email if needed
			email, err := githelper.GetGitLabUserEmail(tok, true)
			if email != "" {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "Switch from gitlab user to email  : %s", email)
				meetUser = email
			} else {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "Error retrieving email from gitlab: %e", err)
				meetUser = user.Username
			}

			meetPassword = user.Password

			//to get meet token and create a client while login
			meetUserID, err = meethelper.CreateMeetUserClient(meetUser, meetPassword, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
			if err != nil {
				if repman.Conf.LogSupport {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "Error retrieving meet token: %e", err)
				}
			} else {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "Meet token is retrieved")
			}

			userInfo = struct {
				Name       string
				Role       string
				Password   string
				Email      string `json:"email"`
				Profile    string `json:"profile"`
				MeetUserID string `json:"meet_user_id"`
			}{user.Username, "Member", repman.Conf.GetEncryptedString(user.Password), email, repman.Conf.OAuthProvider, meetUserID}

		} else {

			// not a gitlab user, login via local password and set global gitlab user as the meet user
			loggedIn := false
			for _, cl := range repman.Clusters {
				//validate user credentials
				if !cl.IsValidACL(user.Username, user.Password, r.URL.Path, "password") {
					continue
				}
				loggedIn = true
			}

			if !loggedIn {
				http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized)
				return
			}

			meetUser = repman.Conf.Cloud18GitUser
			meetPassword = repman.Conf.GetDecryptedPassword("cloud18-gitlab-password", repman.Conf.Cloud18GitPassword)

			//to get meet token and create a client while login
			meetUserID, err = meethelper.CreateMeetUserClient(meetUser, meetPassword, repman.Conf.IsEligibleForPrinting(config.ConstLogModSupport, "ERROR"))
			if err != nil {
				if repman.Conf.LogSupport {
					repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlErr, "Error retrieving meet token: %e", err)
				}
			} else {
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlInfo, "Meet token is retrieved")
			}

			userInfo = struct {
				Name       string
				Role       string
				Password   string
				MeetUserID string `json:"meet_user_id"`
			}{user.Username, "Member", repman.Conf.GetEncryptedString(user.Password), meetUserID}

		}

		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlDbg, "LoginHandler: meet userID %s", meetUserID)
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModSupport, config.LvlDbg, "LoginHandler: userInfo after meet userID %v", userInfo)

	} else { // CLoud18 unregister instance
		loggedIn := false
		for _, cl := range repman.Clusters {
			//validate user credentials
			if !cl.IsValidACL(user.Username, user.Password, r.URL.Path, "password") {
				continue
			}
			loggedIn = true
			userInfo = struct {
				Name     string
				Role     string
				Password string
			}{user.Username, "Member", repman.Conf.GetEncryptedString(user.Password)}
		}

		if !loggedIn {
			http.Error(w, "Error logging in: Invalid credentials", http.StatusUnauthorized)
			return
		}
	}

	// Reset the auth try counter
	var auth_try authTry = authTry{
		User: user.Username,
		Try:  0,
		Time: time.Now(),
	}
	repman.UserAuthTry.Store(user.Username, auth_try)

	signer := jwt.New(jwt.SigningMethodRS256)
	claims := signer.Claims.(jwt.MapClaims)
	//set claims
	claims["iss"] = "https://api.replication-manager.signal18.io"
	claims["iat"] = time.Now().Unix()
	claims["exp"] = time.Now().Add(time.Hour * time.Duration(repman.Conf.TokenTimeout)).Unix()
	claims["jti"] = "1"   // should be user ID(?)
	claims["token"] = tok // store gitlab token
	claims["CustomUserInfo"] = userInfo
	signer.Claims = claims
	sk, _ := jwt.ParseRSAPrivateKeyFromPEM(signingKey)

	tokenString, err := signer.SignedString(sk)
	if err != nil {
		fmt.Fprintln(w, "Error while signing the token")
		http.Error(w, "Error signing token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	//create a token instance using the token string
	specs := r.Header.Get("Accept")
	//resp := token{tokenString}

	resp := AuthToken{
		Token: tokenString,
	}

	if strings.Contains(specs, "text/html") {
		w.Write([]byte(tokenString))
		return
	}

	repman.jsonResponse(resp, w)
	return
}

func (repman *ReplicationManager) handlerMuxAuthCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	OAuthContext := context.Background()
	Provider, err := oidc.NewProvider(OAuthContext, repman.Conf.OAuthProvider)
	if err != nil {
		log.Printf("OAuth callback Failed to init oidc from gitlab:%s %v\n", repman.Conf.OAuthProvider, err)
		return
	}
	OAuthConfig := oauth2.Config{
		ClientID:     repman.Conf.OAuthClientID,
		ClientSecret: repman.Conf.GetDecryptedPassword("api-oauth-client-secret", repman.Conf.OAuthClientSecret),
		Endpoint:     Provider.Endpoint(),
		RedirectURL:  repman.Conf.APIPublicURL + "/api/auth/callback",
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "read_api", "api"},
	}
	log.Printf("OAuth oidc to gitlab: %v\n", OAuthConfig)
	oauth2Token, err := OAuthConfig.Exchange(OAuthContext, r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, "Failed to exchange token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	repman.OAuthAccessToken = oauth2Token

	userInfo, err := Provider.UserInfo(OAuthContext, oauth2.StaticTokenSource(oauth2Token))
	if err != nil {
		http.Error(w, "Failed to get userinfo: "+err.Error(), http.StatusInternalServerError)
		return
	}

	r.Header.Get("Accept")

	for _, cluster := range repman.Clusters {
		//validate user credentials
		if cluster.IsValidACL(userInfo.Email, cluster.APIUsers[userInfo.Email].Password, r.URL.Path, "oidc") {
			apiuser := cluster.APIUsers[userInfo.Email]
			apiuser.GitToken = oauth2Token.AccessToken
			tmp := strings.Split(userInfo.Profile, "/")
			apiuser.GitUser = tmp[len(tmp)-1]
			cluster.APIUsers[userInfo.Email] = apiuser

			if cluster.Conf.Cloud18 {
				tokenName := conf.Cloud18Domain + "-" + conf.Cloud18SubDomain + "-" + conf.Cloud18SubDomainZone
				new_token, user_id := githelper.GetGitLabTokenOAuth(oauth2Token.AccessToken, tokenName, cluster.Conf.LogGit)
				//vault_aut_url := vaulthelper.GetVaultOIDCAuth()
				//vaulthelper.GetVaultOIDCAuth()
				//http.Redirect(w, r, vault_aut_url, http.StatusSeeOther)
				if new_token != "" {
					//to create project for user if not exist
					path := cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
					name := cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone
					githelper.GitLabCreateProject(new_token, name, path, cluster.Conf.Cloud18Domain, user_id, cluster.Conf.LogGit)
					//to store new gitlab token
					cluster.Conf.GitUrl = repman.Conf.OAuthProvider + "/" + cluster.Conf.Cloud18Domain + "/" + cluster.Conf.Cloud18SubDomain + "-" + cluster.Conf.Cloud18SubDomainZone + ".git"
					cluster.Conf.GitUsername = tmp[len(tmp)-1]
					newSecret := cluster.Conf.Secrets["git-acces-token"]
					newSecret.OldValue = newSecret.Value
					newSecret.Value = new_token
					cluster.Conf.Secrets["git-acces-token"] = newSecret
					//cluster.Conf.GitAccesToken = tokenInfo.token
					cluster.LogModulePrintf(cluster.Conf.Verbose, config.ConstLogModGit, config.LvlDbg, "Clone from git : url %s, tok %s, dir %s\n", cluster.Conf.GitUrl, cluster.Conf.PrintSecret(cluster.Conf.Secrets["git-acces-token"].Value), cluster.Conf.WorkingDir)
					cluster.Conf.CloneConfigFromGit(cluster.Conf.GitUrl, cluster.Conf.GitUsername, cluster.Conf.Secrets["git-acces-token"].Value, cluster.Conf.WorkingDir)
				} else {
					log.Printf("Failed to get token from gitlab: %v\n", err)
				}

			}

			signer := jwt.New(jwt.SigningMethodRS256)
			claims := signer.Claims.(jwt.MapClaims)
			//set claims
			claims["iss"] = "https://api.replication-manager.signal18.io"
			claims["iat"] = time.Now().Unix()
			claims["exp"] = time.Now().Add(time.Hour * time.Duration(repman.Conf.TokenTimeout)).Unix()
			claims["jti"] = "1" // should be user ID(?)
			claims["CustomUserInfo"] = struct {
				Name     string
				Role     string
				Password string
			}{userInfo.Email, "Member", repman.Conf.GetEncryptedString(cluster.APIUsers[userInfo.Email].Password)}
			password := cluster.APIUsers[userInfo.Email].Password
			signer.Claims = claims
			sk, _ := jwt.ParseRSAPrivateKeyFromPEM(signingKey)

			tokenString, err := signer.SignedString(sk)

			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintln(w, "Error while signing the token")
				log.Printf("Error signing token: %v\n", err)
			}
			//create a token instance using the token string
			specs := r.Header.Get("Accept")
			resp := token{tokenString}
			if strings.Contains(specs, "text/html") {
				http.Redirect(w, r, repman.Conf.APIPublicURL+"/#!/dashboard?token="+tokenString+"&user="+userInfo.Email+"&pass="+password, http.StatusTemporaryRedirect)
				return
			}
			repman.jsonResponse(resp, w)
			return
		}

	}

	w.WriteHeader(http.StatusForbidden)
	fmt.Println("Error logging in")
	fmt.Fprint(w, "Invalid credentials")
	return
}

//AUTH TOKEN VALIDATION

// handlerMuxReplicationManager handles the HTTP request for the replication manager.
// @Summary Handles replication manager requests
// @Description This endpoint processes the replication manager requests, validates cluster ACLs, and returns the cluster list in JSON format.
// @Tags Public
// @Accept  json
// @Produce  json
// @Param Authorization header string false "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} ReplicationManager "Successful response with replication manager details"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/monitor [get]
func (repman *ReplicationManager) handlerMuxReplicationManager(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	var cl []string
	for _, cluster := range repman.Clusters {
		if valid, _ := repman.IsValidClusterACL(r, cluster); valid {
			cl = append(cl, cluster.Name)
		}
	}

	res, err := json.Marshal(repman)
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	res, err = sjson.SetBytes(res, "clusters", cl)

	for crkey := range repman.Conf.Secrets {
		res, err = sjson.SetBytes(res, "config."+strcase.ToLowerCamel(crkey), "*:*")
	}

	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

// handlerVersion handles the HTTP request for the replication manager version.
// @Summary Handles replication manager version requests
// @Description This endpoint processes the replication manager version requests and returns the version in JSON format.
// @Tags Public
// @Produce text/plain
// @Success 200 {string} string "Version"
// @Router /api/version [get]
func (repman *ReplicationManager) handlerVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write([]byte(repman.Fullversion))
}

// handlerMuxTerms handles HTTP requests for retrieving terms.
// @Summary Retrieves terms
// @Tags Cloud18
// @Description This endpoint returns the terms managed by the replication manager.
// @Produce text/plain
// @Success 200 {string} string "Terms"
// @Router /api/terms [get]
func (repman *ReplicationManager) handlerMuxTerms(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(repman.Terms)
}

// handlerMuxWhoAmI handles the HTTP request to identify the user making the request.
// @Summary Identify the user
// @Description Returns information about the user making the request based on the provided JWT token.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} map[string]string "User information"
// @Failure 500 {string} string "Error retrieving user info from token" or "Error Marshal"
// @Router /api/whoami [get]
func (repman *ReplicationManager) handlerMuxWhoAmI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	user, err := repman.GetJWTClaims(r)
	if err != nil {
		http.Error(w, "Error retrieving user info from token: "+err.Error(), http.StatusInternalServerError)
		return
	}

	res, err := json.Marshal(user)
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	res, err = sjson.SetBytes(res, "Password", "*****")
	if err != nil {
		http.Error(w, "Encoding error", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(res)
}

// handlerMuxAddClusterUser handles the addition of a new user to a cluster.
//
// @Summary Add a new user to a cluster
// @Description Adds a new user to the specified cluster if the request is valid and the user has the necessary permissions.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param userform body cluster.UserForm true "User Form"
// @Success 200 {string} string "User added successfully"
// @Failure 400 {string} string "Error in request"
// @Failure 403 {string} string "No Valid ACL"
// @Failure 500 {string} string "Error adding new user" or "No valid cluster"
// @Router /api/clusters/{clusterName}/users/add [post]
func (repman *ReplicationManager) handlerMuxAddClusterUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, delegator := repman.IsValidClusterACL(r, mycluster); valid {
			err := mycluster.AddUser(userform, delegator, true)
			if err != nil {
				http.Error(w, "Error adding new user: "+err.Error(), 500)
				return
			}
		} else {
			http.Error(w, "No Valid ACL", 403)
			return
		}
	} else {
		http.Error(w, "No valid cluster", 500)
		return
	}
}

// handlerMuxUpdateClusterUser handles the HTTP request to update a user in a cluster.
//
// @Summary Update a cluster user
// @Description Updates the user information for a specified cluster.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param userform body cluster.UserForm true "User Form"
// @Success 200 {string} string "User updated successfully"
// @Failure 400 {string} string "Error in request"
// @Failure 403 {string} string "No Valid ACL"
// @Failure 500 {string} string "Error updating user" or "No valid cluster"
// @Router /api/clusters/{clusterName}/users/update [post]
func (repman *ReplicationManager) handlerMuxUpdateClusterUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, delegator := repman.IsValidClusterACL(r, mycluster); valid {
			err := mycluster.UpdateUser(userform, delegator, true)
			if err != nil {
				http.Error(w, "Error updating user: "+err.Error(), 500)
				return
			}
		} else {
			http.Error(w, "No Valid ACL", 403)
			return
		}
	} else {
		http.Error(w, "No valid cluster", 500)
		return
	}
}

// handlerMuxDropClusterUser handles the HTTP request to drop a user from a cluster.
//
// @Summary Drop a cluster user
// @Description Drops a user from the specified cluster.
// @Tags User
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param userform body cluster.UserForm true "User Form"
// @Success 200 {string} string "User dropped successfully"
// @Failure 400 {string} string "Error in request"
// @Failure 403 {string} string "No Valid ACL"
// @Failure 500 {string} string "Error dropping user" or "No valid cluster"
// @Router /api/clusters/{clusterName}/users/drop [post]
func (repman *ReplicationManager) handlerMuxDropClusterUser(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	var userform cluster.UserForm
	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&userform)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster != nil {
		if valid, _ := repman.IsValidClusterACL(r, mycluster); valid {
			err := mycluster.DropUser(userform, true)
			if err != nil {
				http.Error(w, "Error dropping user: "+err.Error(), 500)
				return
			}
		} else {
			http.Error(w, "No Valid ACL", 403)
			return
		}
	} else {
		http.Error(w, "No valid cluster", 500)
		return
	}
}

// handlerMuxClusters handles the HTTP request for fetching clusters.
// @Summary Fetch clusters
// @Description Fetches the list of clusters that the user has access to based on ACL.
// @Tags Cluster
// @Produce application/json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {array} cluster.Cluster "List of clusters"
// @Failure 401 {string} string "Unauthenticated resource"
// @Failure 500 {string} string "Internal server error"
// @Router /api/clusters [get]
func (repman *ReplicationManager) handlerMuxClusters(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if ok, err := repman.isValidRequest(r); ok {

		var clusters []*cluster.Cluster

		for _, cluster := range repman.Clusters {
			if valid, _ := repman.IsValidClusterACL(r, cluster); valid {
				clusters = append(clusters, cluster)
			}
		}

		sort.Sort(cluster.ClusterSorter(clusters))

		var cl []byte = []byte("[")

		for i, cluster := range clusters {
			cj, err := cluster.GetCompactJson()
			if err != nil {
				http.Error(w, "Error getting compact JSON: "+err.Error(), 500)
				return
			}
			cl = append(cl, cj...)
			if i < len(clusters)-1 {
				cl = append(cl, ',')
			}
		}
		cl = append(cl, ']')

		w.Header().Set("Content-Type", "application/json")
		w.Write(cl)

	} else {
		http.Error(w, "Unauthenticated resource: "+err.Error(), 401)
		return
	}
}

// handlerMuxPeerNodes handles the request to retrieve all peer nodes status.
// @Summary Retrieve peer nodes status
// @Description This endpoint retrieves the status of all peer nodes.
// @Tags Cloud18
// @Produce application/json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {array} peer.PeerNodeStatus "List of peer nodes"
// @Failure 401 {string} string "Unauthenticated resource"
// @Failure 500 {string} string "Failed to get token claims or Error Marshal"
// @Router /api/peers [get]
func (repman *ReplicationManager) handlerMuxPeerNodes(w http.ResponseWriter, r *http.Request) {
	ok, err := repman.isValidRequest(r)
	if !ok {
		http.Error(w, "Unauthenticated resource: "+err.Error(), 401)
		return
	}

	uinfo, err := repman.GetJWTClaims(r)
	if err != nil {
		http.Error(w, "Failed to get token claims: "+err.Error(), 500)
		return
	}

	peerUser := uinfo["User"]
	if peerUser == "admin" {
		peerUser = repman.Conf.Cloud18GitUser
	}

	cl, err := repman.PeerManager.GetPeerNodesJSON()
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(cl)
}

// handlerMuxPeerClusters handles the request to retrieve peer clusters for a user.
// @Summary Retrieve peer clusters for a user
// @Description This endpoint retrieves the peer clusters that a user has access to.
// @Tags Cloud18
// @Produce application/json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {array} peer.PeerCluster "List of peer clusters"
// @Failure 401 {string} string "Unauthenticated resource"
// @Failure 500 {string} string "Failed to get token claims or Error Marshal"
// @Router /api/clusters/peers [get]
func (repman *ReplicationManager) handlerMuxPeerClusters(w http.ResponseWriter, r *http.Request) {
	ok, err := repman.isValidRequest(r)
	if !ok {
		http.Error(w, "Unauthenticated resource: "+err.Error(), 401)
		return
	}

	uinfo, err := repman.GetJWTClaims(r)
	if err != nil {
		http.Error(w, "Failed to get token claims: "+err.Error(), 500)
		return
	}

	peerUser := uinfo["User"]
	if peerUser == "admin" {
		peerUser = repman.Conf.Cloud18GitUser
	}

	cl, err := repman.PeerManager.GetUserClustersJSON(peerUser)
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(cl)
}

// handlerMuxPeerClustersForSale handles the HTTP request to retrieve the list of peer clusters available for sale.
// @Summary Retrieve peer clusters for sale
// @Description This endpoint returns a list of peer clusters that are available for sale, excluding those that are booked by the requesting user.
// @Tags Cloud18
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Produce application/json
// @Success 200 {array} peer.PeerCluster "List of peer clusters available for sale"
// @Failure 401 {string} string "Unauthenticated resource"
// @Failure 500 {string} string "Failed to get token claims or Error Marshal"
// @Router /api/clusters/for-sale [get]
func (repman *ReplicationManager) handlerMuxPeerClustersForSale(w http.ResponseWriter, r *http.Request) {
	ok, err := repman.isValidRequest(r)
	if !ok {
		http.Error(w, "Unauthenticated resource: "+err.Error(), 401)
		return
	}

	cl, err := repman.PeerManager.GetSaleClustersJSON()
	if err != nil {
		http.Error(w, "Error Marshal", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(cl)
}

// handlerMuxClusterSubscribe handles the subscription of a user to a cluster.
// @Summary Subscribe a user to a cluster
// @Description This endpoint allows a user to subscribe to a specified cluster.
// @Tags Cloud18
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param userform body cluster.UserForm true "User Form"
// @Success 200 {string} string "Email sent to admin!"
// @Failure 400 {string} string "Error in request"
// @Failure 403 {string} string "Error parsing JWT" / "Current user is not logged in via Gitlab!"
// @Failure 409 {string} string "User already subscribed on peer cluster!" / "Another user already subscribed on peer cluster!"
// @Failure 500 {string} string "No valid cluster" / "Peer does not have cloud18 setup!" / "Error sending email"
// @Failure 401 {string} string "Error logging in to gitlab: Token credentials is not valid"
// @Router /api/clusters/{clusterName}/subscribe [post]
func (repman *ReplicationManager) handlerMuxClusterSubscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	var userform cluster.UserForm

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No valid cluster", 500)
		return
	}

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
		return
	}

	if _, ok := uinfomap["profile"]; !ok {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprintf(w, "Current user is not logged in via Gitlab!")
		return
	}

	userform.Username = uinfomap["User"]
	userform.Password = repman.Conf.GetDecryptedPassword("peer-login", uinfomap["Password"])

	tok, _ := githelper.GetGitLabTokenBasicAuth(userform.Username, userform.Password, false)
	if tok == "" {
		http.Error(w, "Error logging in to gitlab: Token credentials is not valid", http.StatusUnauthorized)
		return
	}

	if repman.Conf.Cloud18GitUser == "" || repman.Conf.Cloud18GitPassword == "" || !repman.Conf.Cloud18 {
		http.Error(w, "Peer does not have cloud18 setup!", 500)
		return
	}

	for _, auser := range mycluster.APIUsers {
		v, ok := auser.Grants["pending"]
		v2, ok2 := auser.Grants["sponsor"]
		if (ok && v) || (ok2 && v2) {
			if auser.User == userform.Username {
				http.Error(w, "User already subscribed on peer cluster!", http.StatusConflict)
			} else {
				http.Error(w, "Another user already subscribed on peer cluster!", http.StatusConflict)
			}
			return
		}
	}

	roles := []string{"pending"}
	grants := []string{}
	auser, ok := mycluster.APIUsers[userform.Username]
	if ok {
		for role, v := range auser.Roles {
			if v {
				roles = append(roles, role)
			}
		}
		userform.Roles = strings.Join(roles, " ")

		for grant, v := range auser.Grants {
			if v {
				grants = append(grants, grant)
			}
		}
		userform.Grants = strings.Join(grants, " ")
		mycluster.UpdateUser(userform, repman.Conf.Cloud18GitUser, true)
	} else {
		userform.Roles = strings.Join(roles, " ")
		userform.Grants = strings.Join(grants, " ")
		mycluster.AddUser(userform, repman.Conf.Cloud18GitUser, true)
	}

	// User already listed as pending
	mycluster.LogModulePrintf(mycluster.Conf.Verbose, config.ConstLogModGeneral, "ALERT", "User %s has subscribed to cluster %s", userform.Username, mycluster.Name)

	if repman.Conf.Cloud18SalesSubscriptionScript != "" {
		repman.BashScriptSalesSubscribe(mycluster, userform.Username)
	}

	err = repman.SendCloud18ClusterSubscriptionMail(mycluster.Name, userform)
	if err != nil {
		http.Error(w, "Error sending email :"+err.Error(), 500)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent to admin!"))
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

func (repman *ReplicationManager) validateSwaggerMiddleware(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	//validate token

	if !repman.Conf.ApiSwaggerEnabled {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "Swagger is not enabled")
		return
	}
	next(w, r)
	return
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

// handlerMuxClusterAdd handles the addition of a new cluster.
//
// @Summary Add a new cluster
// @Description Adds a new cluster to the replication manager. If the cluster already exists, it returns an error.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param cluster body cluster.ClusterForm true "Cluster Form"
// @Success 200 {object} cluster.Cluster "Cluster added successfully"
// @Failure 400 {string} string "Error in request or Cluster already exists"
// @Failure 500 {string} string "User is not valid"
// @Router /api/clusters/actions/add/{clusterName} [post]
func (repman *ReplicationManager) handlerMuxClusterAdd(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	username := repman.GetUserFromRequest(r)
	if username == "" {
		http.Error(w, "User is not valid", http.StatusInternalServerError)
		return
	}

	var cForm cluster.ClusterForm

	//decode request into UserCredentials struct
	err := json.NewDecoder(r.Body).Decode(&cForm)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprintf(w, "Error in request")
		return
	}

	if cname, ok := vars["clusterName"]; !ok || cname == "" {
		vars["clusterName"] = cForm.ClusterName
	}

	cl := repman.getClusterByName(vars["clusterName"])
	if cl != nil {
		http.Error(w, "Cluster already exists", http.StatusBadRequest)
		return
	}

	repman.AddCluster(vars["clusterName"], "")
	// Create user and grant for new cluster
	cl = repman.getClusterByName(vars["clusterName"])
	if cl != nil {
		cl.Conf.APIUsersExternal = ""
		cl.Conf.APIUsersACLAllowExternal = ""
		cl.Conf.APIUsersACLDiscardExternal = ""

		repman.AddLocalAdminUserACL(cl, false)

		if repman.Conf.Cloud18GitUser != "" {
			repman.AddCloud18GitUser(cl, false)
		}

		cl.LoadAPIUsers()
		cl.SaveAcls()

		// Adjust cluster based on selected orchestrator
		if cForm.Orchestrator != "" && cForm.Orchestrator != cl.Conf.ProvOrchestrator {
			cl.SetProvOrchestrator(cForm.Orchestrator)
		}

		// Cluster will auto set service when plan is not empty
		if cForm.Plan != "" {
			cl.SetServicePlan(cForm.Plan)
		}

		cl.Save()
	}
}

// handlerMuxClusterDelete handles the deletion of a cluster.
// @Summary Delete a cluster
// @Description Deletes a cluster identified by its name.
// @Tags Cluster
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {string} string "Cluster deleted successfully"
// @Failure 500 {string} string "Invalid cluster name" or "No Valid ACL"
// @Router /api/clusters/actions/delete/{clusterName} [delete]
func (repman *ReplicationManager) handlerMuxClusterDelete(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Invalid cluster name", 500)
		return
	}

	valid, _ := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No Valid ACL", 500)
		return
	}

	repman.DeleteCluster(vars["clusterName"])
}

// handlerMuxClusterRename handles the HTTP request to rename a cluster.
// @Summary Rename a cluster
// @Description Renames a cluster identified by its name.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Param newClusterName path string true "New Cluster Name"
// @Success 200 {string} string "Cluster renamed successfully"
// @Failure 500 {string} string "Invalid cluster name" or "Cluster name already exists" or "No Valid ACL"
// @Router /api/clusters/actions/rename/{clusterName}/{newClusterName} [post]
func (repman *ReplicationManager) handlerMuxClusterRename(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)

	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "Invalid cluster name", 500)
		return
	}

	if slices.Contains(repman.ImmutableClusterList, mycluster.Name) {
		http.Error(w, "Cluster is not dynamic", 500)
		return
	}

	valid, _ := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No Valid ACL", 500)
		return
	}

	// Check if new cluster name is already exist
	if newcluster := repman.getClusterByName(vars["newClusterName"]); newcluster != nil {
		http.Error(w, "Cluster name already exists", 500)
		return
	}

	err := mycluster.RenameCluster(vars["newClusterName"])
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Cluster renamed successfully"))
}

// handlerMuxPrometheus handles HTTP requests to fetch Prometheus metrics for all servers in all clusters.
// @Summary Fetch Prometheus metrics
// @Description Fetches Prometheus metrics for all servers in all clusters managed by the replication manager.
// @Tags Public
// @Produce plain
// @Success 200 {string} string "Prometheus metrics"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/prometheus [get]
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

// handlerMuxStatus handles the HTTP request to check the status of the replication manager.
// @Summary Get Replication Manager Status
// @Description Returns the status of the replication manager indicating whether it is running or starting.
// @Tags Public
// @Produce json
// @Success 200 {object} map[string]string "{"alive": "running"} or {"alive": "starting"}"
// @Router /api/status [get]
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

// handlerMuxTimeout handles HTTP requests and responds with a JSON indicating the service is running.
//
// @Summary Check if the replication manager is running
// @Description This endpoint is used to check if the replication manager is running. It will respond with a JSON object after a delay of 1200 seconds.
// @Tags Public
// @Produce application/json
// @Success 200 {object} map[string]string
// @Router /api/timeout [get]
func (repman *ReplicationManager) handlerMuxTimeout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	time.Sleep(1200 * time.Second)
	io.WriteString(w, `{"alive": "running"}`)
}

// handlerMuxMonitorHeartbeat handles the HTTP request for monitoring the heartbeat of the replication manager.
// @Summary Monitor Heartbeat
// @Description Returns the heartbeat status of the replication manager.
// @Tags Public
// @Accept json
// @Produce json
// @Success 200 {object} Heartbeat
// @Failure 500 {object} map[string]string
// @Router /api/heartbeat [get]
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
		w.Header().Set("Etag", repman.Fullversion)

		h.ServeHTTP(w, r)
	})
}

// handlerMuxGrafana handles HTTP requests to list Grafana files.
// @Summary List Grafana files
// @Description Returns a list of Grafana files from the specified directory.
// @Tags Public
// @Produce json
// @Success 200 {array} string "List of Grafana files"
// @Failure 500 {string} string "Internal Server Error"
// @Router /api/configs/grafana [get]
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

func (repman *ReplicationManager) RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				// Capture error and stack trace
				repman.Logrus.WithFields(logrus.Fields{
					"error":      err,
					"stacktrace": string(debug.Stack()),
					"url":        r.URL.String(),
				}).Print("Recovered from panic")

				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// isAllowedPeer checks if a given peer URL is in the allowed list
func (repman *ReplicationManager) IsAllowedPeer(peerURL string) bool {
	return repman.PeerManager.HasPeerURL(peerURL)
}

// isAllowedPeer checks if a given peer URL is in the allowed list
func (repman *ReplicationManager) IsAllowedPeerRoute(route string) bool {
	prefixes := []string{"api"}
	for _, prefix := range prefixes {
		if strings.HasPrefix(route, prefix) {
			return true
		}
	}
	return false
}

// DynamicPeerHandler forwards requests to the specified peer URL
func (repman *ReplicationManager) DynamicPeerHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the Base64-encoded peer URL and the route from the path
	vars := mux.Vars(r)
	encodedPeer := vars["encodedpeer"]
	route := vars["route"]

	// logRequest(r)

	// Decode the Base64-encoded peer URL
	peer, err := base64.StdEncoding.DecodeString(encodedPeer)
	if err != nil {
		http.Error(w, "Invalid peer URL encoding", http.StatusBadRequest)
		log.Printf("Error decoding peer URL: %v", err)
		return
	}

	// Convert the decoded URL to string
	peerURL := string(peer)

	// Validate the peer URL
	if !repman.IsAllowedPeer(peerURL) {
		http.Error(w, "Peer URL not allowed", http.StatusForbidden)
		log.Printf("Blocked forwarding to disallowed peer: %s", peerURL)
		return
	}

	// Parse the peer URL
	parsedPeerURL, err := url.Parse(peerURL)
	if err != nil {
		http.Error(w, "Invalid peer URL", http.StatusBadRequest)
		log.Printf("Error parsing peer URL: %v", err)
		return
	}

	// Attach the specific route from the URL to the peer URL
	parsedPeerURL.Path = parsedPeerURL.Path + "/" + route

	var user userCredentials
	var status int
	var body []byte
	if route == "api/login" {
		//decode request into UserCredentials struct
		err = json.NewDecoder(r.Body).Decode(&user)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "Error in request")
			return
		}

		uinfomap, err := repman.GetJWTClaims(r)
		if err != nil {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "Error parsing JWT: %s", err.Error())
			return
		}

		if _, ok := uinfomap["profile"]; !ok {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprintf(w, "Current user is not logged in via Gitlab!")
			return
		}

		user.Password = repman.Conf.GetDecryptedPassword("peer-login", uinfomap["Password"])

		status, body = repman.PeerLogin(parsedPeerURL, user)

	} else {
		// Forward the request to the peer URL
		status, body = repman.PeerRequestForwarder(parsedPeerURL, r)
	}

	w.WriteHeader(status)
	w.Write(body)
}

/*
// logRequest logs details of the incoming HTTP request
func logRequest(r *http.Request) {
	log.Printf("Incoming Request -> Method: %s, URL: %s", r.Method, r.URL.String())
	log.Printf("Incoming Headers: %v", r.Header)
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		log.Printf("Incoming Body: %s", string(body))
		r.Body = io.NopCloser(bytes.NewReader(body)) // Reset the body for further use
	}
}

// logForwardedRequest logs details of the request sent to GoApp 2
func logForwardedRequest(req *http.Request) {
	log.Printf("Forwarding Request -> Method: %s, URL: %s", req.Method, req.URL.String())
	log.Printf("Forwarding Headers: %v", req.Header)
	if req.Body != nil {
		body, _ := io.ReadAll(req.Body)
		log.Printf("Forwarding Body: %s", string(body))
		req.Body = io.NopCloser(bytes.NewReader(body)) // Reset the body for sending
	}
}

// logResponse logs details of the HTTP response received from GoApp 2
func logResponse(resp *http.Response) {
	log.Printf("Response Status: %s", resp.Status)
	log.Printf("Response Headers: %v", resp.Header)
	if resp.Body != nil {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Response Body: %s", string(body))
		resp.Body = io.NopCloser(bytes.NewReader(body)) // Reset the body for further use
	}
}*/

// handlerTerminal handles the WebSocket connection for a terminal session.
// @Summary Terminal
// @Description	Establishes a WebSocket connection for a terminal session.
// @Tags Terminal
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Param serverName path string false "Server Name"
// @Success 200 {string} string "Connected successfully"
// @Failure 400 {string} string "No user provided"
// @Failure 500 {string} string "No valid node" or "No valid cluster"
// @Router /api/terminal/connect [get]
// @Router /api/terminal/connect/clusters/{clusterName}/servers/{serverName} [get]
// @Router /api/terminal/connect/clusters/{clusterName}/proxies/{serverName} [get]
// @Router /api/terminal/connect/clusters/{clusterName}/servers/{serverName}/{command} [get]
// @Router /api/terminal/connect/clusters/{clusterName}/proxies/{serverName}/{command} [get]
func (repman *ReplicationManager) handlerTerminal(w http.ResponseWriter, r *http.Request) {
	defer repman.LogPanicToFile()

	var err error
	var mycluster *cluster.Cluster
	var node *cluster.ServerMonitor
	var proxy cluster.DatabaseProxy

	vars := mux.Vars(r)
	path := r.URL.Path

	if !repman.Conf.TerminalSessionEnabled {
		http.Error(w, "Terminal session is disabled", 403)
		return
	}

	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}

	// Upgrade the HTTP connection to a WebSocket.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Error upgrading connection: %v", err)
		http.Error(w, "Failed to upgrade connection", 500)
		return
	}
	defer conn.Close()

	session := repman.SessionManager.CreateSession(conn)

	// Upgrade successful, create a new session
	repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Socket upgraded successfully for url %s", r.URL.String())
	session.SafeWriteMessage(websocket.TextMessage, []byte("Connected. Waiting for token...\n"))

	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)

	// Handle the message
	token := session.HandleWebToken(ctx)
	if ctx.Err() == context.DeadlineExceeded {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Timeout waiting for token\n"))
		return
	}

	if token == "" {
		session.SafeWriteMessage(websocket.TextMessage, []byte("No valid token received\n"))
		return
	}

	session.SafeWriteMessage(websocket.TextMessage, []byte("Token received. Validating...\n"))

	// Validate the token
	uinfomap, err := repman.ParseWebSocketJWT(token)
	if err != nil {
		session.SafeWriteMessage(websocket.TextMessage, []byte("Invalid token\n"))
		return
	}

	// uinfo, err := json.Marshal(uinfomap)
	// if err != nil {
	// 	session.SafeWriteMessage(websocket.TextMessage, []byte("Error encoding JSON"))
	// 	return
	// }

	// Send uinfomap to the client
	// session.SafeWriteMessage(websocket.TextMessage, bytes.Join([][]byte{[]byte("User info:"), uinfo}, []byte(" ")))

	plainuser := uinfomap["User"]
	username := plainuser
	md5h := md5.New()
	md5h.Write([]byte(username))
	username = string(md5h.Sum(nil))

	sessionID := "global"

	if vars["clusterName"] == "" {
		// Check if the user is allowed to access the terminal
		if uinfomap["User"] != "admin" && uinfomap["User"] != repman.Conf.Cloud18GitUser {
			session.SafeWriteMessage(websocket.TextMessage, []byte("No valid ACL\n"))
			return
		}

		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Terminal session started for user %s", plainuser)
	} else {
		mycluster = repman.getClusterByName(vars["clusterName"])
		if mycluster == nil {
			session.SafeWriteMessage(websocket.TextMessage, []byte("No valid cluster\n"))
			return
		}

		method := "password"
		if _, ok := uinfomap["profile"]; ok {
			method = "oidc"
		}

		if !mycluster.IsValidACL(uinfomap["User"], uinfomap["Password"], path, method) {
			session.SafeWriteMessage(websocket.TextMessage, []byte("No valid ACL\n"))
			return
		}

		node = mycluster.GetServerFromName(vars["serverName"])
		proxy = mycluster.GetProxyFromName(vars["serverName"])
		if node != nil {
			sessionID = mycluster.Name + "-" + node.Name
		} else if proxy != nil {
			sessionID = mycluster.Name + "-" + proxy.GetName()
		} else {
			session.SafeWriteMessage(websocket.TextMessage, []byte("No valid node\n"))
			return
		}
		repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Terminal session started for user %s on cluster %s", plainuser, mycluster.Name)

	}

	finalID := username + "-" + sessionID

	session.Owner = uinfomap["User"]
	session.ID = finalID
	session.AllowResume = repman.Conf.TerminalSessionResume
	session.TerminalMgr = repman.GetTerminalManager()

	if sessionID == "global" {
		session.WorkingDir = repman.OsUser.HomeDir
		// Create a new session or resume the existing session by ID
		session, err = repman.SessionManager.RunSession(session)
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Error creating or resuming session: %v", err)
			session.SafeWriteMessage(websocket.TextMessage, []byte("Failed to create or resume session\n"))
			return
		}
	} else {
		cmdstring, ok := vars["command"]
		if ok {
			session.CmdType, err = tty.GetTerminalCommandType(cmdstring)
			if err != nil {
				session.SafeWriteMessage(websocket.TextMessage, []byte("Invalid command\n"))
				return
			}

			if cmdstring == "" {
				cmdstring = "SSH"
			}
		}

		if node != nil {
			err = repman.SetSessionValuesFromNode(session, node)
		} else if proxy != nil {
			err = repman.SetSessionValuesFromProxy(session, proxy)
		}
		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Error setting session values from node: %v", err)
			session.SafeWriteMessage(websocket.TextMessage, []byte("Failed to set session values from node\n"))
			return
		}

		if session.CmdType == tty.TerminalBash {
			if session.Orchestrator == config.ConstOrchestratorOpenSVC {
				url, node := mycluster.GetGottyServer(session.ServiceName, session.ServiceContainerName)
				session.ServiceGottyUrl = strings.Replace(url, node, node+".signal18.io", 1)
				repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Terminal session OpenSVC Service Gotty Url  %s on cluster %s", session.ServiceGottyUrl, mycluster.Name)

				session.Arguments = append(session.Arguments, "--v2")
				session.Arguments = append(session.Arguments, "--skip-tls-verify")
				session.Arguments = append(session.Arguments, session.ServiceGottyUrl)

				session.CmdType = tty.TerminalGottyClient
				session.Command = mycluster.GetGottyClientPath()
				session, err = repman.SessionManager.RunSession(session)
			} else {
				session, err = repman.SessionManager.RunSSHSession(session)
			}
		} else if session.CmdType == tty.TerminalMySQL {
			session.Command = mycluster.GetMysqlclientPath()
			session.Arguments = append(session.Arguments, "-p")
			session, err = repman.SessionManager.RunSession(session)
		} else if session.CmdType == tty.TerminalMyTop {
			session.Command = mycluster.GetMytopPath()
			session.Arguments = append(session.Arguments, "--prompt")
			session, err = repman.SessionManager.RunSession(session)
		} else {
			session.SafeWriteMessage(websocket.TextMessage, []byte("Invalid command\n"))
			return
		}

		if err != nil {
			repman.LogModulePrintf(repman.Conf.Verbose, config.ConstLogModGeneral, config.LvlInfo, "Error creating or resuming SSH session: %v", err)
			session.SafeWriteMessage(websocket.TextMessage, []byte("Failed to create or resume '"+cmdstring+"' session\n"))
			return
		}
	}

	// Wait for the session to finish (or be closed).
	session.WG.Wait()
}

// handlerGetTerminalSessionList handles the HTTP request to retrieve a list of terminal sessions.
// @Summary Get Terminal Session List
// @Description Returns a list of terminal sessions for a user.
// @Tags Terminal
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Success 200 {array} string "List of terminal sessions"
// @Failure 403 {string} string "Terminal session is disabled"
// @Failure 500 {string} string "Error getting JWT claims" or "Error encoding JSON"
// @Router /api/terminal/list [get]
// @Router /api/terminal/list/clusters/{clusterName} [get]
func (repman *ReplicationManager) handlerGetTerminalSessionList(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	claims, err := repman.GetJWTClaims(r)
	if err != nil {
		http.Error(w, "Error getting JWT claims", 500)
		return
	}

	if !repman.Conf.TerminalSessionEnabled {
		http.Error(w, "Terminal session is disabled", 403)
		return
	}

	username := claims["User"]
	md5h := md5.New()
	md5h.Write([]byte(username))
	username = string(md5h.Sum(nil))

	sessionID := "global"

	if vars["clusterName"] != "" {
		sessionID = vars["clusterName"]
	}

	finalID := username + "-" + sessionID

	sessions := repman.SessionManager.GetSessions(finalID)

	// Return the list of sessions as JSON
	jsonSessions, err := json.Marshal(sessions)
	if err != nil {
		http.Error(w, "Error encoding JSON", 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(jsonSessions)
}

// handlerMuxSendEmail handles the HTTP request to send an email.
// @Summary Send Email
// @Description Sends an email to the specified recipient.
// @Tags AlertMailer
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string false "Cluster Name"
// @Param email body mailer.Email true "Email details"
// @Success 200 {string} string "Email sent successfully"
// @Failure 400 {string} string "Error in request"
// @Failure 500 {string} string "Error sending email"
// @Router /api/email/send [post]
// @Router /api/clusters/{clusterName}/actions/send-email [post]
func (repman *ReplicationManager) handlerMuxSendEmail(w http.ResponseWriter, r *http.Request) {
	var email mailer.Email
	vars := mux.Vars(r)

	uinfomap, err := repman.GetJWTClaims(r)
	if err != nil {
		http.Error(w, "Error getting JWT claims", 500)
		return
	}

	// Decode the request body into the Email struct
	err = json.NewDecoder(r.Body).Decode(&email)
	if err != nil {
		http.Error(w, "Error in request", 400)
		return
	}

	clusterName := vars["clusterName"]
	if clusterName != "" {
		mycluster := repman.getClusterByName(clusterName)
		if mycluster == nil {
			http.Error(w, "Invalid cluster name", 500)
			return
		}

		if !mycluster.IsValidACL(uinfomap["User"], uinfomap["Password"], r.URL.Path, "oidc") {
			http.Error(w, "No valid ACL", 500)
			return
		}

		// Send the email in the cluster scope
		err = mycluster.SendMail(email)
		if err != nil {
			http.Error(w, "Error sending email: "+err.Error(), 500)
			return
		}

	} else {
		if uinfomap["User"] != "admin" && uinfomap["User"] != repman.Conf.Cloud18GitUser {
			http.Error(w, "No valid ACL", 500)
			return
		}

		// Send the email in global scope
		err = repman.SendMail(email)
		if err != nil {
			http.Error(w, "Error sending email: "+err.Error(), 500)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Email sent successfully"))
}

// handlerMuxHealth handles the HTTP request to retrieve the status of all privileged clusters.
// @Summary Get Cluster Health
// @Description Returns the health status of all privileged clusters.
// @Tags Cluster
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Success 200 {object} map[string]peer.PeerHealth "List of cluster health statuses"
// @Failure 500 {string} string "Error getting JWT claims"
// @Router /api/health [get]
func (repman *ReplicationManager) handlerMuxHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	healths := make(map[string]peer.PeerHealth)
	for _, mycluster := range repman.Clusters {
		healths[mycluster.Name] = mycluster.GetPeerHealth()
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(healths)
}
