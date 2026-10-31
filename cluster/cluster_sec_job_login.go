// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integrity of this file.

package cluster

import (
	"container/heap"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/crypto"
	"github.com/signal18/replication-manager/utils/jobapi"
	"github.com/signal18/replication-manager/utils/state"
)

// The job body: what a database jobs script posts to replication-manager (secret-login, job
// state, tasks, logs), AES-256-CBC encrypted with the server's password as the key.
//
// One-time format: {"data": <base64>, "iv": <32 hex>}, a random IV per message, the clear JSON
// object carrying "ts" (unix seconds) and "nonce" (random hex). It is accepted once, within
// api-secret-login-window seconds of its timestamp.
//
// Legacy format: {"data": <base64>}, the IV derived from the password, no timestamp, no nonce:
// the same body at every run, replayable. It is accepted for one thing only, fetching the new
// jobs script (the jobs check and jobs upgrade endpoints), and only until the new script answers
// (a one-time body from that server since start): everything else, secret-login included,
// refuses it. Not when the script is sent: a send that lands on another listener would leave
// the server refused everywhere with an old script and no way to upgrade. Each use opens WARN0276 on that server.
// No setting: the transition is the script upgrade itself, kept in memory per server.

// jobBody is the posted JSON.
type jobBody struct {
	Data string `json:"data"`
	IV   string `json:"iv"`
	Key  string `json:"key"` // "jobs": encrypted with the cluster's jobs key; else the server's password
}

// Job body keys, as OpenJobBody reports them.
const (
	JobKeyLegacy = "legacy" // the server's password, fixed IV: replayable
	JobKeyDB     = "db"     // the server's password, one-time
	JobKeyJobs   = "jobs"   // the cluster's jobs key, one-time: no database password involved
)

// jobNonceMax bounds the nonces kept (F4): a nonce is kept twice the window, a database server
// posts a few bodies a minute.
const jobNonceMax = 65536

// jobNonceClusterMax is one cluster's share of the store: a cluster whose jobs post too many
// bodies (or a compromised container of it) is refused alone, never the job logins of the
// others.
const jobNonceClusterMax = 8192

// legacyJobLoginShown is how long WARN0276 stays open after the last legacy body of a server.
const legacyJobLoginShown = 10 * time.Minute

// jobNonce is a nonce seen, its cluster and when it can be forgotten.
type jobNonce struct {
	key     string
	cluster string
	until   time.Time
}

// jobNonceExpiry orders the nonces by their end (container/heap), the first to forget first.
type jobNonceExpiry []jobNonce

func (h jobNonceExpiry) Len() int            { return len(h) }
func (h jobNonceExpiry) Less(i, j int) bool  { return h[i].until.Before(h[j].until) }
func (h jobNonceExpiry) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *jobNonceExpiry) Push(x interface{}) { *h = append(*h, x.(jobNonce)) }
func (h *jobNonceExpiry) Pop() interface{} {
	old := *h
	n := old[len(old)-1]
	*h = old[:len(old)-1]
	return n
}

// jobNonces are the nonces of the one-time bodies and stream headers seen, per cluster, with
// when they can be forgotten: expired ones leave from the front of expiry, so a request never
// scans the store.
var jobNonces = struct {
	sync.Mutex
	seen       map[string]time.Time
	expiry     jobNonceExpiry
	perCluster map[string]int
}{seen: make(map[string]time.Time), perCluster: make(map[string]int)}

// errJobNoncesFull: more bodies within the window than the store (or the cluster's share)
// holds, refused until the oldest expire (never an unbounded store, F4).
var errJobNoncesFull = fmt.Errorf("too many job bodies in the replay window")

