// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package githelper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	git_https "github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/sirupsen/logrus"
)

var Logrus = logrus.New()

type GitRepository struct {
	R         *git.Repository
	WT        *git.Worktree
	Auth      *git_https.BasicAuth
	Path      string
	URL       string
	IsPushing bool
}

func InitGitRepo(url string, tok string, user string, path string) (*GitRepository, error) {

	auth := &git_https.BasicAuth{
		Username: user, // can be any non-empty string
		Password: tok,
	}

	// Open the repository
	r, err := git.PlainOpen(path)
	if err != nil {
		return nil, fmt.Errorf("git error: cannot open repository: %w", err)
	}

	// Get the working tree
	w, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git error: cannot get worktree: %w", err)
	}

	gr := &GitRepository{
		R:    r,
		WT:   w,
		Auth: auth,
		Path: path,
		URL:  url,
	}

	return gr, err
}

func InitGitClone(url string, tok string, user string, path string) (*GitRepository, error) {

	auth := &git_https.BasicAuth{
		Username: user, // can be any non-empty string
		Password: tok,
	}

	// Clone the repository to the given directory
	r, err := git.PlainClone(path, false, &git.CloneOptions{
		URL:               url,
		RecurseSubmodules: git.DefaultSubmoduleRecursionDepth,
		Auth:              auth,
	})
	if err != nil {
		return nil, fmt.Errorf("git error: cannot clone repository %s: %w", url, err)
	}

	// Get the working tree
	w, err := r.Worktree()
	if err != nil {
		return nil, fmt.Errorf("git error: cannot get worktree: %w", err)
	}

	gr := &GitRepository{
		R:    r,
		WT:   w,
		Auth: auth,
		Path: path,
		URL:  url,
	}

	return gr, err
}

func CloneConfigFromGit(url string, user string, tok string, path string) (*GitRepository, error) {
	if _, err := os.Stat(path + "/.git"); err == nil {
		return InitGitRepo(url, tok, user, path)
	} else {
		return InitGitClone(url, tok, user, path)

	}
}

func (gr *GitRepository) Pull(force bool) error {
	// Pull the latest changes from the remote repository
	err := gr.WT.Pull(&git.PullOptions{
		RemoteName: "origin",
		Auth:       gr.Auth,
		RemoteURL:  gr.URL,
		Force:      force,
	})
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return err
	}
	return nil
}

func (gr *GitRepository) Push() error {
	// Pull the latest changes from the remote repository
	err := gr.R.Push(&git.PushOptions{Auth: gr.Auth})
	if err != nil && err.Error() != "already up-to-date" {
		return err
	}
	return nil
}

func (gr *GitRepository) AddGlob(pattern string) error {
	// // Get the working tree status
	// wStatus, err := gr.WT.Status()
	// if err != nil {
	// 	return err // Return error if status retrieval fails
	// }
	// var found bool
	// var filteredFiles []string

	// // Iterate over the working tree status
	// for filePath, fileStatus := range wStatus {

	// 	// Custom matching logic for recursive patterns
	// 	if matchesGlobPattern(pattern, filePath) {
	// 		if fileStatus.Worktree == git.Unmodified {
	// 			found = true
	// 			continue // Skip unmodified files
	// 		}

	// 		if _, err := gr.WT.Add(filePath); err != nil {
	// 			return err // Return error if adding the file fails
	// 		}
	// 		filteredFiles = append(filteredFiles, filePath)
	// 	}
	// }

	// // If no files matched the pattern, return a "no matches" error
	// if len(filteredFiles) == 0 && !found {
	// 	return git.ErrGlobNoMatches
	// }

	return gr.WT.AddGlob(pattern)
}

func matchesGlobPattern(pattern, path string) bool {
	// Handle recursive patterns like **/*
	if strings.Contains(pattern, "**") {
		basePattern := strings.ReplaceAll(pattern, "**", "")
		return strings.HasPrefix(path, basePattern) && strings.HasSuffix(path, strings.TrimPrefix(pattern, "**/"))
	}

	// Match normal patterns using filepath.Match
	matched, _ := filepath.Match(pattern, path)
	return matched
}

