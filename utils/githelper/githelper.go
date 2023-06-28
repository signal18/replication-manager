// replication-manager - Replication Manager Monitoring and CLI for MariaDB and MySQL
// Copyright 2017-2021 SIGNAL18 CLOUD SAS
// Authors: Guillaume Lefranc <guillaume@signal18.io>
//          Stephane Varoqui  <svaroqui@gmail.com>
// This source code is licensed under the GNU General Public License, version 3.

package githelper

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"strings"
)

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

func GetGitLabTokenOAuth(acces_token string, log_git bool) (string, int) {

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/personal_access_tokens?revoked=false", nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)

	if log_git {
		log.Println("Gitlab API Response: ", string(body))
	}

	var tokenInfos []TokenInfo

	err = json.Unmarshal(body, &tokenInfos)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1
	}

	id := strconv.Itoa(tokenInfos[0].ID)

	req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/personal_access_tokens/"+id+"/rotate", nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1
	}
	defer resp.Body.Close()
	body, _ = ioutil.ReadAll(resp.Body)
	//log.Println("Gitlab API Response: ", string(body))

	err = json.Unmarshal(body, &tokenInfos[0])
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return "", -1
	}
	return tokenInfos[0].Token, tokenInfos[0].ID

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
	body, _ := ioutil.ReadAll(resp.Body)

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
		body, _ := ioutil.ReadAll(resp.Body)

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
			body, _ = ioutil.ReadAll(resp.Body)
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

	body, err := ioutil.ReadAll(res.Body)
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
