// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integrity of this file.

package cluster

import (
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
// jobs script (the jobs check and jobs upgrade endpoints), and only until replication-manager
// has sent it the new script since its start (jobsScriptSent) or seen a one-time body from it:
// everything else, secret-login included, refuses it. Each use opens WARN0274 on that server.
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

// legacyJobLoginShown is how long WARN0274 stays open after the last legacy body of a server.
const legacyJobLoginShown = 10 * time.Minute

// jobNonces are the nonces of the one-time bodies seen, with when they can be forgotten. One
// store for the process: a nonce is random, a replay to another cluster fails its key anyway.
var jobNonces = struct {
	sync.Mutex
	seen map[string]time.Time
}{seen: make(map[string]time.Time)}

// errJobNoncesFull: more bodies within the window than the store holds, refused until the
// oldest expire (never an unbounded store, F4).
var errJobNoncesFull = fmt.Errorf("too many job bodies in the replay window")

// useJobNonce records nonce until keep, false when it was already seen.
func useJobNonce(nonce string, now, keep time.Time) (bool, error) {
	jobNonces.Lock()
	defer jobNonces.Unlock()
	if until, ok := jobNonces.seen[nonce]; ok && now.Before(until) {
		return false, nil
	}
	if len(jobNonces.seen) >= jobNonceMax {
		for n, until := range jobNonces.seen {
			if !now.Before(until) {
				delete(jobNonces.seen, n)
			}
		}
		if len(jobNonces.seen) >= jobNonceMax {
			return false, errJobNoncesFull
		}
	}
	jobNonces.seen[nonce] = keep
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
// dispatch endpoints (needs, write-log, receive-task, job-state, jobs check and upgrade:
// db-jobs), its config fetch (config-receiver, config-dummy-sender: db-config-flag) and the
// usage push (dbu: cluster-resource-sensor). Never backups, restores, maintenance, topology or
// settings, whatever the `system` account holds.
var jobsGrants = []string{config.GrantDBJobs, config.GrantDBConfigFlag, config.GrantClusterResourceSensor}

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

// EnsureJobsPrincipal registers the cluster's jobs token principal: the jobs grants, on this
// cluster only, intersected with the `system` account's at each check (GetACLUser). Registered
// at cluster start and at each secret-login, so a restart or a reload never leaves a valid
// token without its principal. One per cluster: bounded.
func (cluster *Cluster) EnsureJobsPrincipal() {
	grants := make(map[string]bool, len(jobsGrants))
	for _, g := range jobsGrants {
		grants[g] = true
	}
	cluster.SetTokenPrincipal(TokenPrincipal{ID: jobsPrincipalID, User: "system", Grants: grants, Clusters: []string{cluster.Name}})
}

// JobsPrincipalName is the identity a secret-login token names: token:jobs:system.
func (cluster *Cluster) JobsPrincipalName() string {
	return TokenPrincipalName(jobsPrincipalID, "system")
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
//   - legacy: accepted only when legacyAllowed (the jobs check and upgrade endpoints) and while
//     the new script was not sent to this server nor a one-time body seen from it; noted for
//     WARN0274.
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
	} else if !legacyAllowed {
		atomic.StoreInt64(&server.legacyJobLoginAt, now.Unix())
		return nil, JobKeyLegacy, 401, fmt.Errorf("Legacy job body refused: an older jobs script may only fetch the new one (jobs upgrade)")
	} else if server.jobsScriptSent.Load() {
		return nil, JobKeyLegacy, 401, fmt.Errorf("Legacy job body refused: the new jobs script was already sent to this server")
	} else if auth, _ := server.jobStream.Load().(jobStreamAuth); auth.Key != "" {
		// its script signs since this start: a legacy body now is a captured one replayed
		return nil, JobKeyLegacy, 401, fmt.Errorf("Legacy job body refused: the jobs script of this server posts one-time bodies (replay)")
	}
	data, err := server.DecryptAES256(b.Data, key, iv)
	if err != nil {
		return nil, mode, 500, fmt.Errorf("Error decrypting data : %s", err.Error())
	}
	if mode == JobKeyLegacy {
		atomic.StoreInt64(&server.legacyJobLoginAt, now.Unix())
		return data, mode, 200, nil
	}
	var fresh struct {
		Ts     int64  `json:"ts"`
		Nonce  string `json:"nonce"`
		Server string `json:"server"`
	}
	if err := json.Unmarshal(jobJSONObject(data), &fresh); err != nil || fresh.Ts == 0 || len(fresh.Nonce) < 16 {
		return nil, mode, 401, fmt.Errorf("One-time job body without timestamp or nonce")
	}
	if mode == JobKeyJobs && !server.namedBy(fresh.Server) {
		return nil, mode, 401, fmt.Errorf("Job body for %q posted for %s", fresh.Server, server.URL)
	}
	window := cluster.secretLoginWindow()
	ts := time.Unix(fresh.Ts, 0)
	if ts.Before(now.Add(-window)) || ts.After(now.Add(window)) {
		return nil, mode, 401, fmt.Errorf("Job body outside the %s window (clock skew or replay)", window)
	}
	ok, err := useJobNonce(fresh.Nonce, now, now.Add(2*window))
	if err != nil {
		return nil, mode, 503, err
	}
	if !ok {
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

// CheckSecretLoginLegacy runs every tick: WARN0274, a security state (T3), naming the servers
// whose jobs script used the legacy job body within legacyJobLoginShown, until their script is
// upgraded.
func (cluster *Cluster) CheckSecretLoginLegacy() {
	now := time.Now()
	var urls []string
	for _, s := range cluster.Servers {
		if s == nil {
			continue
		}
		at := atomic.LoadInt64(&s.legacyJobLoginAt)
		if at != 0 && now.Sub(time.Unix(at, 0)) <= legacyJobLoginShown {
			urls = append(urls, s.URL)
		}
	}
	if len(urls) == 0 {
		return
	}
	cluster.SecurityStateMachine.AddState("WARN0274", state.State{ErrType: "WARNING", ErrKey: "WARN0274", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0274"], strings.Join(urls, ", "))})
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
	// a connection that sends nothing must not hold the receiver
	conn.SetReadDeadline(time.Now().Add(30 * time.Second))
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
	ok, err := useJobNonce("stream:"+nonce, now, now.Add(2*window))
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

// acceptJobStream accepts the stream of the server's jobs on the receiver's listener: a
// connection whose header CheckJobStream refuses is closed, noted for WARN0275, and the next
// one awaited until the listener's deadline, so whoever connects first can neither write the
// backup or log nor block the real transfer.
func (sst *SST) acceptJobStream(server *ServerMonitor, task string) (net.Conn, error) {
	for {
		conn, err := sst.listener.Accept()
		if err != nil || server == nil {
			return conn, err
		}
		if err = server.CheckJobStream(conn); err == nil {
			return conn, nil
		}
		server.jobStreamRefused.Store(jobStreamRefusal{At: time.Now(), Reason: fmt.Sprintf("%s from %s: %s", task, conn.RemoteAddr(), err)})
		sst.cluster.LogModulePrintf(sst.cluster.Conf.Verbose, config.ConstLogModSST, config.LvlErr, "Stream for task %s of %s refused from %s: %s", task, server.URL, conn.RemoteAddr(), err)
		conn.Close()
	}
}

// CheckJobStreamRefused runs every tick: WARN0275, a security state, naming the servers whose
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
	cluster.SecurityStateMachine.AddState("WARN0275", state.State{ErrType: "WARNING", ErrKey: "WARN0275", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0275"], strings.Join(refused, ", "))})
}
