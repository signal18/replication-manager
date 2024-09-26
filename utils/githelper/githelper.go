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
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/sirupsen/logrus"
)

var Logger = logrus.New()

type TokenInfo struct {
	ID     int    `json:"id"`
	Name   string `json:"name"`
	UserID int    `json:"user_id"`
	Token  string `json:"token"`
}

type NameSpace struct {
	ID int `json:"id"`
}

type UserId struct {
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

func GetGitLabTokenOAuth(acces_token string) (string, int, []byte, error) {

	uid, body, err := GetGitLabUserId(acces_token)
	if err != nil {
		return "", -1, body, fmt.Errorf("Error when getting gitlab user: %v", err)
	}

	if uid == 0 {
		return "", -1, body, fmt.Errorf("Error when getting gitlab user: got 0 as user id")
	}

	req, err := http.NewRequest("GET", fmt.Sprintf("https://gitlab.signal18.io/api/v4/personal_access_tokens?revoked=false&user_id=%d", uid), nil)
	if err != nil {
		return "", -1, body, fmt.Errorf("Error when creating personal access token request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1, body, fmt.Errorf("Error when requesting personal access token: %v", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)

	var tokenInfos []TokenInfo

	err = json.Unmarshal(body, &tokenInfos)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1, body, fmt.Errorf("Error when decoding personal access token: %v", err)
	}

	id := strconv.Itoa(tokenInfos[0].ID)

	req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/personal_access_tokens/"+id+"/rotate", nil)
	if err != nil {
		return "", -1, body, fmt.Errorf("Error when creating rotate token request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		return "", -1, body, fmt.Errorf("Error when requesting rotate token: %v", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)

	err = json.Unmarshal(body, &tokenInfos[0])
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1, body, fmt.Errorf("Error when decoding rotate token: %v", err)
	}
	return tokenInfos[0].Token, tokenInfos[0].ID, body, nil

}

func GitLabCreateProject(token string, name string, path string, namespace string, user_id int, log_git bool) {
	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/projects?search="+name, nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return
	}
	req.Header.Set("Private-token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if log_git {
		log.Println("Gitlab API Response: ", string(body))
	}

	var ProjectInfos []ProjectInfo

	err = json.Unmarshal(body, &ProjectInfos)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return
	}

	if len(ProjectInfos) != 0 && ProjectInfos[0].PathNameSpace == path {
		return
	} else {
		req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/projects?namespace="+namespace, nil)
		if err != nil {
			log.Println("Gitlab API Error: ", err)
			return
		}
		req.Header.Set("Private-token", token)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			log.Println("Gitlab API Error: ", err)
			return
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)

		if log_git {
			log.Println("Gitlab API Response: ", string(body))
		}

		var ProjectInfos []ProjectInfo

		err = json.Unmarshal(body, &ProjectInfos)
		if err != nil {
			log.Println("Gitlab API Error: ", err)
			return
		}
		if len(ProjectInfos) != 0 {
			namespace_id := strconv.Itoa(ProjectInfos[0].Namespace.ID)
			jsondata := `{"name": "` + strings.ToLower(name) + `", "description": "", "path": "` + strings.ToLower(name) + `","namespace_id": ` + namespace_id + `, "initialize_with_readme": "false"}`
			b := bytes.NewBuffer([]byte(jsondata))
			req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/projects/", b)
			if err != nil {
				log.Println("Gitlab API Error: ", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Private-token", token)
			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				log.Println("Gitlab API Error: ", err)
				return
			}
			defer resp.Body.Close()
			body, _ = io.ReadAll(resp.Body)
			if log_git {
				log.Println("Gitlab API Response: ", string(body))
			}
		}
	}

}

func RefreshAccessToken(refresh_tok string, client_id string, secret_id string, log_git bool) (string, string, error) {
	url := "https://gitlab.signal18.io/oauth/token"
	payload := strings.NewReader("grant_type=refresh_token&client_id=" + client_id + "&client_secret=" + secret_id + "&refresh_token=" + refresh_tok)

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, payload)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	req.Header.Add("content-type", "application/x-www-form-urlencoded")

	res, err := client.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", "", err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	if log_git {
		log.Println("Gitlab API Response: ", string(body))
	}

	var accessToken AccessToken

	err = json.Unmarshal(body, &accessToken)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", "", err
	}

	return accessToken.AccessToken, accessToken.RefreshToken, nil
}

func GetGitLabTokenBasicAuth(user string, password string) (string, []byte, error) {
	var accessToken AccessToken
	var body = make([]byte, 0)

	url := "https://gitlab.signal18.io/oauth/token"
	data := "grant_type=password&username=" + user + "&password=" + password

	client := &http.Client{}
	req, err := http.NewRequest("POST", url, strings.NewReader(data))
	if err != nil {
		return "", body, fmt.Errorf("Error when creating request to gitlab: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", body, fmt.Errorf("Error when sending request to gitlab: %v", err)
	}
	defer resp.Body.Close()

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return "", body, fmt.Errorf("Error when reading response from gitlab: %v", err)
	}

	err = json.Unmarshal(body, &accessToken)
	if err != nil {
		return "", body, fmt.Errorf("Error when decoding response from gitlab: %v", err)
	}

	return accessToken.AccessToken, body, nil

}

func GetGitLabUserId(acces_token string) (int, []byte, error) {
	var body = make([]byte, 0)

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/user", nil)
	if err != nil {
		return 0, body, fmt.Errorf("Gitlab User API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, body, fmt.Errorf("Gitlab User API Error: ", err)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)

	var userId UserId

	err = json.Unmarshal(body, &userId)
	if err != nil {
		return 0, body, fmt.Errorf("Gitlab User API Unmarshall Error: ", err)
	}

	return userId.ID, body, nil

}
