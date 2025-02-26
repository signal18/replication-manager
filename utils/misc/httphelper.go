package misc

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
)

// Extract a token using regex pattern
func ExtractValue(body, pattern string) (string, error) {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(body)
	if len(matches) < 2 {
		return "", fmt.Errorf("failed to extract token with pattern: %s", pattern)
	}
	return matches[1], nil
}

// Perform an HTTP GET request and return the response body as a string
func GetRequest(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed GET request to %s: %v", url, err)
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}
	return string(body), nil
}

// Perform an HTTP Post request and return the response body as a string
func PostRequest(client *http.Client, url string, form url.Values, headers map[string]string) (string, error) {
	req, err := http.NewRequest("POST", url, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("failed to create POST request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed POST request to %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %v", err)
	}
	return string(body), nil
}
