package auth

import (
	"fmt"
	"slices"
	"strings"
	"sync"
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
	Users            map[string]*user.User
	ServerGrantOpts  []string
	ClusterGrantOpts []string
	mu               sync.RWMutex
}

func InitAuth() Auth {
	a := Auth{
		Attempts:         NewAuthTryMap(),
		Users:            make(map[string]*user.User),
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

// StoreUser stores user data in the userStore.
func (auth *Auth) AddUser(username string, user *user.User) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	auth.Users[username] = user
}

// LoadUser retrieves user data from the userStore.
func (auth *Auth) LoadUser(username string) (*user.User, bool) {
	auth.mu.RLock()
	defer auth.mu.RUnlock()
	u, exists := auth.Users[username]
	return u, exists
}

// LoadOrStoreUser retrieves user data or stores a default user if not found.
func (auth *Auth) LoadOrStoreUser(username string, defaultUser *user.User) (*user.User, bool) {
	auth.mu.RLock()
	u, exists := auth.Users[username]
	auth.mu.RUnlock()

	if exists {
		return u, true
	}

	// If not exists, store the default value.
	auth.mu.Lock()
	defer auth.mu.Unlock()

	// Double-check existence before storing.
	if u, exists = auth.Users[username]; !exists {
		auth.Users[username] = defaultUser
		return defaultUser, false
	}
	return u, true
}

// DeleteUser removes a user from the userStore.
func (auth *Auth) DeleteUser(username string) {
	auth.mu.Lock()
	defer auth.mu.Unlock()
	u, ok := auth.LoadUser(username)
	if ok {
		u.WG.Wait()
	}
	delete(auth.Users, username)
}