// useJobNonce records the nonce of cluster until keep, false when it was already seen.
func useJobNonce(cluster string, nonce string, now, keep time.Time) (bool, error) {
	jobNonces.Lock()
	defer jobNonces.Unlock()
	for len(jobNonces.expiry) > 0 && !now.Before(jobNonces.expiry[0].until) {
		e := heap.Pop(&jobNonces.expiry).(jobNonce)
		delete(jobNonces.seen, e.key)
		if jobNonces.perCluster[e.cluster]--; jobNonces.perCluster[e.cluster] <= 0 {
			delete(jobNonces.perCluster, e.cluster)
		}
	}
	key := cluster + "/" + nonce
	if _, ok := jobNonces.seen[key]; ok {
		return false, nil
	}
	if len(jobNonces.seen) >= jobNonceMax || jobNonces.perCluster[cluster] >= jobNonceClusterMax {
		return false, errJobNoncesFull
	}
	jobNonces.seen[key] = keep
	jobNonces.perCluster[cluster]++
	heap.Push(&jobNonces.expiry, jobNonce{key: key, cluster: cluster, until: keep})
	return true, nil
}

// GetJobsKey is the key a cluster's database jobs encrypt their job bodies with:
// hex(HMAC-SHA256(SecretKey, "jobs:"+cluster)), derived from replication-manager's persistent
// key (monitoring-key-path), never stored, never a database password. Per cluster, unlike the
// `system` key (GetSystemAPIKey): a database container of one cluster cannot write the job
// bodies of another's servers on a shared replication-manager. Delivered to the jobs container
// in its shm secret volume (OpenSVC, /credentials/JOBS_API_KEY) or in the ssh environment
// (on-premise, GetSshEnv); it changes when the key rotates. Empty without a persistent key:
// the jobs then use the database password.
func (cluster *Cluster) GetJobsKey() string {
	key := cluster.Conf.SecretKey
	if len(key) == 0 {
		return ""
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte("jobs:" + cluster.Name))
	return hex.EncodeToString(mac.Sum(nil))
}

// jobsPrincipalID names the jobs token principal of a cluster: the identity a secret-login
// token carries (token:jobs:system). Not an API token id (those are random hex): never stored.
const jobsPrincipalID = "jobs"

// jobsGrants are the only grants a jobs token carries, what the jobs script calls: the job
// dispatch endpoints (needs, write-log, receive-task, job-state, jobs check and upgrade) and its
// config fetch (config-receiver, config-dummy-sender: their own rules, db-jobs), plus the usage
// push (dbu: cluster-resource-sensor). Never db-config-flag (the config of other servers,
// dynamic config, tags, secret stores), backups, restores, maintenance, topology or settings.
// The token names its server too (jobsTokenServer): it acts on that server's endpoints only.
var jobsGrants = []string{config.GrantDBJobs, config.GrantClusterResourceSensor}

// sensorPrincipalID names the sensor token principal of a cluster: what a password login of
// the `system` account returns (token:sensor:system), for the compute sensor of the proxies
// and apps (share/scripts/app_job.sh). Its key is repman-wide (GetSystemAPIKey): it pushes
// usage and nothing else, and its token is short (secret-login's lifetime).
const sensorPrincipalID = "sensor"

// sensorGrants are the grants of a sensor token: the usage push only.
var sensorGrants = []string{config.GrantClusterResourceSensor}

// SystemServiceGrants are the grants of the `system` service account, exactly: what the
// machine callers do with it and nothing more, the jobs grants (sensors, get the tasks to do and
// report their results, get the config). Never db or proxy actions, never tokens.
func SystemServiceGrants() string {
	return strings.Join(jobsGrants, " ")
}

// systemServiceGrantMap is SystemServiceGrants expanded over the cluster's grant catalogue.
func (cluster *Cluster) systemServiceGrantMap() map[string]bool {
	u := APIUser{Grants: map[string]bool{}}
	cluster.SetUserGrants(&u, SystemServiceGrants())
	return u.Grants
}

// hasSystemServiceGrants: u holds exactly the system grants, no more, no fewer.
func (cluster *Cluster) hasSystemServiceGrants(u APIUser) bool {
	want := cluster.systemServiceGrantMap()
	for g, v := range u.Grants {
		if v != want[g] {
			return false
		}
	}
	for g, v := range want {
		if v && !u.Grants[g] {
			return false
		}
	}
	return true
}