func (gr *GitRepository) Add(pattern string) (plumbing.Hash, error) {
	wStatus, _ := gr.WT.Status()
	// Check the status of the target file
	fileStatus, exists := wStatus[pattern]
	if !exists {
		return gr.WT.Add(pattern)
	}

	// Check if the file is unmodified
	if fileStatus.Worktree != git.Unmodified {
		return gr.WT.Add(pattern)
	}

	return plumbing.ZeroHash, nil
}

func (gr *GitRepository) HasStagedFiles() bool {
	ws, err := gr.WT.Status()
	if err == nil {
		for _, gf := range ws {
			if gf.Staging == git.Added || gf.Staging == git.Modified || gf.Staging == git.Deleted {
				// Logrus.Debugf("%s is %d", name, gf.Staging)
				return true
			}
		}
	}
	return false
}

func (gr *GitRepository) Commit(msg string, opts *git.CommitOptions) (plumbing.Hash, error) {
	return gr.WT.Commit(msg, opts)
}

type TokenInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	UserID int    `json:"user_id"`
	Token  string `json:"token"`
}

type NameSpace struct {
	ID int `json:"id"`
}

type ProjectInfo struct {
	ID            int       `json:"id"`
	Name          string    `json:"name"`
	PathNameSpace string    `json:"path_with_namespace"`
	Path          string    `json:"path"`
	Namespace     NameSpace `json:"namespace"`
}

type AccessToken struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

type GroupId struct {
	ID int `json:"id"`
}

type UserId struct {
	ID int `json:"id"`
}

type GroupAccess struct {
	ID          int `json:"id"`
	AccessLevel int `json:"access_level"`
}

type DuplicateRequest struct {
	Message RequestUserId `json:"message"`
}

type RequestUserId struct {
	UserId []string `json:"user_id"`
}

func GetGitLabTokenOAuth(acces_token string, token_name string, log_git bool) (string, int) {

	uid, err := GetGitLabUserId(acces_token, log_git)
	if err != nil {
		return "", -1
	}

	if uid == 0 {
		return "", -1
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://gitlab.signal18.io/api/v4/personal_access_tokens?revoked=false&user_id=%d&search=%s", uid, token_name), nil)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", -1
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if log_git {
		Logrus.Println("Gitlab API Response: ", string(body))
	}

	var tokenInfos []TokenInfo

	err = json.Unmarshal(body, &tokenInfos)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", -1
	}

	if len(tokenInfos) == 0 {
		Logrus.Println("Gitlab API Error: No PAT")
		return "", -1
	}

	id := strconv.Itoa(tokenInfos[0].ID)

	req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/personal_access_tokens/"+id+"/rotate", nil)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", -1
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", -1
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	//Logrus.Println("Gitlab API Response: ", string(body))

	err = json.Unmarshal(body, &tokenInfos[0])
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", -1
	}
	return tokenInfos[0].Token, tokenInfos[0].ID

}
func GitLabCreatePullProject(token string, name string, path string, namespace string, user_id int, log_git bool) {
	GitLabCreateProject(token, name+"-pull", path, namespace, user_id, log_git)
}

func GitLabCreateProject(token string, name string, path string, namespace string, user_id int, log_git bool) {
	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/projects/"+namespace+"%2F"+name, nil)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	req.Header.Set("Private-token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if log_git {
		Logrus.Println("Gitlab API Response: ", string(body))
	}

	if resp.StatusCode != http.StatusNotFound {
		return
	}

	req, err = http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/namespaces/"+namespace, nil)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	req.Header.Set("Private-token", token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)

	if log_git {
		Logrus.Println("Gitlab API Response: ", string(body))
	}

	if resp.StatusCode != http.StatusOK {
		return
	}

	var ns NameSpace

	err = json.Unmarshal(body, &ns)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}

	namespace_id := ns.ID
	lowerName := strings.ToLower(name)
	jsondata := fmt.Sprintf(`{"name": "%s", "description": "", "path": "%s","namespace_id": %d, "initialize_with_readme": "false"}`, lowerName, lowerName, namespace_id)
	b := bytes.NewBuffer([]byte(jsondata))
	req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/projects/", b)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Private-token", token)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return
	}
	defer resp.Body.Close()
	body, _ = io.ReadAll(resp.Body)
	if log_git {
		Logrus.Println("Gitlab API Response: ", string(body))
	}
}

