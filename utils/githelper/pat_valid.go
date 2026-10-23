// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3.

package githelper

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// GitLabURL is the GitLab that PATValid asks; a variable so tests can point it at a fake server.
var GitLabURL = "https://gitlab.signal18.io"

var patValidClient = &http.Client{Timeout: 10 * time.Second}

// PATValid tells whether the instance's saved personal access token must be replaced (#2153).
// valid is false only when GitLab definitely refuses it: no token, HTTP 401 or 403 (GitLab
// answers 401 for an expired token), or a 200 saying it is inactive or revoked. Any other
// failure (network, timeout, 5xx, 404 on a GitLab without the endpoint, an unreadable reply)
// returns valid with the error: the caller keeps the token and warns, since minting a new one
// during a GitLab outage would leave a revoked token behind at every start.
func PATValid(token string) (valid bool, err error) {
	if token == "" {
		return false, fmt.Errorf("no token")
	}
	req, err := http.NewRequest("GET", GitLabURL+"/api/v4/personal_access_tokens/self", nil)
	if err != nil {
		return true, err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := patValidClient.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return false, fmt.Errorf("token refused by GitLab (HTTP %d)", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return true, fmt.Errorf("token not checked, GitLab answered HTTP %d", resp.StatusCode)
	}
	var info struct {
		Active  bool `json:"active"`
		Revoked bool `json:"revoked"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&info); err != nil {
		return true, fmt.Errorf("token not checked, unreadable GitLab reply: %v", err)
	}
	if !info.Active || info.Revoked {
		return false, fmt.Errorf("token revoked or inactive")
	}
	return true, nil
}