// EnsureJobsPrincipal registers the cluster's service token principals: the jobs one (the
// jobs grants) and the sensor one (the usage push), on this cluster only, intersected with
// the `system` account's at each check (GetACLUser). Registered at cluster start, at each
// secret-login and at each `system` login, so a restart or a reload never leaves a valid
// token without its principal. Two per cluster: bounded.
func (cluster *Cluster) EnsureJobsPrincipal() {
	for id, list := range map[string][]string{jobsPrincipalID: jobsGrants, sensorPrincipalID: sensorGrants} {
		grants := make(map[string]bool, len(list))
		for _, g := range list {
			grants[g] = true
		}
		cluster.SetTokenPrincipal(TokenPrincipal{ID: id, User: "system", Grants: grants, Clusters: []string{cluster.Name}})
	}
}

// JobsPrincipalName is the identity a secret-login token names: token:jobs:system.
func (cluster *Cluster) JobsPrincipalName() string {
	return TokenPrincipalName(jobsPrincipalID, "system")
}

// SensorPrincipalName is the identity a `system` login's token names: token:sensor:system.
func SensorPrincipalName() string {
	return TokenPrincipalName(sensorPrincipalID, "system")
}

// IsJobsPrincipal: name is the jobs token principal, whose token is bound to one server.
func IsJobsPrincipal(name string) bool {
	id, user, ok := ParseTokenPrincipal(name)
	return ok && id == jobsPrincipalID && user == "system"
}

// JobsTokenServerInScope: a jobs token minted for server (its URL, the claim
// secretLoginHandler sets) acts on that server's endpoints only, /servers/<name>[/<port>]/...
// naming it (namedBy, or its name for the routes without a port). Any other URL, another
// server's or a cluster one, is refused.
func (cluster *Cluster) JobsTokenServerInScope(server string, serverName string, serverPort string) bool {
	if server == "" || serverName == "" {
		return false
	}
	var node *ServerMonitor
	if serverPort == "" {
		node = cluster.GetServerFromName(serverName)
	} else {
		node = cluster.GetServerFromURL(serverName + ":" + serverPort)
	}
	return node != nil && node.URL == server
}

// secretLoginWindow is the accepted distance to a body's timestamp.
func (cluster *Cluster) secretLoginWindow() time.Duration {
	if cluster.Conf.APISecretLoginWindow <= 0 {
		return 300 * time.Second
	}
	return time.Duration(cluster.Conf.APISecretLoginWindow) * time.Second
}

