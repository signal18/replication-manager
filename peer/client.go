package peer

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

// PeerClient represents the HTTP client for peer communication
type PeerClient struct {
	client  *http.Client
	baseURL string
	headers map[string]string
}

// NewPeerClient initializes a new PeerClient
func NewPeerClient(baseURL string, timeout time.Duration) *PeerClient {
	return &PeerClient{
		client: &http.Client{
			Timeout: timeout,
		},
		baseURL: baseURL,
		headers: make(map[string]string),
	}
}

// SetBaseURL allows changing the base URL dynamically
func (pc *PeerClient) SetBaseURL(baseURL string) {
	pc.baseURL = baseURL
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
		return resp.StatusCode, nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, respBody, fmt.Errorf("peer status %d: %s", resp.StatusCode, string(respBody))
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
