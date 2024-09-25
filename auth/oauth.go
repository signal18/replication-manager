package auth

import (
	"context"
	"fmt"
	"log"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

func OAuthGetTokenAndUser(OAuthProvider, OAuthClientID, OAuthClientSecret, RedirectURL, Code string) (*oauth2.Token, *oidc.UserInfo, error) {
	OAuthContext := context.Background()
	Provider, err := oidc.NewProvider(OAuthContext, OAuthProvider)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to init oidc from gitlab %s: %s", OAuthProvider, err.Error())
	}

	OAuthConfig := oauth2.Config{
		ClientID:     OAuthClientID,
		ClientSecret: OAuthClientSecret,
		Endpoint:     Provider.Endpoint(),
		RedirectURL:  RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email", "read_api", "api"},
	}

	log.Printf("OAuth oidc to gitlab: %v\n", OAuthConfig)
	oauth2Token, err := OAuthConfig.Exchange(OAuthContext, Code)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to exchange token: %s", err.Error())
	}

	userInfo, err := Provider.UserInfo(OAuthContext, oauth2.StaticTokenSource(oauth2Token))
	if err != nil {
		return oauth2Token, nil, fmt.Errorf("Failed to get userinfo: %s", err.Error())
	}

	return oauth2Token, userInfo, nil
}
