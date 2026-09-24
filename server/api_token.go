// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/codegangsta/negroni"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/golang-jwt/jwt/v5/request"
	"github.com/gorilla/mux"
	"github.com/signal18/replication-manager/cluster"
	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
)

// User-issued API tokens (issue #1835).
//
// A token is a bearer credential a user issues for THEMSELVES: an HS256 JWT signed
// with the persistent secret key (monitoring-key-path), so it survives repman
// restarts (the RSA login key is regenerated at every start) and rotates on purpose
// with the key. It embeds its authority — subject, grant list, cluster scope, expiry
// — and can only NARROW the owner's grants: at request time the cluster ACL runs on
// the intersection of the token's grants and the owner's current grants
// (cluster.GetACLUser), so revoking a grant from the user revokes it from every
// token, and deleting the user kills the tokens.
//
// Every token also has a record in the store, WorkingDir/api-tokens.json, a JSON
// document AES-encrypted with the same secret key (utils/crypto.Password) and kept
// out of the config git sync. The store is the revocation list and the audit trail
// (created / last used / revoked), and it keeps the token string itself so the
// tokens reload at restart and an owner can display one again from the GUI. A
// bearer token whose jti is not in the store, or is revoked, is refused even if its
// signature is valid.
//
// Off-switch (T14): api-user-tokens=false refuses both issuing and authenticating.

const (
	apiTokenTyp     = "api" // the `typ` claim marking a user-issued API token
	apiTokenStore   = "api-tokens.json"
	apiTokenIDBytes = 12
	// apiTokenLastUsedFlush bounds disk writes: last-used is persisted at most once
	// per token per this interval (the in-memory value is always current).
	apiTokenLastUsedFlush = time.Minute
	// apiTokenRetention bounds the store (T18): a revoked or expired record is kept
	// this long for the audit trail, then dropped at the next save.
	apiTokenRetention = 90 * 24 * time.Hour
)

