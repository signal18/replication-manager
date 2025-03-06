package githelper

import (
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"

	"github.com/signal18/replication-manager/utils/misc"
)

func CreatePersonalAccessTokenCSRF(gitlabUser, gitlabPassword, tokenName string) (string, error) {
	// Replace with your values
	gitlabHost := "https://gitlab.signal18.io"

	// Create a cookie jar to handle cookies
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}

	// 1. Get the login page to retrieve CSRF token
	loginPageURL := fmt.Sprintf("%s/users/sign_in", gitlabHost)
	body, err := misc.GetRequest(client, loginPageURL)
	if err != nil {
		return "", err
	}

	csrfToken, err := misc.ExtractValue(body, `name="authenticity_token" value="([^"]+)"`)
	if err != nil {
		return "", err
	}

	// 2. Send login credentials
	form := url.Values{
		"user[login]":        {gitlabUser},
		"user[password]":     {gitlabPassword},
		"authenticity_token": {csrfToken},
	}
	_, err = misc.PostRequest(client, loginPageURL, form, nil)
	if err != nil {
		return "", err
	}

	// 3. Access personal access tokens page to retrieve new CSRF token
	tokensPageURL := fmt.Sprintf("%s/-/user_settings/personal_access_tokens", gitlabHost)
	body, err = misc.GetRequest(client, tokensPageURL)
	if err != nil {
		return "", err
	}

	csrfToken, err = misc.ExtractValue(body, `name="csrf-token" content="([^"]+)"`)
	if err != nil {
		return "", err
	}
	fmt.Println("New CSRF Token:", csrfToken)

	// 4. Generate a personal access token
	form = url.Values{
		"authenticity_token":              {csrfToken},
		"personal_access_token[name]":     {tokenName},
		"personal_access_token[scopes][]": {"api", "write_repository"},
	}
	headers := map[string]string{
		"X-CSRF-Token": csrfToken,
	}
	body, err = misc.PostRequest(client, tokensPageURL, form, headers)
	if err != nil {
		return "", err
	}

	fmt.Println("New PAT Token:", body)

	// 5. Scrape the personal access token from the response
	return misc.ExtractValue(body, `"new_token":"([^"]+)"`)
}
