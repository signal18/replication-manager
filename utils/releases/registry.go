// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package releases

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Registry endpoints, variables so tests can point them at a local server.
var (
	registryAuthBase = "https://auth.docker.io"
	registryBase     = "https://registry-1.docker.io"
	hubAPIBase       = "https://hub.docker.com"
)

// hubRepo is the Docker Hub repository path of an image repository ("mariadb" ->
// "library/mariadb"); ok=false for another registry (a host before the first slash).
func hubRepo(repo string) (string, bool) {
	repo = strings.TrimSpace(repo)
	if first := strings.SplitN(repo, "/", 2)[0]; strings.ContainsAny(first, ".:") || first == "localhost" {
		return "", false
	}
	if !strings.Contains(repo, "/") {
		repo = "library/" + repo
	}
	return repo, true
}

func hubToken(ctx context.Context, client *http.Client, repo string) (string, error) {
	tokReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		registryAuthBase+"/token?service=registry.docker.io&scope=repository:"+repo+":pull", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(tokReq)
	if err != nil {
		return "", err
	}
	var tok struct {
		Token string `json:"token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tok)
	resp.Body.Close()
	if err != nil || tok.Token == "" {
		return "", fmt.Errorf("docker hub token: %v", err)
	}
	return tok.Token, nil
}

// manifestDigest asks the registry for the manifest (list) digest of repo:tag.
// found=false on 404.
func manifestDigest(ctx context.Context, client *http.Client, repo, tag string) (digest string, found bool, err error) {
	tok, err := hubToken(ctx, client, repo)
	if err != nil {
		return "", false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, registryBase+"/v2/"+repo+"/manifests/"+tag, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", strings.Join([]string{
		"application/vnd.docker.distribution.manifest.list.v2+json",
		"application/vnd.docker.distribution.manifest.v2+json",
		"application/vnd.oci.image.index.v1+json",
		"application/vnd.oci.image.manifest.v1+json",
	}, ", "))
	resp, err := client.Do(req)
	if err != nil {
		return "", false, err
	}
	resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Header.Get("Docker-Content-Digest"), true, nil
	case http.StatusNotFound:
		return "", false, nil
	}
	return "", false, fmt.Errorf("docker hub answered %d for %s:%s", resp.StatusCode, repo, tag)
}

var explicitTagRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]*)?$`)
var plainReleaseRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// IsExplicitTag says whether a tag names one release (11.8.9, 8.0.41-debian, or a
// digest reference) rather than a moving pointer (latest, lts, 11.8, 11).
func IsExplicitTag(tag string) bool {
	return strings.Contains(tag, "@sha256:") || explicitTagRe.MatchString(tag)
}

// IsExplicitImage is IsExplicitTag on the tag of repo:tag.
func IsExplicitImage(img string) bool {
	_, tag := SplitImage(img)
	return IsExplicitTag(tag)
}

// pickExplicit chooses the release tag among the tags sharing a digest: the plain
// x.y.z first, else the shortest explicit one; "" when none is explicit.
func pickExplicit(names []string) string {
	var plain, explicit []string
	for _, n := range names {
		if plainReleaseRe.MatchString(n) {
			plain = append(plain, n)
		} else if IsExplicitTag(n) {
			explicit = append(explicit, n)
		}
	}
	byLen := func(a []string) string {
		sort.Slice(a, func(i, j int) bool {
			if len(a[i]) != len(a[j]) {
				return len(a[i]) < len(a[j])
			}
			return a[i] < a[j]
		})
		return a[0]
	}
	if len(plain) > 0 {
		return byLen(plain)
	}
	if len(explicit) > 0 {
		return byLen(explicit)
	}
	return ""
}

// ResolveTag turns a moving tag of a Docker Hub image (latest, lts, 11.8) into the
// release it points at today: the x.y.z tag sharing its manifest digest, else the tag
// pinned by digest ("tag@sha256:..."). An explicit tag resolves to itself without a
// request. checked=false for another registry: the caller renders the tag as is.
func ResolveTag(ctx context.Context, repo, tag string) (explicit string, digest string, checked bool, err error) {
	tag = strings.TrimSpace(tag)
	if IsExplicitTag(tag) {
		return tag, "", true, nil
	}
	hrepo, ok := hubRepo(repo)
	if !ok {
		return "", "", false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	client := &http.Client{}
	digest, found, err := manifestDigest(ctx, client, hrepo, tag)
	if err != nil {
		return "", "", true, err
	}
	if !found {
		return "", "", true, fmt.Errorf("tag %s not found on docker hub for %s", tag, repo)
	}
	// The Hub tag listing carries the manifest digest of every tag: collect the names
	// sharing ours, newest first, five pages at most (the release of a moving tag is
	// recent by construction).
	var names []string
	url := hubAPIBase + "/v2/repositories/" + hrepo + "/tags?page_size=100&ordering=last_updated"
	for page := 0; page < 5 && url != ""; page++ {
		req, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if rerr != nil {
			return "", "", true, rerr
		}
		resp, rerr := client.Do(req)
		if rerr != nil {
			return "", "", true, rerr
		}
		var body struct {
			Next    string `json:"next"`
			Results []struct {
				Name   string `json:"name"`
				Digest string `json:"digest"`
			} `json:"results"`
		}
		derr := json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if derr != nil {
			return "", "", true, fmt.Errorf("docker hub tag listing: %v", derr)
		}
		for _, t := range body.Results {
			if t.Digest == digest {
				names = append(names, t.Name)
			}
		}
		if e := pickExplicit(names); e != "" {
			return e, digest, true, nil
		}
		url = body.Next
	}
	return tag + "@" + digest, digest, true, nil
}

// TagExists asks Docker Hub whether repo:tag has a manifest, for the public
// registry only: an image of another registry (a host before the first slash)
// answers checked=false, so the caller reports "not checked" rather than a guess.
// A wrong tag found here costs a request; found by the rolling upgrade it costs a
// replica already stopped.
func TagExists(ctx context.Context, repo, tag string) (exists bool, checked bool, err error) {
	hrepo, ok := hubRepo(repo)
	if !ok {
		return false, false, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, found, err := manifestDigest(ctx, &http.Client{}, hrepo, tag)
	if err != nil {
		return false, true, err
	}
	return found, true, nil
}