// APIToken is a stored token record.
type APIToken struct {
	ID           string    `json:"id"`
	User         string    `json:"user"`
	Label        string    `json:"label"`
	Grants       []string  `json:"grants"`   // compact grant prefixes requested by the owner
	Clusters     []string  `json:"clusters"` // cluster scope; ["*"] = all
	CreatedAt    time.Time `json:"createdAt"`
	CreatedFrom  string    `json:"createdFrom"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
	LastUsedAt   time.Time `json:"lastUsedAt,omitempty"`
	LastUsedFrom string    `json:"lastUsedFrom,omitempty"`
	RevokedAt    time.Time `json:"revokedAt,omitempty"`
	RevokedBy    string    `json:"revokedBy,omitempty"`
	Token        string    `json:"token"` // the bearer string, kept so it reloads and can be shown again to its owner
}

// APITokenView is what the API returns: the token string is only included when
// the caller is the owner (list) or on creation.
type APITokenView struct {
	APIToken
	Token   string `json:"token,omitempty"`
	Expired bool   `json:"expired"`
	Revoked bool   `json:"revoked"`
}

// IsRevoked reports whether the token was revoked.
func (t APIToken) IsRevoked() bool { return !t.RevokedAt.IsZero() }

// IsExpired reports whether the token is past its expiry (never, when none).
func (t APIToken) IsExpired() bool { return !t.ExpiresAt.IsZero() && time.Now().After(t.ExpiresAt) }

// IsGlobal reports whether the token covers every cluster.
func (t APIToken) IsGlobal() bool {
	for _, c := range t.Clusters {
		if c == "*" {
			return true
		}
	}
	return false
}

// InScope reports whether the token covers clusterName.
func (t APIToken) InScope(clusterName string) bool {
	for _, c := range t.Clusters {
		if c == "*" || c == clusterName {
			return true
		}
	}
	return false
}

// deadSince returns when the token stopped being usable (revocation or expiry)
// and whether it has.
func (t APIToken) deadSince() (time.Time, bool) {
	switch {
	case t.IsRevoked():
		return t.RevokedAt, true
	case t.IsExpired():
		return t.ExpiresAt, true
	}
	return time.Time{}, false
}

func (t APIToken) view(withToken bool) APITokenView {
	v := APITokenView{APIToken: t, Expired: t.IsExpired(), Revoked: t.IsRevoked()}
	v.APIToken.Token = ""
	if withToken {
		v.Token = t.Token
	}
	return v
}

// apiTokenStore is the in-memory image of WorkingDir/api-tokens.json.
type apiTokenStoreState struct {
	sync.Mutex
	loaded    bool
	tokens    map[string]*APIToken // by id
	lastFlush map[string]time.Time // last persisted last-used per id
}

type apiTokenFile struct {
	Version int        `json:"version"`
	Tokens  []APIToken `json:"tokens"`
}

func (repman *ReplicationManager) apiTokenStorePath() string {
	return filepath.Join(repman.Conf.WorkingDir, apiTokenStore)
}

// apiTokensEnabled is the T14 off-switch.
func (repman *ReplicationManager) apiTokensEnabled() bool {
	return repman.Conf.APIUserTokens && len(repman.Conf.SecretKey) > 0
}

// loadAPITokenStoreLocked reads and decrypts the store once. Caller holds the lock.
func (repman *ReplicationManager) loadAPITokenStoreLocked() error {
	st := &repman.apiTokens
	if st.loaded {
		return nil
	}
	// `loaded` flips only once the file is read and parsed (or absent): a transient
	// read/decrypt failure must not leave an empty store that the next save would
	// write back over the real one.
	tokens := map[string]*APIToken{}
	data, err := os.ReadFile(repman.apiTokenStorePath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && len(strings.TrimSpace(string(data))) > 0 {
		p := crypto.Password{Key: repman.Conf.SecretKey, CipherText: strings.TrimSpace(string(data))}
		if err := p.Decrypt(); err != nil {
			return fmt.Errorf("api token store: cannot decrypt %s: %w", repman.apiTokenStorePath(), err)
		}
		var f apiTokenFile
		if err := json.Unmarshal([]byte(p.PlainText), &f); err != nil {
			return fmt.Errorf("api token store: cannot parse %s (wrong key?): %w", repman.apiTokenStorePath(), err)
		}
		for i := range f.Tokens {
			t := f.Tokens[i]
			tokens[t.ID] = &t
		}
	}
	st.tokens = tokens
	st.lastFlush = map[string]time.Time{}
	st.loaded = true
	return nil
}

// saveAPITokenStoreLocked encrypts and atomically rewrites the store. Caller holds the lock.
func (repman *ReplicationManager) saveAPITokenStoreLocked() error {
	st := &repman.apiTokens
	f := apiTokenFile{Version: 1}
	now := time.Now()
	for id, t := range st.tokens {
		if end, dead := t.deadSince(); dead && now.Sub(end) > apiTokenRetention {
			delete(st.tokens, id)
			delete(st.lastFlush, id)
			continue
		}
		f.Tokens = append(f.Tokens, *t)
	}
	sort.Slice(f.Tokens, func(i, j int) bool { return f.Tokens[i].CreatedAt.Before(f.Tokens[j].CreatedAt) })
	plain, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	p := crypto.Password{Key: repman.Conf.SecretKey, PlainText: string(plain)}
	p.Encrypt()
	if p.CipherText == "" {
		return errors.New("api token store: encryption failed")
	}
	path := repman.apiTokenStorePath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(p.CipherText+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// newAPITokenID returns a random url-safe token id (the JWT jti).
func newAPITokenID() (string, error) {
	b := make([]byte, apiTokenIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// mintAPIToken signs the bearer string for a record.
func (repman *ReplicationManager) mintAPIToken(t APIToken) (string, error) {
	if len(repman.Conf.SecretKey) == 0 {
		return "", errors.New("cannot mint API token: no persistent secret key (monitoring-key-path)")
	}
	claims := jwt.MapClaims{
		"typ":      apiTokenTyp,
		"iss":      "https://api.replication-manager.signal18.io",
		"sub":      t.User,
		"jti":      t.ID,
		"grants":   t.Grants,
		"clusters": t.Clusters,
		"iat":      t.CreatedAt.Unix(),
	}
	if !t.ExpiresAt.IsZero() {
		claims["exp"] = t.ExpiresAt.Unix()
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(repman.Conf.SecretKey)
}

// parseAPITokenFromRequest returns the stored record of a valid user-issued API
// token carried by the request, or (nil, false) when the request carries no such
// token (no bearer, an RSA login JWT, bad signature, unknown / revoked / expired
// id, disabled feature). Signature, expiry and store are all checked here.
func (repman *ReplicationManager) parseAPITokenFromRequest(r *http.Request) (*APIToken, bool) {
	t, _, ok := repman.apiTokenFromRequest(r)
	return t, ok
}

// apiTokenFromRequest is parseAPITokenFromRequest plus whether the bearer LOOKS like
// an API token (HMAC-signed) at all, so the middleware can answer a rejected token
// with a clean 401 instead of falling through to the RSA parser's error text.
func (repman *ReplicationManager) apiTokenFromRequest(r *http.Request) (*APIToken, bool, bool) {
	isHMAC := false
	if raw, err := request.AuthorizationHeaderExtractor.ExtractToken(r); err == nil && raw != "" {
		if unverified, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{}); err == nil {
			_, isHMAC = unverified.Method.(*jwt.SigningMethodHMAC)
		}
	}
	if !repman.apiTokensEnabled() {
		return nil, isHMAC, false
	}
	t, ok := repman.parseVerifiedAPIToken(r)
	return t, isHMAC, ok
}

func (repman *ReplicationManager) parseVerifiedAPIToken(r *http.Request) (*APIToken, bool) {
	tok, err := request.ParseFromRequest(r, request.AuthorizationHeaderExtractor,
		func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("not an API token")
			}
			return repman.Conf.SecretKey, nil
		},
		request.WithClaims(jwt.MapClaims{}),
	)
	if err != nil || tok == nil || !tok.Valid {
		return nil, false
	}
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false
	}
	if typ, _ := mc["typ"].(string); typ != apiTokenTyp {
		return nil, false
	}
	id, _ := mc["jti"].(string)
	sub, _ := mc["sub"].(string)
	if id == "" || sub == "" {
		return nil, false
	}
	st := &repman.apiTokens
	st.Lock()
	defer st.Unlock()
	if err := repman.loadAPITokenStoreLocked(); err != nil {
		repman.Logrus.Errorf("%s", err)
		return nil, false
	}
	t, ok := st.tokens[id]
	if !ok || t.User != sub || t.IsRevoked() || t.IsExpired() {
		return nil, false
	}
	// Owner must still exist somewhere.
	if !repman.userExists(t.User) {
		return nil, false
	}
	t.LastUsedAt = time.Now()
	t.LastUsedFrom = r.RemoteAddr
	if last, ok := st.lastFlush[id]; !ok || time.Since(last) > apiTokenLastUsedFlush {
		st.lastFlush[id] = time.Now()
		if err := repman.saveAPITokenStoreLocked(); err != nil {
			repman.Logrus.Errorf("api token store: %s", err)
		}
	}
	cp := *t
	return &cp, true
}

func (repman *ReplicationManager) userExists(username string) bool {
	for _, cl := range repman.Clusters {
		if _, ok := cl.APIUsers[username]; ok {
			return true
		}
	}
	return false
}

// tokenPrincipalFor registers the token's authority on a cluster and returns the
// principal name the ACL must run under. The grant map is the expansion of the
// stored compact prefixes over the cluster's grant catalogue (cluster.SetUserGrants),
// so "db-show" carries every db-show-* grant; the intersection with the owner's
// current grants happens in cluster.GetACLUser.
func (repman *ReplicationManager) tokenPrincipalFor(t *APIToken, cl *cluster.Cluster) string {
	tmp := cluster.APIUser{Grants: map[string]bool{}}
	cl.SetUserGrants(&tmp, strings.Join(t.Grants, " "))
	cl.SetTokenPrincipal(cluster.TokenPrincipal{
		ID:       t.ID,
		User:     t.User,
		Grants:   tmp.Grants,
		Clusters: t.Clusters,
	})
	return cluster.TokenPrincipalName(t.ID, t.User)
}

// tokenURLInScope applies the cluster scope to a URL evaluated on cl: a token
// scoped to named clusters may only touch that cluster's own endpoints; anything
// else (global settings, peers, cluster add) needs the "*" scope.
func tokenURLInScope(t *APIToken, cl *cluster.Cluster, URL string) bool {
	if t.IsGlobal() {
		return true
	}
	if !t.InScope(cl.Name) {
		return false
	}
	return URL == "/api/clusters/"+cl.Name || strings.HasPrefix(URL, "/api/clusters/"+cl.Name+"/")
}

// ---------------------------------------------------------------------------
// HTTP surface
// ---------------------------------------------------------------------------

func (repman *ReplicationManager) apiTokenRoutes(router *mux.Router) {
	router.Handle("/api/tokens", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAPITokens)),
	)).Methods(http.MethodGet, http.MethodPost, http.MethodOptions)
	router.Handle("/api/tokens/{tokenID}", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxAPITokenRevoke)),
	)).Methods(http.MethodDelete, http.MethodOptions)
	router.Handle("/api/clusters/{clusterName}/tokens", negroni.New(
		negroni.HandlerFunc(repman.validateTokenMiddleware),
		negroni.Wrap(http.HandlerFunc(repman.handlerMuxClusterAPITokens)),
	)).Methods(http.MethodGet, http.MethodOptions)
}

// APITokenForm is the create request body.
type APITokenForm struct {
	Label      string   `json:"label"`
	Grants     string   `json:"grants"`     // compact prefixes, space separated, e.g. "db-show proxy"; empty = all of the owner's grants
	Clusters   []string `json:"clusters"`   // empty or ["*"] = every cluster the owner can see
	ExpireDays int      `json:"expireDays"` // 0 = server default (api-user-tokens-default-expire-days); -1 = never
}

// handlerMuxAPITokens lists (GET) or creates (POST) the caller's own tokens.
//
// @Summary List or create the caller's API tokens
// @Description GET lists the caller's tokens (token strings included, they are the owner's). POST issues a new token narrowed to a subset of the caller's own grants and a cluster scope; the token can never carry a grant the caller does not hold.
// @Tags Auth
// @Accept json
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param token body APITokenForm false "Token form (POST)"
// @Success 200 {array} APITokenView
// @Failure 400 {string} string "Bad request"
// @Failure 403 {string} string "Forbidden"
// @Router /api/tokens [get]
// @Router /api/tokens [post]
func (repman *ReplicationManager) handlerMuxAPITokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if !repman.apiTokensEnabled() {
		http.Error(w, "API tokens are disabled (api-user-tokens) or no persistent secret key is set", http.StatusForbidden)
		return
	}
	username := repman.GetUserFromRequest(r)
	if username == "" {
		http.Error(w, "User is not valid", http.StatusForbidden)
		return
	}
	// A token cannot mint tokens, and a token-authenticated caller never sees
	// token strings: a narrowed token must not read back a wider sibling. Only an
	// interactive login may create or display tokens.
	_, viaToken := repman.parseAPITokenFromRequest(r)
	if viaToken && r.Method == http.MethodPost {
		http.Error(w, "An API token cannot issue tokens, log in with your credentials", http.StatusForbidden)
		return
	}
	switch r.Method {
	case http.MethodGet:
		repman.jsonResponse(repman.listAPITokens(username, "", !viaToken), w)
	case http.MethodPost:
		var form APITokenForm
		if err := json.NewDecoder(r.Body).Decode(&form); err != nil {
			http.Error(w, "Error in request: "+err.Error(), http.StatusBadRequest)
			return
		}
		t, err := repman.createAPIToken(username, form, r.RemoteAddr)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		repman.logSecurityEvent("api_token_created", username, r.RemoteAddr,
			fmt.Sprintf("API token %s (%s) created, grants [%s], clusters [%s]", t.ID, t.Label, strings.Join(t.Grants, " "), strings.Join(t.Clusters, ",")))
		repman.jsonResponse(t.view(true), w)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handlerMuxAPITokenRevoke revokes one token: its owner always may; another user
// needs the cluster-grant grant on every cluster the token covers.
//
// @Summary Revoke an API token
// @Tags Auth
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param tokenID path string true "Token id"
// @Success 200 {object} APITokenView
// @Failure 403 {string} string "Forbidden"
// @Failure 404 {string} string "Not found"
// @Router /api/tokens/{tokenID} [delete]
func (repman *ReplicationManager) handlerMuxAPITokenRevoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	username := repman.GetUserFromRequest(r)
	if username == "" {
		http.Error(w, "User is not valid", http.StatusForbidden)
		return
	}
	id := mux.Vars(r)["tokenID"]
	t, err := repman.revokeAPIToken(id, username, r)
	if err != nil {
		if errors.Is(err, errAPITokenNotFound) {
			http.Error(w, err.Error(), http.StatusNotFound)
		} else {
			http.Error(w, err.Error(), http.StatusForbidden)
		}
		return
	}
	repman.logSecurityEvent("api_token_revoked", username, r.RemoteAddr,
		fmt.Sprintf("API token %s (%s) of user %s revoked", t.ID, t.Label, t.User))
	repman.jsonResponse(t.view(false), w)
}

// handlerMuxClusterAPITokens lists every token whose scope covers the cluster, for
// users holding grant-show there (token strings never included).
//
// @Summary List the API tokens covering a cluster
// @Tags Auth
// @Produce json
// @Param Authorization header string true "Insert your access token" default(Bearer <Add access token here>)
// @Param clusterName path string true "Cluster Name"
// @Success 200 {array} APITokenView
// @Failure 403 {string} string "Forbidden"
// @Router /api/clusters/{clusterName}/tokens [get]
func (repman *ReplicationManager) handlerMuxClusterAPITokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	vars := mux.Vars(r)
	mycluster := repman.getClusterByName(vars["clusterName"])
	if mycluster == nil {
		http.Error(w, "No cluster", http.StatusNotFound)
		return
	}
	valid, username := repman.IsValidClusterACL(r, mycluster)
	if !valid {
		http.Error(w, "No valid ACL", http.StatusForbidden)
		return
	}
	if u, ok := repman.requestACLUser(r, mycluster); !ok || !u.Grants[config.GrantGrantShow] {
		http.Error(w, "No grant-show grant", http.StatusForbidden)
		return
	}
	_ = username
	repman.jsonResponse(repman.listAPITokens("", mycluster.Name, false), w)
}

var errAPITokenNotFound = errors.New("token not found")

// listAPITokens returns tokens filtered by owner and/or cluster scope.
func (repman *ReplicationManager) listAPITokens(owner string, clusterName string, withToken bool) []APITokenView {
	st := &repman.apiTokens
	st.Lock()
	defer st.Unlock()
	if err := repman.loadAPITokenStoreLocked(); err != nil {
		repman.Logrus.Errorf("%s", err)
	}
	out := []APITokenView{}
	for _, t := range st.tokens {
		if owner != "" && t.User != owner {
			continue
		}
		if clusterName != "" && !t.InScope(clusterName) {
			continue
		}
		out = append(out, t.view(withToken))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// createAPIToken validates the form against the owner's grants and scope, mints
// and stores the token.
func (repman *ReplicationManager) createAPIToken(owner string, form APITokenForm, from string) (*APIToken, error) {
	label := strings.TrimSpace(form.Label)
	if label == "" {
		return nil, errors.New("label is required")
	}
	if len(label) > 64 {
		return nil, errors.New("label is too long (64 max)")
	}
	// Scope: the clusters the owner exists in, or "*".
	scope := []string{}
	global := len(form.Clusters) == 0
	for _, c := range form.Clusters {
		c = strings.TrimSpace(c)
		if c == "*" {
			global = true
			break
		}
		if c == "" {
			continue
		}
		cl := repman.getClusterByName(c)
		if cl == nil {
			return nil, fmt.Errorf("unknown cluster %s", c)
		}
		if _, ok := cl.APIUsers[owner]; !ok {
			return nil, fmt.Errorf("you have no account on cluster %s", c)
		}
		scope = append(scope, c)
	}
	var targets []*cluster.Cluster
	if global {
		scope = []string{"*"}
		for _, cl := range repman.Clusters {
			if _, ok := cl.APIUsers[owner]; ok {
				targets = append(targets, cl)
			}
		}
	} else {
		for _, c := range scope {
			targets = append(targets, repman.getClusterByName(c))
		}
	}
	if len(targets) == 0 {
		return nil, errors.New("you have no account on any cluster")
	}
	// Grants: requested prefixes must each match a grant the owner holds in at
	// least one target cluster (the per-cluster intersection is applied at request
	// time). Empty = everything the owner holds, expressed as compact prefixes.
	requested := strings.Fields(form.Grants)
	var grants []string
	if len(requested) == 0 {
		set := map[string]bool{}
		for _, cl := range targets {
			allow, _ := config.GetCompactGrants(cl.APIUsers[owner].Grants)
			for _, g := range allow {
				set[g] = true
			}
		}
		for g := range set {
			grants = append(grants, g)
		}
		sort.Strings(grants)
	} else {
		for _, prefix := range requested {
			held := false
			for _, cl := range targets {
				if _, missing := cl.TokenGrantsAllowedFor(owner, prefix); len(missing) == 0 {
					held = true
					break
				}
			}
			if !held {
				return nil, fmt.Errorf("grant %s is not held by %s on the requested clusters", prefix, owner)
			}
			grants = append(grants, prefix)
		}
	}
	if len(grants) == 0 {
		return nil, errors.New("no grant to embed")
	}
	// Expiry.
	now := time.Now()
	var expires time.Time
	days := form.ExpireDays
	if days == 0 {
		days = repman.Conf.APIUserTokensDefaultExpireDays
	}
	if days > 0 {
		expires = now.Add(time.Duration(days) * 24 * time.Hour)
	}
	id, err := newAPITokenID()
	if err != nil {
		return nil, err
	}
	t := APIToken{
		ID:          id,
		User:        owner,
		Label:       label,
		Grants:      grants,
		Clusters:    scope,
		CreatedAt:   now,
		CreatedFrom: from,
		ExpiresAt:   expires,
	}
	t.Token, err = repman.mintAPIToken(t)
	if err != nil {
		return nil, err
	}
	st := &repman.apiTokens
	st.Lock()
	defer st.Unlock()
	if err := repman.loadAPITokenStoreLocked(); err != nil {
		return nil, err
	}
	st.tokens[t.ID] = &t
	if err := repman.saveAPITokenStoreLocked(); err != nil {
		delete(st.tokens, t.ID)
		return nil, err
	}
	return &t, nil
}

// revokeAPIToken marks a token revoked. The owner always may; anyone else needs
// cluster-grant on every cluster the token covers.
func (repman *ReplicationManager) revokeAPIToken(id string, by string, r *http.Request) (*APIToken, error) {
	st := &repman.apiTokens
	// Read the record, then authorize OUTSIDE the store lock: requestACLUser parses
	// the caller's own bearer, which takes the same (non-reentrant) lock.
	st.Lock()
	if err := repman.loadAPITokenStoreLocked(); err != nil {
		st.Unlock()
		return nil, err
	}
	rec, ok := st.tokens[id]
	if !ok {
		st.Unlock()
		return nil, errAPITokenNotFound
	}
	snapshot := *rec
	st.Unlock()
	if snapshot.User != by {
		for _, cl := range repman.Clusters {
			if !snapshot.InScope(cl.Name) {
				continue
			}
			u, ok := repman.requestACLUserOnCluster(r, cl)
			if !ok || !u.Grants[config.GrantClusterGrant] {
				return nil, fmt.Errorf("revoking another user's token needs the cluster-grant grant on %s", cl.Name)
			}
		}
	}
	st.Lock()
	defer st.Unlock()
	t, ok := st.tokens[id]
	if !ok {
		return nil, errAPITokenNotFound
	}
	if t.IsRevoked() {
		cp := *t
		return &cp, nil
	}
	t.RevokedAt = time.Now()
	t.RevokedBy = by
	if err := repman.saveAPITokenStoreLocked(); err != nil {
		return nil, err
	}
	for _, cl := range repman.Clusters {
		cl.DropTokenPrincipal(id)
	}
	cp := *t
	return &cp, nil
}

// requestACLUser returns the APIUser view a request acts as on a cluster: the
// token-narrowed view for an API token, the plain user otherwise. Handlers that
// check a grant directly (instead of through the URL ACL) must use it so a token
// cannot exceed its embedded grants.
func (repman *ReplicationManager) requestACLUser(r *http.Request, cl *cluster.Cluster) (cluster.APIUser, bool) {
	if t, ok := repman.parseAPITokenFromRequest(r); ok {
		if !tokenURLInScope(t, cl, r.URL.Path) {
			return cluster.APIUser{}, false
		}
		return cl.GetACLUser(repman.tokenPrincipalFor(t, cl))
	}
	return repman.loginACLUser(r, cl)
}

// requestACLUserOnCluster is requestACLUser for endpoints that are not under a
// cluster path but act on one (revoking a token covering cluster X from
// /api/tokens/{id}): a token needs cluster X in its scope, not the URL.
func (repman *ReplicationManager) requestACLUserOnCluster(r *http.Request, cl *cluster.Cluster) (cluster.APIUser, bool) {
	if t, ok := repman.parseAPITokenFromRequest(r); ok {
		if !t.InScope(cl.Name) {
			return cluster.APIUser{}, false
		}
		return cl.GetACLUser(repman.tokenPrincipalFor(t, cl))
	}
	return repman.loginACLUser(r, cl)
}

// loginACLUser is the APIUser view of an interactive (RSA JWT) login.
func (repman *ReplicationManager) loginACLUser(r *http.Request, cl *cluster.Cluster) (cluster.APIUser, bool) {
	username := repman.GetUserFromRequest(r)
	if username == "" {
		return cluster.APIUser{}, false
	}
	return cl.GetACLUser(username)
}
