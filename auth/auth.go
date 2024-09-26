package auth

import (
	"fmt"
	"time"

	"github.com/signal18/replication-manager/auth/user"
)

type AuthTry struct {
	User string    `json:"username"`
	Try  int       `json:"try"`
	Time time.Time `json:"time"`
}

// Users will be stored here
type Auth struct {
	Attempts *AuthTryMap
	Users    *user.UserMap
}

func InitAuth() Auth {
	return Auth{
		Attempts: NewAuthTryMap(),
		Users:    user.NewUserMap(),
	}
}

func (auth *Auth) LogAttempt(user user.UserCredentials) (*AuthTry, error) {
	var auth_try *AuthTry

	if auth_try, ok := auth.Attempts.CheckAndGet(user.Username); ok {
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
		}
	} else {
		auth_try = new(AuthTry)
		auth_try.User = user.Username
		auth_try.Try = 1
		auth_try.Time = time.Now()
		auth.Attempts.Set(user.Username, auth_try)
	}

	return auth_try, nil
}

func (auth *Auth) LoginAttempt(user user.UserCredentials) (*user.User, error) {
	auth_try, err := auth.LogAttempt(user)
	if err != nil {
		return nil, err
	}

	u, ok := auth.Users.CheckAndGet(user.Username)
	if !ok || user.Password != u.Password {
		return nil, fmt.Errorf("invalid credentials")
	}

	auth_try.User = user.Username
	auth_try.Try = 1
	auth_try.Time = time.Now()

	return u, nil
}
