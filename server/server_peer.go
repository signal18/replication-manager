package server

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
	"github.com/signal18/replication-manager/peer"
	log "github.com/sirupsen/logrus"
)

func (repman *ReplicationManager) GetPeerCluster(peerURL, clustername string) (*peer.PeerCluster, bool) {
	return repman.PeerManager.GetCluster(peer.GetHashID(peerURL, clustername))
}

func (repman *ReplicationManager) PeerLogin(parsedPeerURL *url.URL, user userCredentials) (int, []byte) {

	// Marshal the modified JSON back to a byte slice
	loginBody, err := json.Marshal(user)
	if err != nil {
		return http.StatusInternalServerError, []byte("Failed to marshal modified JSON")
	}

	rBody := io.NopCloser(bytes.NewBuffer(loginBody))

	// Create a new request to forward to Peer
	req, err := http.NewRequest("POST", parsedPeerURL.String(), rBody)
	if err != nil {
		return http.StatusInternalServerError, []byte("Failed to create request: " + err.Error())
	}

	req.Close = true

	// Send the request to GoApp 2
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return http.StatusBadGateway, []byte("Error forwarding request: " + err.Error())
	}
	defer resp.Body.Close()

	return repman.PeerResponseHandler(resp)
}

func (repman *ReplicationManager) PeerRequestForwarder(parsedPeerURL *url.URL, r *http.Request) (int, []byte) {

	// Log the forwarding request
	log.Printf("Forwarding request to: %s", parsedPeerURL.String())

	// Create a new request to forward to Peer
	req, err := http.NewRequest(r.Method, parsedPeerURL.String(), r.Body)
	if err != nil {
		return http.StatusInternalServerError, []byte("Failed to create request: " + err.Error())
	}

	// Copy Content-Type and other headers from the original request
	req.Header = r.Header.Clone()
	req.Close = true

	// Send the request to GoApp 2
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return http.StatusBadGateway, []byte("Error forwarding request: " + err.Error())
	}
	defer resp.Body.Close()

	return repman.PeerResponseHandler(resp)
}

func (repman *ReplicationManager) PeerResponseHandler(resp *http.Response) (int, []byte) {
	var body []byte
	var err error
	switch resp.Header.Get("Content-Encoding") {
	case "zstd":
		// Handle zstd encoding
		decoder, err := zstd.NewReader(resp.Body)
		if err != nil {
			return http.StatusInternalServerError, []byte("Failed to create zstd decoder: " + err.Error())
		}
		defer decoder.Close()
		body, _ = io.ReadAll(decoder)

	case "gzip":
		// Handle gzip encoding
		reader, err := pgzip.NewReader(resp.Body)
		if err != nil {
			return http.StatusInternalServerError, []byte("Failed to create gzip reader: " + err.Error())
		}
		defer reader.Close()
		body, err = io.ReadAll(reader)

	case "deflate":
		// Handle deflate encoding
		reader, err := zlib.NewReader(resp.Body)
		if err != nil {
			return http.StatusInternalServerError, []byte("Failed to create deflate reader: " + err.Error())
		}
		defer reader.Close()
		body, err = io.ReadAll(reader)

	default:
		// Handle uncompressed response
		body, err = io.ReadAll(resp.Body)
	}

	if err != nil {
		return http.StatusInternalServerError, []byte("Failed to read response body: " + err.Error())
	}

	return resp.StatusCode, body
}

func (repman *ReplicationManager) GetLocalHealth() map[string]peer.PeerHealth {
	healths := make(map[string]peer.PeerHealth)
	for _, mycluster := range repman.Clusters {
		healths[mycluster.Name] = mycluster.GetPeerHealth()
	}

	return healths
}

func (repman *ReplicationManager) UpdateLocalPeer() {
	repman.PeerManager.UpdateHealthStatus(repman.GetLocalHealth())
}

// dispatchPeerHealthPoll runs a scoped health poll in a goroutine (callers — the
// reload path included — never block) and returns false if one was already in
// flight. It holds peerHealthBusy for the WHOLE poll, so the flag genuinely reflects
// "a poll is running": the timer raises GWARN013@peerhealth when it finds a poll
// that hasn't finished within a cycle (a slow/stuck poll — the operator signal that
// would have flagged the original incident). This coarse guard is complementary to
// pollPeerHealth's per-peer first-in-wins claim, which handles fine-grained dedup and
// map safety against BatchUpdateClusters. See MARKETPLACE.md §6.
//
// pulling (default) and smart both scope live polling to the connected-users set
// (own registering identity + active-session users) via GetHealthStatusForActiveUsers.
// This is the invariant: a for-sale cluster is polled only once a user here has a
// relationship to it (delegation, or a pending/sponsor sale workflow) — never while
// merely browsing the catalog. A fresh instance with no such relationship polls zero
// sellers.
//
// peering is the only unscoped mode: a deliberate full-mesh that polls every peer
// URL, for-sale included. It ignores the invariant on purpose and must stay opt-in
// (never the default), reserved for legacy/debug full-mesh fleets.
func (repman *ReplicationManager) dispatchPeerHealthPoll() bool {
	if !repman.peerHealthBusy.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer repman.peerHealthBusy.Store(false)
		switch repman.Conf.Cloud18PeerHealthMode {
		case "peering":
			repman.PeerManager.GetAllHealthStatus()
		case "smart", "pulling":
			activeUsers := repman.getActiveSessionUsers()
			repman.PeerManager.GetHealthStatusForActiveUsers(repman.Conf.Cloud18GitUser, activeUsers)
		}
		repman.UpdateLocalPeer()
	}()
	return true
}

// getActiveSessionUsers returns usernames of users with an active session
// (non-empty GitToken from OAuth login). These are the users currently
// connected to the dashboard who need peer health data.
func (repman *ReplicationManager) getActiveSessionUsers() []string {
	seen := make(map[string]bool)
	for _, cl := range repman.Clusters {
		for username, apiuser := range cl.APIUsers {
			if apiuser.GitToken != "" && !seen[username] {
				seen[username] = true
			}
		}
	}
	users := make([]string, 0, len(seen))
	for u := range seen {
		users = append(users, u)
	}
	return users
}
