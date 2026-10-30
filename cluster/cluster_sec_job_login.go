// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Author: Stephane Varoqui  <svaroqui@gmail.com>
// License: GNU General Public License, version 3. Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this directory for the integrity of this file.

package cluster

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/signal18/replication-manager/utils/crypto"
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
// the same body at every run, accepted any number of times. Accepted while
// api-secret-login-legacy is on, so a server running an older script can still log in and
// fetch the new one (jobs-upgrade); each use opens WARN0272 on that server.

// jobBody is the posted JSON.
type jobBody struct {
	Data string `json:"data"`
	IV   string `json:"iv"`
}

// jobNonceMax bounds the nonces kept (F4): a nonce is kept twice the window, a database server
// posts a few bodies a minute.
const jobNonceMax = 65536

// legacyJobLoginShown is how long WARN0272 stays open after the last legacy body of a server.
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

// secretLoginWindow is the accepted distance to a body's timestamp.
func (cluster *Cluster) secretLoginWindow() time.Duration {
	if cluster.Conf.APISecretLoginWindow <= 0 {
		return 300 * time.Second
	}
	return time.Duration(cluster.Conf.APISecretLoginWindow) * time.Second
}

// OpenJobBody decrypts a job body posted for this server and returns its clear JSON, whether it
// was a one-time body, and the HTTP code of a refusal. A one-time body outside the window or
// with a nonce already seen is refused (401); a legacy body is refused when
// api-secret-login-legacy is off, else accepted and noted for WARN0272.
func (server *ServerMonitor) OpenJobBody(raw []byte, now time.Time) ([]byte, bool, int, error) {
	cluster := server.ClusterGroup
	var b jobBody
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, false, 400, fmt.Errorf("Decode body: %s", err.Error())
	}
	key := crypto.GetSHA256Hash(server.Pass)
	oneTime := b.IV != ""
	iv := crypto.GetMD5Hash(server.Pass)
	if oneTime {
		if v, err := hex.DecodeString(b.IV); err != nil || len(v) != 16 {
			return nil, true, 400, fmt.Errorf("Decode body: iv is not 16 bytes of hex")
		}
		iv = strings.ToLower(b.IV)
	} else if !cluster.Conf.APISecretLoginLegacy {
		return nil, false, 401, fmt.Errorf("Legacy job body refused (api-secret-login-legacy off): upgrade the jobs script")
	}
	data, err := server.DecryptAES256(b.Data, key, iv)
	if err != nil {
		return nil, oneTime, 500, fmt.Errorf("Error decrypting data : %s", err.Error())
	}
	if !oneTime {
		atomic.StoreInt64(&server.legacyJobLoginAt, now.Unix())
		return data, false, 200, nil
	}
	var fresh struct {
		Ts    int64  `json:"ts"`
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal(jobJSONObject(data), &fresh); err != nil || fresh.Ts == 0 || len(fresh.Nonce) < 16 {
		return nil, true, 401, fmt.Errorf("One-time job body without timestamp or nonce")
	}
	window := cluster.secretLoginWindow()
	ts := time.Unix(fresh.Ts, 0)
	if ts.Before(now.Add(-window)) || ts.After(now.Add(window)) {
		return nil, true, 401, fmt.Errorf("Job body outside the %s window (clock skew or replay)", window)
	}
	ok, err := useJobNonce(fresh.Nonce, now, now.Add(2*window))
	if err != nil {
		return nil, true, 503, err
	}
	if !ok {
		return nil, true, 401, fmt.Errorf("Job body already used (replay)")
	}
	return data, true, 200, nil
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

// CheckSecretLoginLegacy runs every tick: WARN0272, a security state (T3), naming the servers
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
	cluster.SecurityStateMachine.AddState("WARN0272", state.State{ErrType: "WARNING", ErrKey: "WARN0272", ErrFrom: "SECURITY",
		ErrDesc: fmt.Sprintf(clusterError["WARN0272"], strings.Join(urls, ", "))})
}