// OpenJobBody decrypts a job body posted for this server and returns its clear JSON, the key it
// was encrypted with (JobKeyJobs, JobKeyDB, JobKeyLegacy), and the HTTP code of a refusal.
//   - jobs key: the clear object must name this server ("server" = its URL), the key being
//     shared by the cluster's servers;
//   - one-time (jobs or db key): refused outside the window of its timestamp or with a nonce
//     already seen (401);
//   - legacy: accepted only when legacyAllowed (the jobs check and upgrade endpoints) and until
//     a one-time body was seen from this server; noted for WARN0276.
func (server *ServerMonitor) OpenJobBody(raw []byte, now time.Time, legacyAllowed bool) ([]byte, string, int, error) {
	cluster := server.ClusterGroup
	var b jobBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, "", 400, fmt.Errorf("Decode body: %s", err.Error())
	}
	mode := JobKeyLegacy
	key := crypto.GetSHA256Hash(server.Pass)
	iv := crypto.GetMD5Hash(server.Pass)
	if b.IV != "" {
		if v, err := hex.DecodeString(b.IV); err != nil || len(v) != 16 {
			return nil, "", 400, fmt.Errorf("Decode body: iv is not 16 bytes of hex")
		}
		iv = strings.ToLower(b.IV)
		mode = JobKeyDB
		if b.Key == JobKeyJobs {
			jobsKey := cluster.GetJobsKey()
			if jobsKey == "" {
				return nil, JobKeyJobs, 401, fmt.Errorf("Job body with the jobs key, but no persistent key to derive it (monitoring-key-path)")
			}
			key = crypto.GetSHA256Hash(jobsKey)
			mode = JobKeyJobs
		}
	} else if auth, _ := server.jobStream.Load().(jobStreamAuth); auth.Key != "" {
		// its script signs since this start: a legacy body now is a captured one replayed
		return nil, JobKeyLegacy, 401, fmt.Errorf("Legacy job body refused: the jobs script of this server posts one-time bodies (replay)")
	}
	data, err := server.DecryptAES256(b.Data, key, iv)
	if err != nil {
		return nil, mode, 500, fmt.Errorf("Error decrypting data : %s", err.Error())
	}
	if mode == JobKeyLegacy {
		// noted once it decrypts: an older script (or its body replayed), never a stranger
		atomic.StoreInt64(&server.legacyJobLoginAt, now.Unix())
		// a PostgreSQL sidecar has no script to fetch: its client comes from its init
		// container, at its start; refused, its backups would stop until then (F12), so it is
		// accepted, under WARN0278, until its first one-time body
		if !legacyAllowed && !server.IsPostgreSQLHost() {
			return nil, JobKeyLegacy, 401, fmt.Errorf("Legacy job body refused: an older jobs script may only fetch the new one (jobs upgrade)")
		}
		return data, mode, 200, nil
	}
	var fresh struct {
		Ts     int64  `json:"ts"`
		Nonce  string `json:"nonce"`
		Server string `json:"server"`
	}
	// refused once decrypted: the script of this server (the key is its own), whose jobs stop
	// until the cause is fixed, noted for ERR00120; a body that does not decrypt is not noted,
	// anyone can post one
	refused := func(code int, err error) ([]byte, string, int, error) {
		server.jobLoginRefused.Store(jobStreamRefusal{At: now, Reason: err.Error()})
		return nil, mode, code, err
	}
	if err := json.Unmarshal(jobJSONObject(data), &fresh); err != nil || fresh.Ts == 0 || len(fresh.Nonce) < 16 {
		return refused(401, fmt.Errorf("One-time job body without timestamp or nonce"))
	}
	if mode == JobKeyJobs && !server.namedBy(fresh.Server) {
		return refused(401, fmt.Errorf("Job body for %q posted for %s", fresh.Server, server.URL))
	}
	window := cluster.secretLoginWindow()
	ts := time.Unix(fresh.Ts, 0)
	if ts.Before(now.Add(-window)) || ts.After(now.Add(window)) {
		return refused(401, fmt.Errorf("Job body outside the %s window: the clocks of the database host and replication-manager differ by %s (or a replay)", window, now.Sub(ts).Round(time.Second)))
	}
	ok, err := useJobNonce(cluster.Name, fresh.Nonce, now, now.Add(2*window))
	if err != nil {
		return refused(503, err)
	}
	if !ok {
		// a replay of a captured body, not the script's fault: refused, not noted
		return nil, mode, 401, fmt.Errorf("Job body already used (replay)")
	}
	server.jobStream.Store(jobStreamAuth{Key: mode, Name: fresh.Server})
	return data, mode, 200, nil
}

// namedBy: name is how a job body names this server, its URL or the host:port and ip:port its
// requests are routed by (GetServerFromURL).
func (server *ServerMonitor) namedBy(name string) bool {
	return name != "" && (name == server.URL || name == server.Host+":"+server.Port || (server.IP != "" && name == server.IP+":"+server.Port))
}

// jobJSONObject is the JSON object inside a decrypted body: from its first '{' to its last
// '}' (the script may pad or prefix it), the input when there is none.
func jobJSONObject(data []byte) []byte {
	s := string(data)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j < i {
		return data
	}
	return []byte(s[i : j+1])
}

