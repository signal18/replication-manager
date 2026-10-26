// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2026 SIGNAL18 CLOUD SAS
// This source code is licensed under the GNU General Public License, version 3.

package githelper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Registration through Cloud18 (#2159): the GitLab calls replication-manager makes with the
// GitLab token of the person registering it (given by the Cloud18 realm, never stored), and the
// self-rotation of the project tokens it keeps. No password, no admin token.

// ProjectTokenScopes are the scopes of the token an instance keeps per project: push and pull,
// and renewing itself.
var ProjectTokenScopes = []string{"write_repository", "self_rotate"}

var cloud18GitLabClient = &http.Client{Timeout: 30 * time.Second}

// GitLabError is a non-2xx answer of GitLab.
type GitLabError struct {
	Status  int
	Message string
}

func (e *GitLabError) Error() string {
	return fmt.Sprintf("GitLab answered %d: %s", e.Status, e.Message)
}

// GitLabStatus is the status of a GitLab answer error, 0 for any other error.
func GitLabStatus(err error) int {
	var e *GitLabError
	if errors.As(err, &e) {
		return e.Status
	}
	return 0
}

// gitlabCall calls the GitLab API with token, as an OAuth token (bearer) or an access token
// (PRIVATE-TOKEN), and decodes a 2xx answer into out.
func gitlabCall(method, path, token string, bearer bool, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, GitLabURL+"/api/v4"+path, reader)
	if err != nil {
		return err
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cloud18GitLabClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	answer, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		msg := strings.TrimSpace(redactTokens(answer))
		if len(msg) > 300 {
			msg = msg[:300] + "…"
		}
		return &GitLabError{Status: resp.StatusCode, Message: msg}
	}
	if out != nil && len(answer) > 0 {
		if err := json.Unmarshal(answer, out); err != nil {
			return fmt.Errorf("unreadable GitLab answer: %w", err)
		}
	}
	return nil
}

// GitLabUser is the person a GitLab token belongs to.
type GitLabUser struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// GitLabTokenUser returns the person of an OAuth token.
func GitLabTokenUser(oauthToken string) (GitLabUser, error) {
	var u GitLabUser
	if err := gitlabCall(http.MethodGet, "/user", oauthToken, true, nil, &u); err != nil {
		return u, err
	}
	if u.ID <= 0 {
		return u, errors.New("GitLab gave no user")
	}
	return u, nil
}

// ErrNotDomainMember: the domain group exists and the person is not one of its members (at
// least Developer): the domain belongs to someone else.
var ErrNotDomainMember = errors.New("the domain exists in Cloud18 and you are not one of its members")

// ErrNotDomainMaintainer: the person is a member of the domain but not Maintainer, which a
// registration needs to give the instance its project tokens.
var ErrNotDomainMaintainer = errors.New("you are a member of this Cloud18 domain but not Maintainer: ask the domain owner to make you Maintainer, then register again")

// MaintainerAccess is GitLab's Maintainer access level.
const MaintainerAccess = 40

// GitLabEnsureDomainGroup returns the id of the domain group, created (private) when it does not
// exist (the person is then its owner); an existing one must count the person among its members,
// Maintainer at least (ErrNotDomainMaintainer otherwise).
func GitLabEnsureDomainGroup(oauthToken, domain string, userID int) (int, error) {
	var g struct {
		ID int `json:"id"`
	}
	err := gitlabCall(http.MethodGet, "/groups/"+url.PathEscape(domain), oauthToken, true, nil, &g)
	if GitLabStatus(err) == http.StatusNotFound {
		err = gitlabCall(http.MethodPost, "/groups", oauthToken, true, map[string]string{"name": domain, "path": domain, "visibility": "private"}, &g)
		if err != nil {
			return 0, fmt.Errorf("domain %s not created: %w", domain, err)
		}
		return g.ID, nil
	}
	if err != nil {
		return 0, err
	}
	var m struct {
		AccessLevel int `json:"access_level"`
	}
	err = gitlabCall(http.MethodGet, "/groups/"+strconv.Itoa(g.ID)+"/members/all/"+strconv.Itoa(userID), oauthToken, true, nil, &m)
	if GitLabStatus(err) == http.StatusNotFound || (err == nil && m.AccessLevel < 30) {
		return 0, ErrNotDomainMember
	}
	if err != nil {
		return 0, err
	}
	if m.AccessLevel < MaintainerAccess {
		return 0, ErrNotDomainMaintainer
	}
	return g.ID, nil
}

// GitLabCreateProjectIn creates the private project path in the group. A path already taken
// (GitLab's own uniqueness) returns the existing project's id with taken true.
func GitLabCreateProjectIn(oauthToken string, groupID int, groupPath, path string) (id int, taken bool, err error) {
	var p struct {
		ID int `json:"id"`
	}
	err = gitlabCall(http.MethodPost, "/projects", oauthToken, true, map[string]interface{}{"name": path, "path": path, "namespace_id": groupID, "visibility": "private"}, &p)
	if err == nil {
		return p.ID, false, nil
	}
	if GitLabStatus(err) != http.StatusBadRequest || !strings.Contains(err.Error(), "has already been taken") {
		return 0, false, fmt.Errorf("project %s/%s not created: %w", groupPath, path, err)
	}
	if err := gitlabCall(http.MethodGet, "/projects/"+url.PathEscape(groupPath+"/"+path), oauthToken, true, nil, &p); err != nil {
		return 0, true, fmt.Errorf("project %s/%s exists and cannot be read: %w", groupPath, path, err)
	}
	return p.ID, true, nil
}

