// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TagExists asks Docker Hub whether repo:tag has a manifest, for the public
// registry only: an image of another registry (a host before the first slash)
// answers checked=false, so the caller reports "not checked" rather than a guess.
// A wrong tag found here costs a request; found by the rolling upgrade it costs a
// replica already stopped.
func TagExists(ctx context.Context, repo, tag string) (exists bool, checked bool, err error) {
	repo = strings.TrimSpace(repo)
	if first := strings.SplitN(repo, "/", 2)[0]; strings.ContainsAny(first, ".:") || first == "localhost" {
		return false, false, nil
	}
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	client := &http.Client{}
	tokReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://auth.docker.io/token?service=registry.docker.io&scope=repository:"+repo+":pull", nil)
	if err != nil {
		return false, true, err
	}
	resp, err := client.Do(tokReq)
	if err != nil {
		return false, true, err
	}
	var tok struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if err != nil || tok.Token == "" {
		return false, true, fmt.Errorf("docker hub token: %v", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead,
		"https://registry-1.docker.io/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return false, true, err
	}
	req.Header.Set("Authorization", "Bearer "+tok.Token)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
	}, ", "))
	resp, err = client.Do(req)
	if err != nil {
		return false, true, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return true, true, nil
	case http.StatusNotFound:
		return false, true, nil
	}
	return false, true, fmt.Errorf("docker hub answered %d for %s:%s", resp.StatusCode, repo, tag)
}