func RefreshAccessToken(refresh_tok string, client_id string, secret_id string, log_git bool) (string, string, error) {
	url := "https://gitlab.signal18.io/oauth/token"
	payload := strings.NewReader("grant_type=refresh_token&client_id=" + client_id + "&client_secret=" + secret_id + "&refresh_token=" + refresh_tok)

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, payload)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	if log_git {
		Logrus.Println("Gitlab API Response: ", string(body))
	}

	var accessToken AccessToken

	err = json.Unmarshal(body, &accessToken)
	if err != nil {
		Logrus.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	return accessToken.AccessToken, accessToken.RefreshToken, nil
}

type ErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func GetGitLabTokenBasicAuth(user string, password string, log_git bool) (string, error) {
	escpasswd := url.QueryEscape(password)
	url := "https://gitlab.signal18.io/oauth/token"
	data := "grant_type=password&username=" + user + "&password=" + escpasswd

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, strings.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("error creating request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("error sending request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("error reading response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// Parse the error response into Main struct
		var apiError ErrorResponse
		if err := json.Unmarshal(body, &apiError); err != nil {
			return "", fmt.Errorf("received non-OK HTTP status %d and failed to parse error response: %w", resp.StatusCode, err)
		}
		return "", fmt.Errorf("API error: %s - %s", apiError.Error, apiError.ErrorDescription)
	}

	var accessToken AccessToken
	err = json.Unmarshal(body, &accessToken)
	if err != nil {
		return "", fmt.Errorf("error unmarshalling response: %w", err)
	}

	if log_git {
		Logrus.Println("Git Auth Response:", string(body))
	}

	return accessToken.AccessToken, nil
}

func GetGitLabUserId(acces_token string, log_git bool) (int, error) {
	var body = make([]byte, 0)

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/user", nil)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Error: ", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		// Parse the error response into Main struct
		var apiError ErrorResponse
		if err := json.Unmarshal(body, &apiError); err != nil {
			return 0, fmt.Errorf("received non-OK HTTP status %d and failed to parse error response: %w", resp.StatusCode, err)
		}
		return 0, fmt.Errorf("API error: %d - %s - %s", resp.StatusCode, apiError.Error, apiError.ErrorDescription)
	}

	if log_git {
		Logrus.Debugf("Init Git Config - Get User response: %s \n", string(body))
	}

	var userId UserId

	err = json.Unmarshal(body, &userId)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return userId.ID, nil

}

// Get User Access Level
func InitGroupAccessLevel(acces_token, domain string, user_id int, log_git bool) (int, error) {
	var body = make([]byte, 0)
	var err error

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/groups/"+domain, nil)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()

	// If 404 error, create the group
	if resp.StatusCode == http.StatusNotFound {
		_, createErr := CreateCloud18Domain(acces_token, domain, log_git)
		if createErr != nil {
			return 0, fmt.Errorf("Error creating GitLab domain %s: %s", domain, createErr.Error())
		}
		// Try to get the access level again
		return InitGroupAccessLevel(acces_token, domain, user_id, log_git)
	}

	body, _ = io.ReadAll(resp.Body)
	if log_git {
		Logrus.Debugf("Get User Access response: %s \n", string(body))
	}

	var groupId GroupId
	err = json.Unmarshal(body, &groupId)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	// Return Group Access Level
	return GetGroupUserAccess(acces_token, domain, user_id, log_git)
}

