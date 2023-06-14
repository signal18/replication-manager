package gitlab

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
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

func handlerGetGitLabTokenOAuth(w http.ResponseWriter, r *http.Request, acces_token string) string {

	// curl --request GET --header "Authorization: Bearer XXX" "https://gitlab.signal18.io/api/v4/personal_access_tokens?revoked=false"

	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/personal_access_tokens?revoked=false", nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return ""
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	log.Println("Gitlab API Response: ", string(body))

	var tokenInfos []TokenInfo

	err = json.Unmarshal(body, &tokenInfos)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return ""
	}

	id := strconv.Itoa(tokenInfos[0].ID)

	// curl --request POST --header "Authorization: Bearer 592d333b9a44357ff238ecfc13a1920290bb28c57026f48494ae6e09853d556a" "https://gitlab.signal18.io/api/v4/personal_access_tokens/11/rotate"

	req, err = http.NewRequest("POST", "https://gitlab.signal18.io/api/v4/personal_access_tokens/"+id+"/rotate", nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+acces_token)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return ""
	}
	defer resp.Body.Close()
	body, _ = ioutil.ReadAll(resp.Body)
	//log.Println("Gitlab API Response: ", string(body))

	err = json.Unmarshal(body, &tokenInfos[0])
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return ""
	}
	return tokenInfos[0].Token

}

func handlerGitLabCreateProject(token string, name string, path string, namespace string) {
	req, err := http.NewRequest("GET", "https://gitlab.signal18.io/api/v4/projects?search="+name, nil)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
	}
	req.Header.Set("Private-token", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
	}
	defer resp.Body.Close()
	body, _ := ioutil.ReadAll(resp.Body)
	log.Println("Gitlab API Response: ", string(body))

	var ProjectInfos []ProjectInfo

	err = json.Unmarshal(body, &ProjectInfos)
	if err != nil {
		log.Println("Gitlab API Error: ", err)
		return
	}

	if len(ProjectInfos) != 0 {
		project_path := ProjectInfos[0].PathNameSpace
		if project_path == path {
			return
		} else {
			jsondata := "{'name': " + name + ", 'description': '', 'path': " + name + ",'namespace': " + namespace + ", 'initialize_with_readme': 'true'}"
			b := bytes.NewBuffer([]byte(jsondata))
			req, err := http.NewRequest("POST", "https://gitlab.example.com/api/v4/projects/", b)
			if err != nil {
				log.Println("Gitlab API Error: ", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Private-token", token)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				log.Println("Gitlab API Error: ", err)
				return
			}
			defer resp.Body.Close()
			body, _ := ioutil.ReadAll(resp.Body)
			log.Println("Gitlab API Response: ", string(body))
		}
	}
}
