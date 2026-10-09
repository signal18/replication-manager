// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.
// Redistribution/Reuse of this code is permitted under the GNU v3 license, as
// an additional term, ALL code must carry the original Author(s) credit in comment form.
// See LICENSE in this source distribution for more information.

package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
	"github.com/signal18/replication-manager/utils/state"
)

// errPeerSchemeMismatch marks a peer heartbeat sent over the wrong scheme:
// plain http to an https-only API, or https to a plain http API.
var errPeerSchemeMismatch = errors.New("scheme mismatch")

// peerHeartbeatTransport is the transport of the peer heartbeat client; nil
// is the default one. Tests set it to trust their TLS server.
var peerHeartbeatTransport http.RoundTripper

// fetchPeerHeartbeat calls a peer's /api/heartbeat and returns its answer, or
// an error that says why it is not one (unreachable, scheme mismatch, HTTP
// status, not a heartbeat).
func fetchPeerHeartbeat(url string, timeout time.Duration) (Heartbeat, error) {
	var h Heartbeat
	client := &http.Client{Timeout: timeout, Transport: peerHeartbeatTransport}
	resp, err := client.Get(url)
	if err != nil {
		if isSchemeMismatchError(err) {
			return h, fmt.Errorf("%w: %v", errPeerSchemeMismatch, err)
		}
		return h, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return h, fmt.Errorf("reading the answer: %w", err)
	}
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "HTTP request to an HTTPS server") {
		return h, fmt.Errorf("%w: the peer API serves https only", errPeerSchemeMismatch)
	}
	if resp.StatusCode == http.StatusNotFound {
		return h, fmt.Errorf("HTTP 404: /api/heartbeat is served on the peer's http-port (10001 by default), not on its api-port")
	}
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(body, &h); err != nil {
		return h, fmt.Errorf("not a heartbeat answer: %w", err)
	}
	return h, nil
}

// isSchemeMismatchError reports a transport error caused by talking https to
// a plain http server (Go names it) or TLS to a non-TLS port. It matches the
// error text of Go's net/http and crypto/tls (and fetchPeerHeartbeat matches the
// 400 body of net/http's TLS server): a Go upgrade that rewords them silently
// disables the fallback, which the tests of server_heartbeat_peer_test.go catch.
func isSchemeMismatchError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "server gave HTTP response to HTTPS client") ||
		strings.Contains(msg, "first record does not look like a TLS handshake")
}

func otherHeartbeatScheme(scheme string) string {
	if scheme == "https://" {
		return "http://"
	}
	return "https://"
}

// peerHeartbeatScheme is the scheme to call a peer written without one: the
// one that last answered, http until then.
func (repman *ReplicationManager) peerHeartbeatScheme(peer string) string {
	if v, ok := repman.arbPeerScheme.Load(peer); ok {
		return v.(string)
	}
	return "http://"
}

// raisePeerHeartbeatStates turns the last heartbeat round into states, so the
// reason of a split brain is on the dashboard and not only in a debug log:
// GWARN018 why a peer heartbeat fails, GWARN019 a peer that answers only on
// the other scheme than the configured value implies, GWARN020 a peer with
// our own arbitration-external-unique-id.
func (repman *ReplicationManager) raisePeerHeartbeatStates(arbPeerList []string) {
	repman.Lock()
	failures := make(map[string]string, len(repman.peerHeartbeatFailures))
	for _, peer := range arbPeerList {
		if reason, ok := repman.peerHeartbeatFailures[peer]; ok {
			failures[peer] = reason
		}
	}
	sameUID := repman.peerHeartbeatSameUID
	repman.Unlock()

	// One GWARN018 for every failing peer: a state key holds one description,
	// so the reasons of several peers are joined instead of overwriting each other.
	var reasons []string
	for _, peer := range arbPeerList {
		if reason, ok := failures[peer]; ok {
			reasons = append(reasons, reason)
		}
	}
	if len(reasons) > 0 {
		repman.SetState("GWARN018", state.State{ErrType: "WARNING", ErrKey: "GWARN018", ErrDesc: fmt.Sprintf(config.GlobalError["GWARN018"], strings.Join(reasons, "; ")), ErrFrom: "ARB"})
	}
	for _, peer := range arbPeerList {
		if _, ok := failures[peer]; ok {
			continue
		}
		if strings.HasPrefix(peer, "https://") || strings.HasPrefix(peer, "http://") {
			continue
		}
		if scheme := repman.peerHeartbeatScheme(peer); scheme != "http://" {
			repman.SetState("GWARN019", state.State{ErrType: "WARNING", ErrKey: "GWARN019", ErrDesc: fmt.Sprintf(config.GlobalError["GWARN019"], peer, scheme, scheme+peer), ErrFrom: "ARB"})
		}
	}
	if sameUID {
		repman.SetState("GWARN020", state.State{ErrType: "WARNING", ErrKey: "GWARN020", ErrDesc: fmt.Sprintf(config.GlobalError["GWARN020"], repman.Conf.ArbitrationSasUniqueId), ErrFrom: "ARB"})
	}
}