// CheckSecretLoginLegacy runs every tick: WARN0276, a security state (T3), naming the servers
// whose jobs script used the legacy job body within legacyJobLoginShown, until their script is
// upgraded; WARN0278 for the PostgreSQL ones, whose older client only a sidecar restart
// replaces.
func (cluster *Cluster) CheckSecretLoginLegacy() {
	now := time.Now()
	var urls, pg []string
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		at := atomic.LoadInt64(&s.legacyJobLoginAt)
		if at == 0 || now.Sub(time.Unix(at, 0)) > legacyJobLoginShown {
			continue
		}
		if s.IsPostgreSQLHost() {
			pg = append(pg, s.URL)
		} else {
			urls = append(urls, s.URL)
		}
	}
	if len(urls) > 0 {
		cluster.SecurityStateMachine.AddState("WARN0276", state.State{ErrType: "WARNING", ErrKey: "WARN0276", ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0276"], strings.Join(urls, ", "))})
	}
	if len(pg) > 0 {
		cluster.SecurityStateMachine.AddState("WARN0278", state.State{ErrType: "WARNING", ErrKey: "WARN0278", ErrFrom: "SECURITY",
			ErrDesc: fmt.Sprintf(clusterError["WARN0278"], strings.Join(pg, ", "))})
	}
}

// The job stream header (jobapi.StreamHeader): the first line of every stream between
// replication-manager and a database's jobs, so each side takes a stream from the other only,
// not from whoever connects to the port first. Signed with the key of the server's last
// accepted job body (the cluster's jobs key, else the server's password), for the name the
// script gives its server. A server whose script posts legacy bodies (older, it neither sends
// nor reads a header) gets none and is not asked for one.

// jobStreamAuth is how a server's jobs script signs and checks its streams, learnt from its
// one-time job bodies: their key (JobKeyJobs or JobKeyDB) and the name it gives its server.
// Key empty: no one-time body since start, the script is an older one.
type jobStreamAuth struct {
	Key  string
	Name string
}

// jobStreamSecret is the secret and name the server's streams are signed with, "" when its
// script does not sign.
func (server *ServerMonitor) jobStreamSecret() (string, string, error) {
	auth, _ := server.jobStream.Load().(jobStreamAuth)
	var secret string
	switch auth.Key {
	case JobKeyJobs:
		secret = server.ClusterGroup.GetJobsKey()
	case JobKeyDB:
		secret = server.Pass
	default:
		return "", "", nil
	}
	if secret == "" || auth.Name == "" {
		return "", "", fmt.Errorf("No secret to sign the streams of %s", server.URL)
	}
	return secret, auth.Name, nil
}

// JobStreamHeader is the header to send first on a stream to the server's jobs listener, nil
// when its script does not check one.
func (server *ServerMonitor) JobStreamHeader(now time.Time) ([]byte, error) {
	secret, name, err := server.jobStreamSecret()
	if err != nil || secret == "" {
		return nil, err
	}
	return jobapi.StreamHeader(secret, jobapi.StreamToDB, name, now)
}

// writeJobStreamHeader writes the header, when the server's script checks one, first on a
// stream to its jobs listener.
func (server *ServerMonitor) writeJobStreamHeader(w io.Writer) error {
	h, err := server.JobStreamHeader(time.Now())
	if err != nil || h == nil {
		return err
	}
	_, err = w.Write(h)
	return err
}

// CheckJobStream reads and checks the header of a stream the server's jobs send to a
// replication-manager receiver: signed for this server, within the window, its nonce unseen
// (the job body store). Nothing is read when the server's script does not sign (an older
// one). A refused connection is closed by the caller, which waits for the next.
func (server *ServerMonitor) CheckJobStream(conn net.Conn) error {
	secret, name, err := server.jobStreamSecret()
	if err != nil || secret == "" {
		return err
	}
	// a connection that sends nothing is closed (acceptJobStream checks each on its own)
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
	line, err := jobapi.ReadStreamHeader(conn)
	if err != nil {
		return err
	}
	now := time.Now()
	window := server.ClusterGroup.secretLoginWindow()
	nonce, err := jobapi.CheckStreamHeader(line, secret, jobapi.StreamToMonitor, name, now, window)
	if err != nil {
		return err
	}
	ok, err := useJobNonce(server.ClusterGroup.Name, "stream:"+nonce, now, now.Add(2*window))
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("stream header already used (replay)")
	}
	return nil
}

