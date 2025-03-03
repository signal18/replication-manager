package peer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
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
	url := pc.baseURL + "/" + endpoint

	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return 500, nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add headers
	for key, value := range pc.headers {
		req.Header.Set(key, value)
	}

	resp, err := pc.client.Do(req)
	if err != nil {
		return http.StatusInternalServerError, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
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

	// Marshal the modified JSON back to a byte slice
	loginBody, err := json.Marshal(PeerCredential{Username: username, Password: password})
	if err != nil {
		return fmt.Errorf("failed to marshal modified JSON: %w", err)
	}
	// Send the request to GoApp 2
	status, body, err := pc.Post("api/login", bytes.NewBuffer(loginBody))
	if err != nil {
		return fmt.Errorf("error forwarding request: %w", err)
	}

	if status != http.StatusOK {
		return fmt.Errorf("peer %s status %d: %s", pc.baseURL, status, string(body))
	}

	var AuthToken struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(body, &AuthToken); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	pc.SetHeader("Authorization", "Bearer "+AuthToken.Token)

	return nil
}
