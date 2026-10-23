// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// License: GNU General Public License, version 3.

package githelper

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// PATValid tells whether GitLab still accepts a personal access token: active, not revoked, not
// expired. A start or a settings change reuses the instance's saved token when it is, instead of
// rotating it (each rotation leaves a revoked token behind in GitLab).
func PATValid(token string) error {
	if token == "" {
		return fmt.Errorf("no token")
	}
	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/personal_access_tokens/self", nil)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("token refused by GitLab (HTTP %d)", resp.StatusCode)
	}
	var info struct {
		Active  bool `json:"active"`
		Revoked bool `json:"revoked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err
	}
	if !info.Active || info.Revoked {
		return fmt.Errorf("token revoked or inactive")
	}
	return nil
}