// jobStreamRefusal is the last refused stream of a server: when and why.
type jobStreamRefusal struct {
	At     time.Time
	Reason string
}

// jobStreamChecking bounds the connections of one receiver whose header is being read (T18): a
// connection past it is closed at once, noted for WARN0277.
const jobStreamChecking = 8

// acceptJobStream accepts the stream of the server's jobs on the receiver's listener. Each
// connection has its header checked on its own (CheckJobStream), so a connection that sends
// nothing holds itself only, never the real transfer; the first one checked is the stream, a
// refused one is closed and noted for WARN0277. The accepting ends with the listener (closed by
// the receiver's cleanup, or its deadline); a connection checked after the stream was taken is
// closed.
func (sst *SST) acceptJobStream(server *ServerMonitor, task string) (net.Conn, error) {
	if server == nil {
		return sst.listener.Accept()
	}
	checked := make(chan net.Conn)
	failed := make(chan error, 1)
	taken := make(chan struct{})
	defer close(taken)
	refuse := func(conn net.Conn, err error) {
		server.jobStreamRefused.Store(jobStreamRefusal{At: time.Now(), Reason: fmt.Sprintf("%s from %s: %s", task, conn.RemoteAddr(), err)})
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Stream for task %s of %s refused from %s: %s", task, server.URL, conn.RemoteAddr(), err)
		conn.Close()
	}
	go func() {
		var checking int32
		for {
			conn, err := sst.listener.Accept()
			if err != nil {
				failed <- err
				return
			}
			if atomic.AddInt32(&checking, 1) > jobStreamChecking {
				atomic.AddInt32(&checking, -1)
				refuse(conn, fmt.Errorf("more than %d connections waiting for their header", jobStreamChecking))
				continue
			}
			go func(conn net.Conn) {
				defer atomic.AddInt32(&checking, -1)
				if err := server.CheckJobStream(conn); err != nil {
					refuse(conn, err)
					return
				}
				select {
				case checked <- conn:
				case <-taken:
					conn.Close()
				}
			}(conn)
		}
	}()
	select {
	case conn := <-checked:
		return conn, nil
	case err := <-failed:
		return nil, err
	}
}

// CheckJobLoginRefused runs every tick: ERR00120, a security state (an ERROR, T3: the jobs of these servers, backups included, cannot run), naming the servers whose
// one-time job bodies were refused once decrypted within legacyJobLoginShown, and why: their
// jobs (backups included) stop until the cause (a clock, the script's server name) is fixed.
func (cluster *Cluster) CheckJobLoginRefused() {
	now := time.Now()
	var refused []string
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		if r, ok := s.jobLoginRefused.Load().(jobStreamRefusal); ok && now.Sub(r.At) <= legacyJobLoginShown {
			refused = append(refused, s.URL+" ("+r.Reason+")")
		}
	}
	if len(refused) == 0 {
		return
	}
	cluster.SecurityStateMachine.AddState("ERR00120", state.State{ErrType: "ERROR", ErrKey: "ERR00120", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["ERR00120"], strings.Join(refused, ", "))})
}

// CheckJobStreamRefused runs every tick: WARN0277, a security state, naming the servers whose
// receivers refused a stream within legacyJobLoginShown, and why.
func (cluster *Cluster) CheckJobStreamRefused() {
	now := time.Now()
	var refused []string
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		if r, ok := s.jobStreamRefused.Load().(jobStreamRefusal); ok && now.Sub(r.At) <= legacyJobLoginShown {
			refused = append(refused, s.URL+" ("+r.Reason+")")
		}
	}
	if len(refused) == 0 {
		return
	}
	cluster.SecurityStateMachine.AddState("WARN0277", state.State{ErrType: "WARNING", ErrKey: "WARN0277", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0277"], strings.Join(refused, ", "))})
}
