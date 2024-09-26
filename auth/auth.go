package auth

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/signal18/replication-manager/auth/user"
	"github.com/signal18/replication-manager/config"
)

type AuthTry struct {
	User string    `json:"username"`
	Try  int       `json:"try"`
	Time time.Time `json:"time"`
}

// Users will be stored here
type Auth struct {
	Attempts         *AuthTryMap
	Users            *user.UserMap
	ServerGrantOpts  []string
	ClusterGrantOpts []string
}

func InitAuth() Auth {
	a := Auth{
		Attempts:         NewAuthTryMap(),
		Users:            user.NewUserMap(),
		ServerGrantOpts:  make([]string, 0),
		ClusterGrantOpts: make([]string, 0),
	}

	a.InitGrants()

	return a
}

func (auth *Auth) InitGrants() {
	for _, v := range config.GetGrantType() {
		if strings.HasPrefix(v, "server") {
			auth.ServerGrantOpts = append(auth.ServerGrantOpts, v)
		} else {
			auth.ClusterGrantOpts = append(auth.ClusterGrantOpts, v)
		}
	}

	slices.Sort(auth.ServerGrantOpts)
	slices.Sort(auth.ClusterGrantOpts)
}

func (auth *Auth) LogAttempt(user user.UserCredentials) (*AuthTry, error) {
	var auth_try *AuthTry

	initAuth := new(AuthTry)
	initAuth.User = user.Username
	initAuth.Try = 1
	initAuth.Time = time.Now()

	try, ok := auth.Attempts.LoadOrStore(user.Username, initAuth)
	if ok {
		auth_try = try.(*AuthTry)
		// If exceed 3 times
		if auth_try.Try == 3 {
			if time.Now().Before(auth_try.Time.Add(3 * time.Minute)) {
				return auth_try, fmt.Errorf("3 authentication errors for the user " + user.Username + ", please try again in 3 minutes")
			} else {
				auth_try.Try = 1
				auth_try.Time = time.Now()
			}
		} else {
			auth_try.Try += 1
			auth_try.Time = time.Now()
		}
	} else {
		auth_try = initAuth
	}

	return auth_try, nil
}