func CreateCloud18Domain(acces_token, domain string, log_git bool) (int, error) {
	data := "name=" + domain + "&path=" + domain
	req, err := http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/groups", strings.NewReader(data))
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if log_git {
		Logrus.Debugf("Create Cloud18 Domain response: %s \n", string(body))
	}

	if resp.StatusCode != http.StatusCreated {
		// Parse the error response into Main struct
		var apiError ErrorResponse
		if err := json.Unmarshal(body, &apiError); err != nil {
			return 0, fmt.Errorf("received non-OK HTTP status %d and failed to parse error response: %w", resp.StatusCode, err)
		}
		return 0, fmt.Errorf("API error: %d - %s - %s", resp.StatusCode, apiError.Error, apiError.ErrorDescription)
	}

	var groupId GroupId

	err = json.Unmarshal(body, &groupId)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return groupId.ID, nil

}

// Get User Access Level For Group
func GetGroupUserAccess(acces_token, domain string, user_id int, log_git bool) (int, error) {
	var body = make([]byte, 0)
	var err error

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/groups/"+domain+"/members/"+strconv.Itoa(user_id), nil)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()

	// If 404 error, create the group
	if resp.StatusCode == http.StatusNotFound {
		Logrus.Errorf("User not listed in domain. Try requesting access to domain %s \n", domain)
		_, reqErr := RegisterToCloud18Domain(acces_token, domain, log_git)
		if reqErr != nil {
			return 0, fmt.Errorf("error requesting access for GitLab domain %s: %w", domain, reqErr)
		}
		// Try to get the access level again
		return 0, fmt.Errorf("Requested access for GitLab domain %s", domain)
	}

	body, _ = io.ReadAll(resp.Body)
	if log_git {
		Logrus.Debugf("Get User Access response: %s", string(body))
	}

	var groupAccess GroupAccess
	err = json.Unmarshal(body, &groupAccess)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return groupAccess.AccessLevel, nil
}

func RegisterToCloud18Domain(acces_token, domain string, log_git bool) (int, error) {
	req, err := http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/groups/"+domain+"/access_requests", nil)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if log_git {
		Logrus.Debugf("Create Cloud18 Domain response: %s", string(body))
	}

	if resp.StatusCode != http.StatusCreated {
		// Parse the error response into Main struct
		if resp.StatusCode == http.StatusBadRequest {
			var duplicateReq DuplicateRequest
			if err := json.Unmarshal(body, &duplicateReq); err != nil {
				return 0, fmt.Errorf("received Bad Request status %d and failed to parse error response: %w", resp.StatusCode, err)
			}
			return 0, fmt.Errorf("API error: %d - %s", resp.StatusCode, duplicateReq.Message.UserId)
		} else {
			var apiError ErrorResponse
			if err := json.Unmarshal(body, &apiError); err != nil {
				return 0, fmt.Errorf("received non-OK HTTP status %d and failed to parse error response: %w", resp.StatusCode, err)
			}
			return 0, fmt.Errorf("API error: %d - %s - %s", resp.StatusCode, apiError.Error, apiError.ErrorDescription)
		}
	}

	var reqId UserId

	err = json.Unmarshal(body, &reqId)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return reqId.ID, nil

}

func RegisterToCloud18Project(acces_token, project string, log_git bool) (int, error) {
	req, err := http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/projects/"+project+"/access_requests", nil)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)
	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if log_git {
		Logrus.Debugf("Create Cloud18 Domain response: %s", string(body))
	}

	if resp.StatusCode != http.StatusCreated {
		// Parse the error response into Main struct
		var apiError ErrorResponse
		if err := json.Unmarshal(body, &apiError); err != nil {
			return 0, fmt.Errorf("received non-OK HTTP status %d and failed to parse error response: %w", resp.StatusCode, err)
		}
		return 0, fmt.Errorf("API error: %d - %s - %s", resp.StatusCode, apiError.Error, apiError.ErrorDescription)
	}

	var reqId UserId

	err = json.Unmarshal(body, &reqId)
	if err != nil {
		return 0, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return reqId.ID, nil

}
