package peer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/signal18/replication-manager/config"
)

// PeerClient represents the HTTP client for peer communication
type PeerClient struct {
	client  *http.Client
	baseURL string
	headers map[string]string
}

type PeerCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// NewPeerClient initializes a new PeerClient
func NewPeerClient(baseURL string, timeout time.Duration) *PeerClient {
	urlparse, err := url.Parse(baseURL)
	if err != nil {
		panic(fmt.Sprintf("failed to parse URL: %s", err))
	}
	return &PeerClient{
		client: &http.Client{
			Timeout: timeout,
			// Bounded transport so a single unresponsive peer can never
			// accumulate connections. The marketplace fan-out once stranded
			// ~6900 sockets against one dark peer; MaxConnsPerHost is the hard
			// ceiling that makes that impossible regardless of how many polls
			// fire. DialContext/handshake/response deadlines bound a peer that
			// accepts TCP (e.g. behind HAProxy) but never answers.
			// See doc/implementation/peer/MARKETPLACE.md §6.
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				MaxConnsPerHost:       2,
				MaxIdleConns:          10,
				MaxIdleConnsPerHost:   1,
				IdleConnTimeout:       30 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: timeout,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		baseURL: urlparse.String(),
		headers: make(map[string]string),
	}
}

// SetBaseURL allows changing the base URL dynamically
func (pc *PeerClient) SetBaseURL(baseURL string) {
	urlparse, err := url.Parse(baseURL)
	if err != nil {
		panic(fmt.Sprintf("failed to parse URL: %s", err))
	}
	pc.baseURL = urlparse.String()
}

// SetHeader sets a custom header for all requests
func (pc *PeerClient) SetHeader(key, value string) {
	pc.headers[key] = value
}

// DoRequest sends an HTTP request with the specified method, endpoint, and body
func (pc *PeerClient) DoRequest(method, endpoint string, body io.Reader) (int, []byte, error) {
	return pc.doRequest(method, endpoint, body, false, "")
}

// doRequest sends the request with the client's headers. A login (#2327) never carries the
// session token the client may hold (any case of the header name), only the realm token given
// as bearer, and is a JSON post.
func (pc *PeerClient) doRequest(method, endpoint string, body io.Reader, login bool, bearer string) (int, []byte, error) {
	// one slash between the base URL and the endpoint: callers pass "/api/..." and a
	// recent peer does not clean "//api/..." (#1953)
	url := strings.TrimRight(pc.baseURL, "/") + "/" + strings.TrimLeft(endpoint, "/")
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 500, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	for key, value := range pc.headers {
		if login && strings.EqualFold(key, "Authorization") {
			continue
		}
		req.Header.Set(key, value)
	}
	if login {
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
	}
	req.Close = true

	resp, err := pc.client.Do(req)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	var from io.Reader = resp.Body
	if login {
		from = io.LimitReader(resp.Body, 1<<20) // a login answer is a token
	}
	respBody, err := io.ReadAll(from)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, respBody, fmt.Errorf("peer %s status %d: %s", pc.baseURL, resp.StatusCode, string(respBody))
	}

	return resp.StatusCode, respBody, nil
}

// Get sends a GET request
func (pc *PeerClient) Get(endpoint string) (int, []byte, error) {
	return pc.DoRequest(http.MethodGet, endpoint, nil)
}

// Post sends a POST request with a JSON payload
func (pc *PeerClient) Post(endpoint string, payload io.Reader) (int, []byte, error) {
	return pc.DoRequest(http.MethodPost, endpoint, payload)
}

// Put sends a PUT request with a JSON payload
func (pc *PeerClient) Put(endpoint string, payload io.Reader) (int, []byte, error) {
	return pc.DoRequest(http.MethodPut, endpoint, payload)
}

// Delete sends a DELETE request
func (pc *PeerClient) Delete(endpoint string) (int, []byte, error) {
	return pc.DoRequest(http.MethodDelete, endpoint, nil)
}

func (pc *PeerClient) PeerLogin(username, password string) error {
	return pc.PeerLoginWithBearer("", username, password)
}

// PeerLoginWithBearer logs in on the peer with a Cloud18 realm token when one is given (#2327):
// the peer verifies it by itself, whether or not it knows the account's password. The password
// (when one is given) is sent only when there is no token, or when the peer refuses the token
// with a 401 of an older release, which knows only passwords; a peer answering that it refused
// the token (config.ConstCloud18TokenRefused) knows tokens, and the password would only be refused
// there too. Any other answer is returned as is.
func (pc *PeerClient) PeerLoginWithBearer(bearer, username, password string) error {
	status, body, err := 0, []byte(nil), error(nil)
	if bearer != "" {
		status, body, err = pc.doRequest(http.MethodPost, "api/login", bytes.NewReader([]byte("{}")), true, bearer)
		if err != nil && (status != http.StatusUnauthorized || password == "" || strings.Contains(string(body), config.ConstCloud18TokenRefused)) {
			return err
		}
	}
	if bearer == "" || err != nil {
		if password == "" {
			return fmt.Errorf("no credential to log in on %s", pc.baseURL)
		}
		loginBody, merr := json.Marshal(PeerCredential{Username: username, Password: password})
		if merr != nil {
			return merr
		}
		status, body, err = pc.doRequest(http.MethodPost, "api/login", bytes.NewReader(loginBody), true, "")
		if err != nil {
			return err
		}
	}

	if status != http.StatusOK {
		return fmt.Errorf("peer login failed with status %d: %s", status, string(body))
	}

	var AuthToken struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(body, &AuthToken); err != nil {
		return err
	}
	if AuthToken.Token == "" {
		return fmt.Errorf("peer %s gave no session token", pc.baseURL)
	}

	pc.SetHeader("Authorization", "Bearer "+AuthToken.Token)

	return nil
}