// GitLabProjectFile reads a file of the project's default branch; nil when the project or the
// file is not there.
func GitLabProjectFile(token string, bearer bool, projectID int, file string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, GitLabURL+"/api/v4/projects/"+strconv.Itoa(projectID)+"/repository/files/"+url.PathEscape(file)+"/raw?ref=HEAD", nil)
	if err != nil {
		return nil, err
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.Header.Set("PRIVATE-TOKEN", token)
	}
	resp, err := cloud18GitLabClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &GitLabError{Status: resp.StatusCode, Message: "file " + file}
	}
	return io.ReadAll(io.LimitReader(resp.Body, 4<<20))
}

// ProjectToken is a project access token and its end.
type ProjectToken struct {
	ID        int    `json:"id"`
	Token     string `json:"token"`
	ExpiresAt string `json:"expires_at"`
}

// GitLabCreateProjectToken creates a project access token (maintainer, ProjectTokenScopes) named
// name, valid until expires.
func GitLabCreateProjectToken(oauthToken string, projectID int, name string, expires time.Time) (ProjectToken, error) {
	var t ProjectToken
	err := gitlabCall(http.MethodPost, "/projects/"+strconv.Itoa(projectID)+"/access_tokens", oauthToken, true, map[string]interface{}{
		"name": name, "scopes": ProjectTokenScopes, "access_level": 40, "expires_at": expires.Format("2006-01-02"),
	}, &t)
	if err == nil && (t.Token == "" || t.ID <= 0) {
		err = errors.New("GitLab gave no project token (or no id)")
	}
	return t, err
}

// GitLabRotateProjectToken renews a project access token with itself: the old one stops working.
// A 401 means the token was already refused (expired, revoked): only a new registration helps.
func GitLabRotateProjectToken(projectToken string, projectID int, expires time.Time) (ProjectToken, error) {
	var t ProjectToken
	err := gitlabCall(http.MethodPost, "/projects/"+strconv.Itoa(projectID)+"/access_tokens/self/rotate?expires_at="+expires.Format("2006-01-02"), projectToken, false, nil, &t)
	if err == nil && t.Token == "" {
		err = errors.New("GitLab gave no project token")
	}
	return t, err
}

// GitLabRevokeProjectTokens revokes the active project access tokens named name except keepID (an
// instance's previous ones, once a reconnect gave it new ones). Best effort: how many it revoked.
func GitLabRevokeProjectTokens(oauthToken string, projectID int, name string, keepID int) int {
	if keepID <= 0 {
		return 0 // without the new token's id, the new token itself could be revoked
	}
	var tokens []struct {
		ID      int    `json:"id"`
		Name    string `json:"name"`
		Active  bool   `json:"active"`
		Revoked bool   `json:"revoked"`
	}
	if err := gitlabCall(http.MethodGet, "/projects/"+strconv.Itoa(projectID)+"/access_tokens?state=active&per_page=100", oauthToken, true, nil, &tokens); err != nil {
		return 0
	}
	n := 0
	for _, t := range tokens {
		if t.Name == name && t.Active && !t.Revoked && t.ID != keepID {
			if gitlabCall(http.MethodDelete, "/projects/"+strconv.Itoa(projectID)+"/access_tokens/"+strconv.Itoa(t.ID), oauthToken, true, nil, nil) == nil {
				n++
			}
		}
	}
	return n
}

// GitLabPersonalTokenExpires returns the end date (YYYY-MM-DD, "" when none) of a personal access
// token, read with the token itself.
func GitLabPersonalTokenExpires(token string) (string, error) {
	var t struct {
		ExpiresAt string `json:"expires_at"`
		Active    bool   `json:"active"`
	}
	if err := gitlabCall(http.MethodGet, "/personal_access_tokens/self", token, false, nil, &t); err != nil {
		return "", err
	}
	if !t.Active {
		return "", &GitLabError{Status: http.StatusUnauthorized, Message: "token not active"}
	}
	return t.ExpiresAt, nil
}

// GitLabRotatePersonalToken renews a personal access token with itself (scope api): same name and
// scopes, the old one stops working. No password (#2159). A 401 means it was already refused.
func GitLabRotatePersonalToken(token string, expires time.Time) (ProjectToken, error) {
	var t ProjectToken
	err := gitlabCall(http.MethodPost, "/personal_access_tokens/self/rotate?expires_at="+expires.Format("2006-01-02"), token, false, nil, &t)
	if err == nil && t.Token == "" {
		err = errors.New("GitLab gave no personal token")
	}
	return t, err
}
